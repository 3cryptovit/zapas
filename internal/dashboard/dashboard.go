// Package dashboard собирает главный экран и карточку позиции (§6).
//
// Это read-модель: запросы идут в item_status и forecast_metrics, которые
// заполняет пересчёт статусов. Собирать дашборд из журнала движений на каждый
// запрос нельзя — бюджет экрана 300 мс на 300 позициях (§12).
package dashboard

import (
	"context"
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

// HistoryDays — сколько дней факта показывает график спроса (§6.2).
const HistoryDays = 60

// Service — чтение дашборда.
type Service struct {
	db    *postgres.DB
	clock clock.Clock
}

func NewService(db *postgres.DB, cl clock.Clock) *Service {
	return &Service{db: db, clock: cl}
}

// View — главный экран (§6).
type View struct {
	// Today — виртуальная дата тенанта: интерфейс считает по ней «через 2 дня».
	Today clock.Day `json:"today"`
	// Counters — четыре счётчика по статусам сверху экрана.
	Counters Counters `json:"counters"`
	// Suggestions — блок «Заказать сегодня», сгруппированный по поставщикам.
	Suggestions []SupplierSuggestion `json:"suggestions"`
	Items       []Row                `json:"items"`
}

// Counters — счётчики по статусам.
type Counters struct {
	OutOfStock int `json:"out_of_stock"`
	Critical   int `json:"critical"`
	OrderToday int `json:"order_today"`
	OK         int `json:"ok"`
	NoForecast int `json:"no_forecast"`
	Total      int `json:"total"`
}

// Row — строка таблицы позиций (§6.1).
type Row struct {
	ItemID       uuid.UUID  `json:"item_id"`
	Name         string     `json:"name"`
	BaseUnit     string     `json:"base_unit"`
	CategoryName string     `json:"category_name,omitempty"`
	OnHand       qty.Qty    `json:"on_hand"`
	OnOrder      qty.Qty    `json:"on_order"`
	Status       string     `json:"status"`
	StockoutDate *clock.Day `json:"stockout_date,omitempty"`
	OrderBy      *time.Time `json:"order_by,omitempty"`
	// RecommendedQty в базовой единице; PurchaseQty — она же в единицах закупки.
	RecommendedQty qty.Qty    `json:"recommended_qty"`
	PurchaseQty    *qty.Qty   `json:"purchase_qty,omitempty"`
	PurchaseUnit   string     `json:"purchase_unit,omitempty"`
	SupplierID     *uuid.UUID `json:"supplier_id,omitempty"`
	SupplierName   string     `json:"supplier_name,omitempty"`
	// Accuracy — точность прогноза: 1 − WAPE, но не ниже нуля (§4.3).
	Accuracy *float64 `json:"accuracy,omitempty"`
	Model    string   `json:"forecast_model,omitempty"`
}

// SupplierSuggestion — что заказать у одного поставщика.
type SupplierSuggestion struct {
	SupplierID   uuid.UUID `json:"supplier_id"`
	SupplierName string    `json:"supplier_name"`
	Contact      string    `json:"contact,omitempty"`
	// OrderBy — время отсечки: «до 16:00».
	OrderBy clock.TimeOfDay `json:"order_by"`
	// DeliveryAt — когда приедет, если заказать сегодня.
	Lines []SuggestionLine `json:"lines"`
}

// SuggestionLine — строка рекомендации.
type SuggestionLine struct {
	ItemID       uuid.UUID  `json:"item_id"`
	Name         string     `json:"name"`
	BaseUnit     string     `json:"base_unit"`
	OnHand       qty.Qty    `json:"on_hand"`
	Qty          qty.Qty    `json:"qty"`
	PurchaseQty  *qty.Qty   `json:"purchase_qty,omitempty"`
	PurchaseUnit string     `json:"purchase_unit,omitempty"`
	StockoutDate *clock.Day `json:"stockout_date,omitempty"`
	Status       string     `json:"status"`
}

// Load собирает главный экран одним запросом плюс запрос рекомендаций.
func (s *Service) Load(ctx context.Context, t tenant.Tenant) (View, error) {
	view := View{
		Today:       t.Today(s.clock),
		Items:       []Row{},
		Suggestions: []SupplierSuggestion{},
	}

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		rows, err := q.Dashboard(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("dashboard: таблица позиций: %w", err)
		}

		for _, r := range rows {
			row := Row{
				ItemID:         r.ItemID,
				Name:           r.ItemName,
				BaseUnit:       r.BaseUnit,
				OnHand:         postgres.Qty(r.OnHand),
				OnOrder:        postgres.Qty(r.OnOrder),
				Status:         string(r.Status),
				RecommendedQty: postgres.Qty(r.RecommendedQty),
				PurchaseUnit:   r.PurchaseUnit,
			}
			if r.CategoryName != nil {
				row.CategoryName = *r.CategoryName
			}
			if r.SupplierName != nil {
				row.SupplierName = *r.SupplierName
			}
			if r.SupplierID.Valid {
				id := r.SupplierID.UUID
				row.SupplierID = &id
			}
			if r.StockoutDate.Valid {
				d := postgres.Day(r.StockoutDate)
				row.StockoutDate = &d
			}
			if r.OrderBy.Valid {
				at := r.OrderBy.Time
				row.OrderBy = &at
			}
			if r.ForecastModel != nil {
				row.Model = string(*r.ForecastModel)
				// Точность показывается только когда модель есть: у M0
				// показывать нечего.
				if *r.ForecastModel != sqlc.ForecastModelM0 {
					acc := forecast.Metrics{WAPE: r.Wape}.Accuracy()
					row.Accuracy = &acc
				}
			}
			if row.RecommendedQty.IsPositive() {
				if packs, ok := inPurchaseUnits(row.RecommendedQty, postgres.Qty(r.UnitFactor), r.PurchaseUnit); ok {
					row.PurchaseQty = &packs
				}
			}

			view.Items = append(view.Items, row)
			countStatus(&view.Counters, row.Status)
		}
		view.Counters.Total = len(view.Items)

		suggestions, err := q.Suggestions(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("dashboard: рекомендации: %w", err)
		}
		view.Suggestions = groupSuggestions(suggestions)
		return nil
	})
	return view, err
}

