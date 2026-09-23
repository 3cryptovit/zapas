package dashboard

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
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Insights — карточка позиции (§6.2).
type Insights struct {
	Today    clock.Day    `json:"today"`
	Item     ItemInfo     `json:"item"`
	Status   StatusInfo   `json:"status"`
	Accuracy AccuracyInfo `json:"accuracy"`
	// History — фактический расход за 60 дней: столбцы графика спроса.
	History []HistoryPoint `json:"history"`
	// Forecast — прогноз на 14 дней: линия графика.
	Forecast []ForecastPoint `json:"forecast"`
	// Projection — проекция остатка на горизонт прогноза с учётом поставок.
	Projection []ProjectionPoint `json:"projection"`
	// Incoming — ожидаемые поставки: метки на графике остатка.
	Incoming []IncomingDelivery `json:"incoming"`
}

// ItemInfo — параметры позиции.
type ItemInfo struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	BaseUnit     string     `json:"base_unit"`
	CategoryName string     `json:"category_name,omitempty"`
	ServiceLevel int        `json:"service_level"`
	ManualMinQty qty.Qty    `json:"manual_min_qty"`
	SupplierName string     `json:"supplier_name,omitempty"`
	SupplierID   *uuid.UUID `json:"supplier_id,omitempty"`
	PurchaseUnit string     `json:"purchase_unit,omitempty"`
	UnitFactor   qty.Qty    `json:"unit_factor"`
}

// StatusInfo — статус и блок «почему такой статус».
type StatusInfo struct {
	Code           string     `json:"code"`
	OnHand         qty.Qty    `json:"on_hand"`
	OnOrder        qty.Qty    `json:"on_order"`
	StockoutDate   *clock.Day `json:"stockout_date,omitempty"`
	OrderBy        *time.Time `json:"order_by,omitempty"`
	RecommendedQty qty.Qty    `json:"recommended_qty"`
	PurchaseQty    *qty.Qty   `json:"purchase_qty,omitempty"`
	TargetLevel    qty.Qty    `json:"target_level"`
	SafetyStock    qty.Qty    `json:"safety_stock"`
	// Explanation — расчёт числами; интерфейс собирает из него фразу
	// «Сейчас 18 л… не хватает 17 л → заказать 2 кор.» (§6.2).
	Explanation replenishment.Explanation `json:"explanation"`
}

// AccuracyInfo — «86% за 28 дней, модель M2, завышает на 3%» (§6.2).
type AccuracyInfo struct {
	Model string `json:"model"`
	// Accuracy = 1 − WAPE, не ниже нуля.
	Accuracy     float64 `json:"accuracy"`
	WAPE         float64 `json:"wape"`
	Bias         float64 `json:"bias"`
	Sigma        float64 `json:"sigma"`
	WindowDays   int     `json:"window_days"`
	DaysWithData int     `json:"days_with_data"`
}

// HistoryPoint — день фактического расхода.
type HistoryPoint struct {
	Day clock.Day `json:"day"`
	Qty qty.Qty   `json:"qty"`
	// HasData: день без данных помечается отдельно, он не ноль (§4.1).
	HasData bool `json:"has_data"`
	// Stockout: в этот день остаток доходил до нуля.
	Stockout bool `json:"stockout"`
}

// ForecastPoint — день прогноза с полосой ±z·σ (§6.2).
type ForecastPoint struct {
	Day   clock.Day `json:"day"`
	Qty   qty.Qty   `json:"qty"`
	Lower qty.Qty   `json:"lower"`
	Upper qty.Qty   `json:"upper"`
}

// ProjectionPoint — ожидаемый остаток на конец дня.
type ProjectionPoint struct {
	Day      clock.Day `json:"day"`
	OnHand   qty.Qty   `json:"on_hand"`
	Incoming qty.Qty   `json:"incoming"`
}

// IncomingDelivery — ожидаемая поставка.
type IncomingDelivery struct {
	Day clock.Day `json:"day"`
	Qty qty.Qty   `json:"qty"`
}

// ErrNotFound — позиции нет или она чужая.
var ErrNotFound = fmt.Errorf("dashboard: позиция не найдена")

