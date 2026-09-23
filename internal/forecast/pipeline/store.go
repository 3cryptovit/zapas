package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// storeAll записывает прогнозы и метрики всех позиций одной транзакцией.
//
// Поштучная запись означала бы по транзакции на позицию и 14 запросов
// внутри каждой; у 40 позиций это 560 заходов в базу вместо двух.
// Ночная задача обязана уложиться в 5 минут на 500 тенантов (§12).
func (s *Service) storeAll(
	ctx context.Context, t tenant.Tenant,
	results map[uuid.UUID]forecast.Result, today clock.Day, now time.Time,
) error {
	return s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		var (
			withoutModel []uuid.UUID
			fcItems      []uuid.UUID
			fcDays       []pgtype.Date
			fcQty        []decimal.Decimal
			fcModels     []string
		)

		for itemID, result := range results {
			// Модель M0: прогноза нет. Старый горизонт сносим, чтобы статус
			// не считался по вчерашним числам.
			if result.Model == forecast.ModelM0 || len(result.Points) == 0 {
				withoutModel = append(withoutModel, itemID)
			} else {
				for _, p := range result.Points {
					fcItems = append(fcItems, itemID)
					fcDays = append(fcDays, postgres.Date(p.Day))
					fcQty = append(fcQty, postgres.DecimalOf(p.Qty))
					fcModels = append(fcModels, string(result.Model))
				}
			}

			var alpha *float64
			if result.Metrics.Alpha > 0 {
				a := result.Metrics.Alpha
				alpha = &a
			}
			if err := q.UpsertForecastMetrics(ctx, sqlc.UpsertForecastMetricsParams{
				TenantID:     t.ID,
				ItemID:       itemID,
				Model:        sqlc.ForecastModel(result.Model),
				Wape:         result.Metrics.WAPE,
				Bias:         result.Metrics.Bias,
				Sigma:        result.Metrics.Sigma,
				Alpha:        alpha,
				WindowDays:   int16(result.Metrics.WindowDays),
				DaysWithData: int16(result.Metrics.DaysWithData),
				ComputedAt:   postgres.Time(now),
			}); err != nil {
				return fmt.Errorf("pipeline: метрики: %w", err)
			}
		}

		if len(withoutModel) > 0 {
			if _, err := q.DeleteForecastsFromForItems(ctx, sqlc.DeleteForecastsFromForItemsParams{
				TenantID: t.ID,
				Day:      postgres.Date(today),
				ItemIds:  withoutModel,
			}); err != nil {
				return fmt.Errorf("pipeline: очистка прогноза: %w", err)
			}
		}

		if len(fcItems) > 0 {
			if _, err := q.BulkUpsertForecasts(ctx, sqlc.BulkUpsertForecastsParams{
				TenantID:   t.ID,
				ItemIds:    fcItems,
				Days:       fcDays,
				Quantities: fcQty,
				Models:     fcModels,
				ComputedAt: postgres.Time(now),
			}); err != nil {
				return fmt.Errorf("pipeline: запись прогноза: %w", err)
			}
		}
		return nil
	})
}

// saveSeries записывает дневной ряд расхода одним запросом.
func (s *Service) saveSeries(
	ctx context.Context, q *sqlc.Queries, t tenant.Tenant,
	series map[uuid.UUID][]forecast.Observation,
) error {
	var (
		items    []uuid.UUID
		days     []pgtype.Date
		values   []decimal.Decimal
		hasData  []bool
		stockout []bool
	)

	for itemID, observations := range series {
		for _, obs := range observations {
			items = append(items, itemID)
			days = append(days, postgres.Date(obs.Day))
			values = append(values, postgres.DecimalOf(obs.Qty))
			hasData = append(hasData, obs.HasData)
			stockout = append(stockout, obs.Stockout)
		}
	}
	if len(items) == 0 {
		return nil
	}

	if _, err := q.BulkUpsertDailyConsumption(ctx, sqlc.BulkUpsertDailyConsumptionParams{
		TenantID:   t.ID,
		ItemIds:    items,
		Days:       days,
		Quantities: values,
		HasData:    hasData,
		Stockouts:  stockout,
	}); err != nil {
		return fmt.Errorf("pipeline: запись дневного расхода: %w", err)
	}
	return nil
}
