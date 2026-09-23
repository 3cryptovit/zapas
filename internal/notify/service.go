package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/orders"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Service пишет уведомления в ленту и ставит их в очередь отправки.
type Service struct {
	db *postgres.DB
	// maint — обслуживающая роль: вебхук Telegram приходит без сессии,
	// и тенант в этот момент известен только из кода привязки (ADR-005).
	maint *postgres.DB
	clock clock.Clock
	// baseURL нужен, чтобы в сообщении была рабочая ссылка на дашборд.
	baseURL string
}

func NewService(db, maint *postgres.DB, cl clock.Clock, baseURL string) *Service {
	return &Service{db: db, maint: maint, clock: cl, baseURL: baseURL}
}

// Emit записывает уведомление в ленту и, если каналы включены, в outbox.
//
// Всё внутри переданной транзакции: уведомление не может разойтись
// с событием, которое его породило (ADR-004).
func (s *Service) Emit(ctx context.Context, tx postgres.Tx, t tenant.Tenant, kind Type, dedupKey string, payload Payload) error {
	q := sqlc.New(tx)

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: сериализация уведомления: %w", err)
	}

	now := s.clock.Now()

	// Дедупликация уникальным индексом: повтор молча не создаёт вторую
	// запись, и ON CONFLICT DO NOTHING вернёт пустой результат (§5.5).
	created, err := q.InsertNotification(ctx, sqlc.InsertNotificationParams{
		ID:        uuid.Must(uuid.NewV7()),
		TenantID:  t.ID,
		Type:      sqlc.NotificationType(kind),
		Payload:   encoded,
		DedupKey:  dedupKey,
		CreatedAt: postgres.Time(now),
	})
	if err != nil {
		if postgres.IsNoRows(err) {
			// Такое уведомление уже было — это штатный путь, не ошибка.
			return nil
		}
		return fmt.Errorf("notify: запись в ленту: %w", err)
	}

	// В песочнице внешние каналы отключены: всё идёт только в ленту (§7.5).
	if t.IsSandbox {
		return nil
	}

	recipients, err := q.ListRecipients(ctx, t.ID)
	if err != nil {
		return fmt.Errorf("notify: получатели: %w", err)
	}

	// Тихие часы: срочные алерты откладываются до утра (§5.5).
	sendAt := now
	if kind == TypeCritical && t.IsQuietHours(now) {
		sendAt = nextMorning(t, now)
	}

	for _, r := range recipients {
		if t.Settings.TelegramEnabled && r.TelegramChatID != nil {
			if err := s.enqueue(ctx, q, t, created.ID, ChannelTelegram,
				fmt.Sprint(*r.TelegramChatID), payload, sendAt); err != nil {
				return err
			}
		}
		// Email шлём только для сводки: остальное в почте только раздражает.
		if t.Settings.EmailEnabled && kind == TypeDailyDigest && r.Email != "" {
			if err := s.enqueue(ctx, q, t, created.ID, ChannelEmail,
				r.Email, payload, sendAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) enqueue(
	ctx context.Context, q *sqlc.Queries, t tenant.Tenant,
	notificationID uuid.UUID, channel Channel, recipient string,
	payload Payload, sendAt time.Time,
) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: сериализация сообщения: %w", err)
	}
	if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{
		ID:             uuid.Must(uuid.NewV7()),
		TenantID:       t.ID,
		NotificationID: uuid.NullUUID{UUID: notificationID, Valid: true},
		Channel:        sqlc.OutboxChannel(channel),
		Recipient:      recipient,
		Payload:        encoded,
		NextAttemptAt:  postgres.Time(sendAt),
	}); err != nil {
		return fmt.Errorf("notify: постановка в очередь: %w", err)
	}
	return nil
}

// nextMorning — ближайший конец тихих часов.
func nextMorning(t tenant.Tenant, now time.Time) time.Time {
	local := now.In(t.Loc())
	morning := time.Date(local.Year(), local.Month(), local.Day(),
		t.Settings.QuietTo.Hour, t.Settings.QuietTo.Minute, 0, 0, t.Loc())

	if !morning.After(local) {
		morning = morning.AddDate(0, 0, 1)
	}
	return morning
}

// --- реализация интерфейсов других модулей ---

// ItemWentRed реализует replenishment.Alerter: позиция стала красной (§5.5).
func (s *Service) ItemWentRed(ctx context.Context, tx postgres.Tx, t tenant.Tenant, ev replenishment.RedAlert) error {
	today := t.Today(s.clock)

	payload := RenderCritical(CriticalData{
		ItemName:     ev.ItemName,
		OnHand:       ev.OnHand,
		Unit:         ev.BaseUnit,
		StockoutDate: ev.StockoutDate,
		Today:        today,
		NextDelivery: ev.NextDelivery,
		SupplierName: ev.SupplierName,
		Link:         s.itemLink(ev.ItemID),
	})

	return s.Emit(ctx, tx, t, TypeCritical, CriticalKey(ev.ItemID, today), payload)
}

// ReceiptMismatch реализует orders.Notifier: факт разошёлся с заказом (§5.5).
func (s *Service) ReceiptMismatch(ctx context.Context, tx postgres.Tx, t tenant.Tenant, order orders.Order) error {
	data := MismatchData{
		SupplierName: order.SupplierName,
		Link:         s.orderLink(order.ID),
	}
	for _, l := range order.Lines {
		if l.QtyReceived == nil {
			continue
		}
		data.Lines = append(data.Lines, MismatchLine{
			Name:     l.ItemName,
			Ordered:  l.QtyOrdered,
			Received: *l.QtyReceived,
			Unit:     l.BaseUnit,
		})
	}

	// Расхождение — история заказа, а не позиции: ключ по заказу.
	return s.Emit(ctx, tx, t, TypeReceiptMismatch, MismatchKey(order.ID), data.payload())
}

func (d MismatchData) payload() Payload { return RenderMismatch(d) }

func (s *Service) itemLink(itemID uuid.UUID) string {
	if s.baseURL == "" {
		return ""
	}
	return s.baseURL + "/app/items/" + itemID.String()
}

func (s *Service) orderLink(orderID uuid.UUID) string {
	if s.baseURL == "" {
		return ""
	}
	return s.baseURL + "/app/orders/" + orderID.String()
}

func (s *Service) dashboardLink() string {
	if s.baseURL == "" {
		return ""
	}
	// Слеш на конце обязателен: по адресу без него отдаётся лендинг,
	// и ссылка из уведомления приводит человека не в кабинет.
	return s.baseURL + "/app/"
}