// LoadInsights собирает карточку позиции.
func (s *Service) LoadInsights(ctx context.Context, t tenant.Tenant, itemID uuid.UUID) (Insights, error) {
	today := t.Today(s.clock)
	out := Insights{
		Today:      today,
		History:    []HistoryPoint{},
		Forecast:   []ForecastPoint{},
		Projection: []ProjectionPoint{},
		Incoming:   []IncomingDelivery{},
	}

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		row, err := q.ItemInsights(ctx, sqlc.ItemInsightsParams{TenantID: t.ID, ID: itemID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("dashboard: карточка позиции: %w", err)
		}

		out.Item = ItemInfo{
			ID:           row.ItemID,
			Name:         row.ItemName,
			BaseUnit:     row.BaseUnit,
			ServiceLevel: int(row.ServiceLevel),
			ManualMinQty: postgres.Qty(row.ManualMinQty),
			PurchaseUnit: row.PurchaseUnit,
			UnitFactor:   postgres.Qty(row.UnitFactor),
		}
		if row.CategoryName != nil {
			out.Item.CategoryName = *row.CategoryName
		}
		if row.SupplierName != nil {
			out.Item.SupplierName = *row.SupplierName
		}
		if row.SupplierID.Valid {
			id := row.SupplierID.UUID
			out.Item.SupplierID = &id
		}

		out.Status = StatusInfo{
			Code:           string(row.Status),
			OnHand:         postgres.Qty(row.OnHand),
			OnOrder:        postgres.Qty(row.OnOrder),
			RecommendedQty: postgres.Qty(row.RecommendedQty),
			TargetLevel:    postgres.Qty(row.TargetLevel),
			SafetyStock:    postgres.Qty(row.SafetyStock),
		}
		if row.StockoutDate.Valid {
			d := postgres.Day(row.StockoutDate)
			out.Status.StockoutDate = &d
		}
		if row.OrderBy.Valid {
			at := row.OrderBy.Time
			out.Status.OrderBy = &at
		}
		if packs, ok := inPurchaseUnits(out.Status.RecommendedQty, out.Item.UnitFactor, out.Item.PurchaseUnit); ok {
			out.Status.PurchaseQty = &packs
		}
		// Объяснение хранится как jsonb; испорченное не должно ронять карточку.
		if len(row.Explanation) > 0 {
			_ = json.Unmarshal(row.Explanation, &out.Status.Explanation)
		}

		model := forecast.ModelM0
		if row.ForecastModel != nil {
			model = forecast.Model(*row.ForecastModel)
		}
		metrics := forecast.Metrics{
			Model:        model,
			WAPE:         row.Wape,
			Bias:         row.Bias,
			Sigma:        row.Sigma,
			DaysWithData: int(row.DaysWithData),
			WindowDays:   forecast.BacktestDays,
		}
		out.Accuracy = AccuracyInfo{
			Model:        string(model),
			Accuracy:     metrics.Accuracy(),
			WAPE:         metrics.WAPE,
			Bias:         metrics.Bias,
			Sigma:        metrics.Sigma,
			WindowDays:   metrics.WindowDays,
			DaysWithData: metrics.DaysWithData,
		}

		historyRows, err := q.ItemHistory(ctx, sqlc.ItemHistoryParams{
			TenantID: t.ID, ItemID: itemID, Day: postgres.Date(today.AddDays(-HistoryDays)),
		})
		if err != nil {
			return fmt.Errorf("dashboard: история расхода: %w", err)
		}
		for _, h := range historyRows {
			out.History = append(out.History, HistoryPoint{
				Day:      postgres.Day(h.Day),
				Qty:      postgres.Qty(h.Qty),
				HasData:  h.HasData,
				Stockout: h.Stockout,
			})
		}

		forecastRows, err := q.ListForecastForItem(ctx, sqlc.ListForecastForItemParams{
			TenantID: t.ID, ItemID: itemID, Day: postgres.Date(today),
		})
		if err != nil {
			return fmt.Errorf("dashboard: прогноз: %w", err)
		}
		// Полоса неопределённости ±z·σ: та же σ, что идёт в страховой запас.
		z := replenishment.ZFactor(int(row.ServiceLevel))
		band := qty.FromInt(1).MulFloat(z * row.Sigma)
		for _, f := range forecastRows {
			value := postgres.Qty(f.Qty)
			out.Forecast = append(out.Forecast, ForecastPoint{
				Day:   postgres.Day(f.Day),
				Qty:   value,
				Lower: value.Sub(band).ClampZero(),
				Upper: value.Add(band),
			})
		}

		incomingRows, err := q.ListIncomingForItem(ctx, sqlc.ListIncomingForItemParams{
			TenantID: t.ID, ItemID: itemID,
		})
		if err != nil {
			return fmt.Errorf("dashboard: поставки в пути: %w", err)
		}
		for _, inc := range incomingRows {
			if !inc.ExpectedAt.Valid {
				continue
			}
			out.Incoming = append(out.Incoming, IncomingDelivery{
				Day: postgres.Day(inc.ExpectedAt),
				Qty: postgres.Qty(inc.Qty),
			})
		}

		out.Projection = project(out.Status.OnHand, out.Forecast, out.Incoming)
		return nil
	})
	return out, err
}

// project строит проекцию остатка: от текущего остатка по дням вычитается
// прогноз и прибавляются ожидаемые поставки (§5.2, §6.2).
func project(onHand qty.Qty, points []ForecastPoint, incoming []IncomingDelivery) []ProjectionPoint {
	out := make([]ProjectionPoint, 0, len(points))
	balance := onHand

	byDay := map[clock.Day]qty.Qty{}
	for _, inc := range incoming {
		byDay[inc.Day] = byDay[inc.Day].Add(inc.Qty)
	}

	for _, p := range points {
		arrived := byDay[p.Day]
		balance = balance.Add(arrived).Sub(p.Qty)
		out = append(out, ProjectionPoint{
			Day:      p.Day,
			OnHand:   balance,
			Incoming: arrived,
		})
	}
	return out
}
