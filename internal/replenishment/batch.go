package replenishment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// RecalculateAll пересчитывает статусы всех позиций тенанта.
//
// В отличие от пересчёта одной позиции, здесь четыре запроса на весь тенант,
// а не на позицию: ночная задача обязана уложиться в 5 минут на 500 тенантов
// по 40 позиций (§12).
func (s *Service) RecalculateAll(ctx context.Context, t tenant.Tenant) error {
	today := t.Today(s.clock)
	now := t.Now(s.clock)
	tz := t.Loc().String()

	return s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		items, err := q.ListItemsForStatus(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("replenishment: список позиций: %w", err)
		}
		if len(items) == 0 {
			return nil
		}

		forecasts, err := s.loadForecasts(ctx, q, t, today)
		if err != nil {
			return err
		}
		incoming, err := s.loadIncoming(ctx, q, t)
		if err != nil {
			return err
		}
		consumed, err := s.loadConsumedToday(ctx, q, t, tz, today)
		if err != nil {
			return err
		}
		previous, err := s.loadPreviousStatuses(ctx, q, t)
		if err != nil {
			return err
		}

		for _, row := range items {
			in := Input{
				Today:         today,
				OnHand:        postgres.Qty(row.OnHand),
				Incoming:      incoming[row.ItemID],
				Forecast:      buildResult(row, forecasts[row.ItemID]),
				ConsumedToday: consumed[row.ItemID],
				ServiceLevel:  int(row.ServiceLevel),
				ManualMin:     postgres.Qty(row.ManualMinQty),
				MinOrder:      postgres.Qty(row.MinOrderQty),
				Pack:          postgres.Qty(row.PackMultiple),
			}
			in.Window = windowFor(row, now, today, t)

			decision := Decide(in)

			if err := s.persistBatch(ctx, q, t, row, decision); err != nil {
				return err
			}

			if s.alerter != nil && decision.Status.IsRed() &&
				decision.Status.WorseThan(previous[row.ItemID]) {
				alert := RedAlert{
					ItemID:         row.ItemID,
					ItemName:       row.ItemName,
					BaseUnit:       row.BaseUnit,
					Status:         decision.Status,
					PreviousStatus: previous[row.ItemID],
					OnHand:         postgres.Qty(row.OnHand),
					StockoutDate:   decision.StockoutDate,
					NextDelivery:   in.Window.D1,
				}
				if row.SupplierName != nil {
					alert.SupplierName = *row.SupplierName
				}
				if err := s.alerter.ItemWentRed(ctx, tx, t, alert); err != nil {
					return fmt.Errorf("replenishment: срочный алерт: %w", err)
				}
			}
		}
		return nil
	})
}

func (s *Service) loadForecasts(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, today clock.Day) (map[uuid.UUID][]forecast.Point, error) {
	rows, err := q.ListForecastsFrom(ctx, sqlc.ListForecastsFromParams{
		TenantID: t.ID, Day: postgres.Date(today),
	})
	if err != nil {
		return nil, fmt.Errorf("replenishment: прогнозы: %w", err)
	}
	out := map[uuid.UUID][]forecast.Point{}
	for _, r := range rows {
		out[r.ItemID] = append(out[r.ItemID], forecast.Point{
			Day: postgres.Day(r.Day),
			Qty: postgres.Qty(r.Qty),
		})
	}
	return out, nil
}

func (s *Service) loadIncoming(ctx context.Context, q *sqlc.Queries, t tenant.Tenant) (map[uuid.UUID][]Delivery, error) {
	rows, err := q.ListIncomingByItem(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("replenishment: поставки в пути: %w", err)
	}
	out := map[uuid.UUID][]Delivery{}
	for _, r := range rows {
		if !r.ExpectedAt.Valid {
			continue
		}
		out[r.ItemID] = append(out[r.ItemID], Delivery{
			Day: postgres.Day(r.ExpectedAt),
			Qty: postgres.Qty(r.Qty),
		})
	}
	return out, nil
}

