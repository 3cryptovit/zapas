// Package worker выполняет периодические задачи и разгребает очередь
// отправки уведомлений (§9).
//
// Логика задач лежит в обычных методах Tasks и не знает про asynq:
// так её можно прогнать в тесте, не поднимая Redis.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/platform/metrics"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Имена задач. Они же — типы задач в asynq и метки в метриках.
const (
	TaskForecast  = "forecast:nightly"
	TaskDigest    = "notify:digest"
	TaskCutoff    = "notify:cutoff"
	TaskLate      = "notify:late"
	TaskOutbox    = "notify:outbox"
	TaskReconcile = "stock:reconcile"
	TaskSandboxGC = "sandbox:cleanup"
)

// Tasks — набор фоновых задач.
type Tasks struct {
	// db — роль приложения: всё, что делается в контексте тенанта.
	db *postgres.DB
	// maint — обслуживающая роль: обход тенантов и очередь (ADR-005).
	maint    *postgres.DB
	clock    clock.Clock
	pipeline *pipeline.Service
	notify   *notify.Service
	outbox   *notify.Dispatcher
	stock    *stock.Service
	log      *slog.Logger
}

func NewTasks(
	db, maint *postgres.DB,
	cl clock.Clock,
	pipe *pipeline.Service,
	notifier *notify.Service,
	dispatcher *notify.Dispatcher,
	stocks *stock.Service,
	log *slog.Logger,
) *Tasks {
	if log == nil {
		log = slog.Default()
	}
	return &Tasks{
		db: db, maint: maint, clock: cl,
		pipeline: pipe, notify: notifier, outbox: dispatcher, stock: stocks,
		log: log,
	}
}

// RunForecast пересчитывает прогноз у тенантов, у которых сейчас 03:00
// по местному времени (§4.4).
//
// Задача запускается каждый час и сама выбирает, чья очередь: тенанты
// живут в разных поясах, а «ночь» у каждого своя.
func (t *Tasks) RunForecast(ctx context.Context) error {
	return t.forEachTenantAt(ctx, TaskForecast, 3, func(ctx context.Context, tn tenant.Tenant) error {
		_, err := t.pipeline.Run(ctx, tn)
		return err
	})
}

// RunDigest шлёт ежедневную сводку тем, у кого наступило заданное время (§5.5).
func (t *Tasks) RunDigest(ctx context.Context) error {
	start := time.Now()
	err := t.forEachTenant(ctx, func(ctx context.Context, tn tenant.Tenant) error {
		if tn.IsSandbox {
			// В песочнице сводка появляется при промотке времени, а не по часам.
			return nil
		}
		if tn.Now(t.clock).In(tn.Loc()).Hour() != tn.Settings.DigestAt.Hour {
			return nil
		}
		_, err := t.notify.SendDigest(ctx, tn)
		return err
	})
	metrics.ObserveTask(TaskDigest, start, err)
	return err
}

// RunCutoffReminders напоминает за два часа до отсечки.
func (t *Tasks) RunCutoffReminders(ctx context.Context) error {
	start := time.Now()
	err := t.forEachTenant(ctx, func(ctx context.Context, tn tenant.Tenant) error {
		if tn.IsSandbox {
			return nil
		}
		_, err := t.notify.SendCutoffReminders(ctx, tn)
		return err
	})
	metrics.ObserveTask(TaskCutoff, start, err)
	return err
}

// RunLateAlerts сообщает о непринятых поставках (FR-21).
func (t *Tasks) RunLateAlerts(ctx context.Context) error {
	start := time.Now()
	err := t.forEachTenant(ctx, func(ctx context.Context, tn tenant.Tenant) error {
		_, err := t.notify.SendLateAlerts(ctx, tn)
		return err
	})
	metrics.ObserveTask(TaskLate, start, err)
	return err
}