func countStatus(c *Counters, status string) {
	switch replenishment.Status(status) {
	case replenishment.StatusOutOfStock:
		c.OutOfStock++
	case replenishment.StatusCritical:
		c.Critical++
	case replenishment.StatusOrderToday:
		c.OrderToday++
	case replenishment.StatusNoForecast:
		c.NoForecast++
	default:
		c.OK++
	}
}

// groupSuggestions складывает рекомендации в блоки по поставщикам:
// заказ оформляется одной кнопкой на поставщика (FR-18).
func groupSuggestions(rows []sqlc.SuggestionsRow) []SupplierSuggestion {
	out := make([]SupplierSuggestion, 0, 4)
	index := map[uuid.UUID]int{}

	for _, r := range rows {
		i, ok := index[r.SupplierID]
		if !ok {
			out = append(out, SupplierSuggestion{
				SupplierID:   r.SupplierID,
				SupplierName: r.SupplierName,
				Contact:      r.Contact,
				OrderBy:      postgres.ClockTime(r.OrderCutoff),
			})
			i = len(out) - 1
			index[r.SupplierID] = i
		}

		line := SuggestionLine{
			ItemID:       r.ItemID,
			Name:         r.ItemName,
			BaseUnit:     r.BaseUnit,
			OnHand:       postgres.Qty(r.OnHand),
			Qty:          postgres.Qty(r.RecommendedQty),
			PurchaseUnit: r.PurchaseUnit,
			Status:       string(r.Status),
		}
		if r.StockoutDate.Valid {
			d := postgres.Day(r.StockoutDate)
			line.StockoutDate = &d
		}
		if packs, ok := inPurchaseUnits(line.Qty, postgres.Qty(r.UnitFactor), r.PurchaseUnit); ok {
			line.PurchaseQty = &packs
		}
		out[i].Lines = append(out[i].Lines, line)
	}
	return out
}

// inPurchaseUnits переводит количество в единицы закупки: 24 л → 2 коробки.
func inPurchaseUnits(base, factor qty.Qty, unit string) (qty.Qty, bool) {
	if unit == "" || !factor.IsPositive() {
		return qty.Zero(), false
	}
	return qty.FromDecimal(base.Decimal().Div(factor.Decimal())), true
}