func (s *Service) loadConsumedToday(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, tz string, today clock.Day) (map[uuid.UUID]qty.Qty, error) {
	rows, err := q.SumConsumptionByItemForDay(ctx, sqlc.SumConsumptionByItemForDayParams{
		TenantID: t.ID, Tz: tz, Day: postgres.Date(today),
	})
	if err != nil {
		return nil, fmt.Errorf("replenishment: расход за сегодня: %w", err)
	}
	out := make(map[uuid.UUID]qty.Qty, len(rows))
	for _, r := range rows {
		out[r.ItemID] = postgres.Qty(r.Consumed)
	}
	return out, nil
}

func (s *Service) loadPreviousStatuses(ctx context.Context, q *sqlc.Queries, t tenant.Tenant) (map[uuid.UUID]Status, error) {
	rows, err := q.ListItemStatuses(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("replenishment: прежние статусы: %w", err)
	}
	out := make(map[uuid.UUID]Status, len(rows))
	for _, r := range rows {
		out[r.ItemID] = Status(r.Status)
	}
	return out, nil
}

// buildResult собирает forecast.Result из строки метрик и точек прогноза.
func buildResult(row sqlc.ListItemsForStatusRow, points []forecast.Point) forecast.Result {
	model := forecast.ModelM0
	if row.MetricsModel != nil {
		model = forecast.Model(*row.MetricsModel)
	}
	if len(points) == 0 {
		model = forecast.ModelM0
	}
	return forecast.Result{
		Model:  model,
		Points: points,
		Metrics: forecast.Metrics{
			Model: model,
			Sigma: row.Sigma,
			WAPE:  row.Wape,
			Bias:  row.Bias,
		},
	}
}

// windowFor считает окна поставки по условиям поставщика позиции.
// Без поставщика окна не существует: система не может сказать, когда
// заказывать, поэтому «заказать сегодня» такой позиции не предлагается.
func windowFor(row sqlc.ListItemsForStatusRow, now time.Time, today clock.Day, t tenant.Tenant) Window {
	if !row.SupplierID.Valid {
		return Window{
			OrderDay: today,
			D1:       today.AddDays(1),
			D2:       today.AddDays(1),
			OrderBy:  now,
		}
	}
	lead := 0
	if row.LeadTimeDays != nil {
		lead = int(*row.LeadTimeDays)
	}
	return ComputeWindow(now, t.Loc(), Supplier{
		LeadTimeDays:     lead,
		DeliveryWeekdays: weekdaysFromDB(row.DeliveryWeekdays),
		Cutoff:           postgres.ClockTime(row.OrderCutoff),
	})
}

func (s *Service) persistBatch(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, row sqlc.ListItemsForStatusRow, d Decision) error {
	explanation, err := json.Marshal(d.Explanation)
	if err != nil {
		return fmt.Errorf("replenishment: объяснение статуса: %w", err)
	}

	params := sqlc.UpsertItemStatusParams{
		TenantID:       t.ID,
		ItemID:         row.ItemID,
		Status:         sqlc.ItemStatusCode(d.Status),
		OnOrder:        postgres.DecimalOf(d.OnOrder),
		StockoutDate:   postgres.NullDate(d.StockoutDate),
		RecommendedQty: postgres.DecimalOf(d.RecommendedQty),
		TargetLevel:    postgres.DecimalOf(d.TargetLevel),
		SafetyStock:    postgres.DecimalOf(d.SafetyStock),
		SupplierID:     row.SupplierID,
		Explanation:    explanation,
		ComputedAt:     postgres.Time(s.clock.Now()),
	}
	if row.SupplierID.Valid && d.Status == StatusOrderToday {
		params.OrderBy = postgres.Time(d.Explanation.OrderBy)
	}
	return q.UpsertItemStatus(ctx, params)
}
