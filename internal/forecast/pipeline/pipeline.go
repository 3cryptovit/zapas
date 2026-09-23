package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// StatusRefresher пересчитывает статусы всех позиций тенанта после прогноза.
// Интерфейс, чтобы пакет не зависел от replenishment напрямую.
type StatusRefresher interface {
	RecalculateAll(ctx context.Context, t tenant.Tenant) error
}

// Service — ночной пересчёт прогноза (§4.4).
type Service struct {
	db       *postgres.DB
	clock    clock.Clock
	statuses StatusRefresher
	log      *slog.Logger
}

func NewService(db *postgres.DB, cl clock.Clock, statuses StatusRefresher, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, clock: cl, statuses: statuses, log: log}
}

// Result — что получилось за прогон.
type Result struct {
	Items      int
	WithModel  int
	AvgWAPE    float64
	ComputedAt time.Time
}

// Run пересобирает ряд расхода, считает модели и бэктест, записывает прогноз
// на 14 дней и метрики, затем обновляет статусы.
//
// Задача идемпотентна: повторный запуск за ту же дату перезаписывает результат,
// а не дублирует его (§4.4).
func (s *Service) Run(ctx context.Context, t tenant.Tenant) (Result, error) {
	now := t.Now(s.clock)
	today := t.Today(s.clock)
	from := today.AddDays(-forecast.HistoryDays)

	series, err := s.rebuildSeries(ctx, t, from, today)
	if err != nil {
		return Result{}, err
	}

	result := Result{ComputedAt: now}
	var wapeSum float64

	computed := make(map[uuid.UUID]forecast.Result, len(series))
	for itemID, observations := range series {
		r := forecast.Compute(observations, today, forecast.HorizonDays)
		computed[itemID] = r

		result.Items++
		if r.Model != forecast.ModelM0 {
			result.WithModel++
			wapeSum += r.Metrics.WAPE
		}
	}

	if err := s.storeAll(ctx, t, computed, today, now); err != nil {
		return Result{}, err
	}
	if result.WithModel > 0 {
		result.AvgWAPE = wapeSum / float64(result.WithModel)
	}

	// Статусы считаются уже по свежему прогнозу.
	if s.statuses != nil {
		if err := s.statuses.RecalculateAll(ctx, t); err != nil {
			return Result{}, fmt.Errorf("pipeline: пересчёт статусов: %w", err)
		}
	}

	s.log.InfoContext(ctx, "прогноз пересчитан",
		slog.String("tenant_id", t.ID.String()),
		slog.Int("items", result.Items),
		slog.Int("with_model", result.WithModel),
		slog.Float64("avg_wape", result.AvgWAPE),
	)
	return result, nil
}

// rebuildSeries пересобирает daily_consumption за окно истории и возвращает
// ряд для каждой позиции.
func (s *Service) rebuildSeries(ctx context.Context, t tenant.Tenant, from, today clock.Day) (map[uuid.UUID][]forecast.Observation, error) {
	tz := t.Loc().String()
	since := from.Time(t.Loc())
	until := today.Time(t.Loc()) // сегодняшний день в обучение не идёт

	var series map[uuid.UUID][]forecast.Observation

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		rows, err := q.ListConsumptionMovementsForSeries(ctx, sqlc.ListConsumptionMovementsForSeriesParams{
			TenantID: t.ID,
			Tz:       tz,
			Since:    postgres.Time(since),
			Until:    postgres.Time(until),
		})
		if err != nil {
			return fmt.Errorf("pipeline: движения расхода: %w", err)
		}

		movements := make([]rawMovement, 0, len(rows))
		for _, r := range rows {
			movements = append(movements, rawMovement{
				ItemID:    r.ItemID,
				Day:       postgres.Day(r.Day),
				Qty:       postgres.Qty(r.Qty),
				FromCount: r.CountID.Valid,
			})
		}

		dayRows, err := q.ListDaysWithUsage(ctx, sqlc.ListDaysWithUsageParams{
			TenantID: t.ID, Tz: tz, Since: postgres.Time(since),
		})
		if err != nil {
			return fmt.Errorf("pipeline: дни с данными: %w", err)
		}
		daysWithData := make(map[clock.Day]bool, len(dayRows))
		for _, d := range dayRows {
			daysWithData[postgres.Day(d)] = true
		}

		stockoutRows, err := q.ListStockoutDays(ctx, sqlc.ListStockoutDaysParams{
			TenantID: t.ID, Tz: tz, Until: postgres.Time(until),
		})
		if err != nil {
			return fmt.Errorf("pipeline: дни дефицита: %w", err)
		}
		stockoutDays := make(map[itemDay]bool, len(stockoutRows))
		for _, r := range stockoutRows {
			stockoutDays[itemDay{r.ItemID, postgres.Day(r.Day)}] = true
		}

		// Позиции без единого движения тоже должны попасть в результат:
		// иначе у новой позиции не будет ни ряда, ни статуса.
		itemIDs, err := q.ListActiveItemIDs(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("pipeline: список позиций: %w", err)
		}

		yesterday := today.AddDays(-1)
		series = buildSeries(movements, daysWithData, stockoutDays, from, yesterday)
		for _, id := range itemIDs {
			if _, ok := series[id]; !ok {
				series[id] = nil
			}
		}

		// Сохраняем ряд: он нужен графику спроса в карточке (§6.2).
		// Одним запросом: у 40 позиций за 90 дней это 3600 строк.
		if err := s.saveSeries(ctx, q, t, series); err != nil {
			return err
		}

		// Старые дни за пределами окна истории не нужны.
		if _, err := q.DeleteDailyConsumptionBefore(ctx, sqlc.DeleteDailyConsumptionBeforeParams{
			TenantID: t.ID, Day: postgres.Date(from),
		}); err != nil {
			return fmt.Errorf("pipeline: очистка старых дней: %w", err)
		}
		return nil
	})
	return series, err
}
