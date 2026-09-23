package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/metrics"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

// Sender отправляет сообщение в конкретный канал.
//
// Реализации: Telegram Bot API и SMTP. В тестах — заглушка, которая
// складывает сообщения в память.
type Sender interface {
	Send(ctx context.Context, recipient string, payload Payload) error
}

// ErrPermanent помечает ошибку, которую нет смысла повторять: адресат
// заблокировал бота, email не существует. Такие сразу идут в failed.
var ErrPermanent = errors.New("notify: постоянная ошибка доставки")

// Dispatcher разгребает очередь отправки (ADR-004).
type Dispatcher struct {
	// maint — обслуживающая роль: очередь общая для всех тенантов,
	// и воркер обходит её без привязки к одному (ADR-005).
	maint   *postgres.DB
	senders map[Channel]Sender
	log     *slog.Logger
	// batch — сколько сообщений забирать за раз.
	batch int
}

func NewDispatcher(maint *postgres.DB, senders map[Channel]Sender, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{maint: maint, senders: senders, log: log, batch: 50}
}

// DispatchResult — итог одного прохода по очереди.
type DispatchResult struct {
	Claimed int
	Sent    int
	Retried int
	Failed  int
}

// Dispatch забирает пачку сообщений и пытается их отправить.
//
// Пачка берётся через FOR UPDATE SKIP LOCKED, поэтому несколько воркеров
// могут работать параллельно, не дожидаясь друг друга.
func (d *Dispatcher) Dispatch(ctx context.Context, now time.Time) (DispatchResult, error) {
	var (
		claimed []sqlc.Outbox
		result  DispatchResult
	)

	err := d.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ClaimOutbox(ctx, sqlc.ClaimOutboxParams{
			NextAttemptAt: postgres.Time(now),
			Limit:         int32(d.batch),
		})
		if err != nil {
			return fmt.Errorf("notify: выборка очереди: %w", err)
		}
		claimed = rows
		return nil
	})
	if err != nil {
		return result, err
	}
	result.Claimed = len(claimed)

	for _, msg := range claimed {
		outcome := d.deliver(ctx, msg, now)
		switch outcome {
		case outcomeSent:
			result.Sent++
		case outcomeRetry:
			result.Retried++
		case outcomeFailed:
			result.Failed++
		}
	}

	// Метрика длины очереди: алерт при >100 дольше 10 минут (§13.4).
	if err := d.refreshPendingMetric(ctx); err != nil {
		d.log.WarnContext(ctx, "не удалось обновить метрику очереди",
			slog.String("err", err.Error()))
	}
	return result, nil
}

type outcome int

const (
	outcomeSent outcome = iota
	outcomeRetry
	outcomeFailed
)

func (d *Dispatcher) deliver(ctx context.Context, msg sqlc.Outbox, now time.Time) outcome {
	channel := Channel(msg.Channel)

	sender, ok := d.senders[channel]
	if !ok || sender == nil {
		// Канал не настроен — повторять бессмысленно.
		d.markFailed(ctx, msg.ID, "канал "+string(channel)+" не настроен")
		metrics.OutboxSent.WithLabelValues(string(channel), "unconfigured").Inc()
		return outcomeFailed
	}

	var payload Payload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		d.markFailed(ctx, msg.ID, "нечитаемое содержимое: "+err.Error())
		metrics.OutboxSent.WithLabelValues(string(channel), "malformed").Inc()
		return outcomeFailed
	}

	sendErr := sender.Send(ctx, msg.Recipient, payload)
	if sendErr == nil {
		d.markSent(ctx, msg.ID, now)
		metrics.OutboxSent.WithLabelValues(string(channel), "ok").Inc()
		return outcomeSent
	}

	// Постоянная ошибка или исчерпанные попытки — в failed, дальше в Sentry.
	if errors.Is(sendErr, ErrPermanent) || int(msg.Attempts) >= MaxAttempts {
		d.markFailed(ctx, msg.ID, sendErr.Error())
		metrics.OutboxSent.WithLabelValues(string(channel), "failed").Inc()
		d.log.ErrorContext(ctx, "уведомление не доставлено",
			slog.String("outbox_id", msg.ID.String()),
			slog.String("channel", string(channel)),
			slog.Int("attempts", int(msg.Attempts)),
			slog.String("err", sendErr.Error()),
		)
		return outcomeFailed
	}

	// Экспоненциальная задержка: 1, 5, 25 минут и далее.
	delay := time.Duration(backoff(int(msg.Attempts))) * time.Minute
	d.reschedule(ctx, msg.ID, now.Add(delay), sendErr.Error())
	metrics.OutboxSent.WithLabelValues(string(channel), "retry").Inc()
	return outcomeRetry
}

func (d *Dispatcher) markSent(ctx context.Context, id uuid.UUID, now time.Time) {
	err := d.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).MarkOutboxSent(ctx, sqlc.MarkOutboxSentParams{
			ID: id, SentAt: postgres.Time(now),
		})
	})
	if err != nil {
		d.log.ErrorContext(ctx, "не удалось отметить сообщение отправленным",
			slog.String("outbox_id", id.String()), slog.String("err", err.Error()))
	}
}

func (d *Dispatcher) reschedule(ctx context.Context, id uuid.UUID, at time.Time, reason string) {
	err := d.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).RescheduleOutbox(ctx, sqlc.RescheduleOutboxParams{
			ID: id, NextAttemptAt: postgres.Time(at), LastError: truncate(reason, 500),
		})
	})
	if err != nil {
		d.log.ErrorContext(ctx, "не удалось отложить сообщение",
			slog.String("outbox_id", id.String()), slog.String("err", err.Error()))
	}
}

func (d *Dispatcher) markFailed(ctx context.Context, id uuid.UUID, reason string) {
	err := d.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).MarkOutboxFailed(ctx, sqlc.MarkOutboxFailedParams{
			ID: id, LastError: truncate(reason, 500),
		})
	})
	if err != nil {
		d.log.ErrorContext(ctx, "не удалось отметить сообщение проваленным",
			slog.String("outbox_id", id.String()), slog.String("err", err.Error()))
	}
}

func (d *Dispatcher) refreshPendingMetric(ctx context.Context) error {
	return d.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		pending, err := sqlc.New(tx).CountPendingOutbox(ctx)
		if err != nil {
			return err
		}
		metrics.OutboxPending.Set(float64(pending))
		return nil
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