// RunOutbox разгребает очередь отправки (ADR-004).
func (t *Tasks) RunOutbox(ctx context.Context) error {
	start := time.Now()
	result, err := t.outbox.Dispatch(ctx, t.clock.Now())
	metrics.ObserveTask(TaskOutbox, start, err)

	if err == nil && result.Claimed > 0 {
		t.log.InfoContext(ctx, "очередь уведомлений обработана",
			slog.Int("claimed", result.Claimed),
			slog.Int("sent", result.Sent),
			slog.Int("retried", result.Retried),
			slog.Int("failed", result.Failed),
		)
	}
	return err
}

// RunReconcile сверяет остатки с журналом (FR-17).
//
// Таблица остатков — кэш, и расхождение должна находить система,
// а не владелец, который однажды не поверит цифре.
func (t *Tasks) RunReconcile(ctx context.Context) error {
	start := time.Now()

	err := t.forEachTenant(ctx, func(ctx context.Context, tn tenant.Tenant) error {
		drifts, err := t.stock.Reconcile(ctx, tn)
		if err != nil {
			return err
		}
		for _, d := range drifts {
			metrics.BalanceDrift.Inc()
			// Расхождение — это баг, а не штатная ситуация: в лог уровнем
			// ERROR, чтобы Sentry его поднял.
			t.log.ErrorContext(ctx, "остаток разошёлся с журналом",
				slog.String("tenant_id", tn.ID.String()),
				slog.String("item_id", d.ItemID.String()),
				slog.String("journal", d.Journal.String()),
				slog.String("balance", d.Balance.String()),
			)
		}
		return nil
	})

	metrics.ObserveTask(TaskReconcile, start, err)
	return err
}

// observeTask — обёртка над метрикой длительности задачи.
func observeTask(name string, start time.Time, err error) {
	metrics.ObserveTask(name, start, err)
}

// forEachTenantAt выполняет fn у тенантов, у которых сейчас заданный час
// по местному времени.
func (t *Tasks) forEachTenantAt(ctx context.Context, task string, hour int, fn func(context.Context, tenant.Tenant) error) error {
	start := time.Now()

	err := t.forEachTenant(ctx, func(ctx context.Context, tn tenant.Tenant) error {
		if tn.Now(t.clock).In(tn.Loc()).Hour() != hour {
			return nil
		}
		return fn(ctx, tn)
	})

	metrics.ObserveTask(task, start, err)
	return err
}

// forEachTenant обходит всех живых тенантов.
//
// Список читается обслуживающей ролью: тенант ещё не известен, а RLS
// под ролью приложения вернула бы ноль строк (ADR-005).
func (t *Tasks) forEachTenant(ctx context.Context, fn func(context.Context, tenant.Tenant) error) error {
	tenants, err := t.listTenants(ctx)
	if err != nil {
		return err
	}

	var failed int
	for _, tn := range tenants {
		if err := fn(ctx, tn); err != nil {
			// Падение одного тенанта не должно останавливать обход:
			// иначе один сломанный клиент лишает сводки всех остальных.
			failed++
			t.log.ErrorContext(ctx, "задача упала на тенанте",
				slog.String("tenant_id", tn.ID.String()),
				slog.String("err", err.Error()),
			)
		}
	}
	if failed > 0 {
		return fmt.Errorf("worker: задача упала у %d тенантов из %d", failed, len(tenants))
	}
	return nil
}

func (t *Tasks) listTenants(ctx context.Context) ([]tenant.Tenant, error) {
	var out []tenant.Tenant

	err := t.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListActiveTenants(ctx, postgres.Time(t.clock.Now()))
		if err != nil {
			return fmt.Errorf("worker: список тенантов: %w", err)
		}
		for _, r := range rows {
			out = append(out, tenant.Tenant{
				ID:          r.ID,
				Name:        r.Name,
				Location:    tenant.LoadLocation(r.Timezone),
				IsSandbox:   r.IsSandbox,
				ClockOffset: postgres.Duration(r.ClockOffset),
				ExpiresAt:   postgres.TimePtr(r.ExpiresAt),
				Autopilot:   r.Autopilot,
				Seed:        r.Seed,
				Settings:    tenant.ParseSettings(r.Settings),
			})
		}
		return nil
	})
	return out, err
}
