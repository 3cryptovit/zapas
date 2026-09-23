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
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Alerter получает уведомление о том, что позиция стала красной.
//
// Интерфейс, а не прямой вызов notify: пересчёт статуса идёт внутри
// транзакции движения, и подменить отправку в тестах нужно без поднятия
// всей очереди.
type Alerter interface {
	// ItemWentRed вызывается в той же транзакции, что и смена статуса
	// (ADR-004): уведомление не может потеряться и не может уйти дважды.
	ItemWentRed(ctx context.Context, tx postgres.Tx, t tenant.Tenant, ev RedAlert) error
}

// RedAlert — данные срочного алерта (§5.5).
type RedAlert struct {
	ItemID         uuid.UUID
	ItemName       string
	BaseUnit       string
	Status         Status
	PreviousStatus Status
	OnHand         qty.Qty
	StockoutDate   clock.Day
	SupplierName   string
	NextDelivery   clock.Day
}

// Service считает статусы позиций и держит read-модель item_status в актуальном виде.
type Service struct {
	db      *postgres.DB
	clock   clock.Clock
	alerter Alerter
}

func NewService(db *postgres.DB, cl clock.Clock, alerter Alerter) *Service {
	return &Service{db: db, clock: cl, alerter: alerter}
}

// Recalculate пересчитывает статус одной позиции внутри уже открытой
// транзакции. Реализует stock.StatusRecalculator.
//
// Расчёт дешёвый — четыре запроса и 14 чисел прогноза, — поэтому идёт
// синхронно сразу после движения (§5.3).
func (s *Service) Recalculate(ctx context.Context, tx postgres.Tx, t tenant.Tenant, itemID uuid.UUID) (stock.StatusSnapshot, error) {
	q := sqlc.New(tx)

	row, err := q.GetItemForStatus(ctx, sqlc.GetItemForStatusParams{TenantID: t.ID, ID: itemID})
	if err != nil {
		if postgres.IsNoRows(err) {
			return stock.StatusSnapshot{}, stock.ErrItemNotFound
		}
		return stock.StatusSnapshot{}, fmt.Errorf("replenishment: параметры позиции: %w", err)
	}

	// Предыдущий статус нужен, чтобы отличить ухудшение от повторного расчёта.
	previous := StatusOK
	if prev, err := q.GetItemStatus(ctx, sqlc.GetItemStatusParams{TenantID: t.ID, ItemID: itemID}); err == nil {
		previous = Status(prev.Status)
	} else if !postgres.IsNoRows(err) {
		return stock.StatusSnapshot{}, fmt.Errorf("replenishment: прежний статус: %w", err)
	}

	in, err := s.buildInput(ctx, q, t, row)
	if err != nil {
		return stock.StatusSnapshot{}, err
	}

	decision := Decide(in)

	if err := s.persist(ctx, q, t, row, decision); err != nil {
		return stock.StatusSnapshot{}, err
	}

	// Срочный алерт уходит только при ухудшении: иначе каждое списание
	// красной позиции слало бы сообщение (§5.5).
	if s.alerter != nil && decision.Status.IsRed() && decision.Status.WorseThan(previous) {
		alert := RedAlert{
			ItemID:         itemID,
			ItemName:       row.ItemName,
			BaseUnit:       row.BaseUnit,
			Status:         decision.Status,
			PreviousStatus: previous,
			OnHand:         postgres.Qty(row.OnHand),
			StockoutDate:   decision.StockoutDate,
			NextDelivery:   in.Window.D1,
		}
		if row.SupplierName != nil {
			alert.SupplierName = *row.SupplierName
		}
		if err := s.alerter.ItemWentRed(ctx, tx, t, alert); err != nil {
			return stock.StatusSnapshot{}, fmt.Errorf("replenishment: срочный алерт: %w", err)
		}
	}

	return snapshot(decision, t), nil
}

// buildInput собирает всё, что нужно формулам: прогноз, поставки в пути,
// израсходованное сегодня и условия поставщика.
func (s *Service) buildInput(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, row sqlc.GetItemForStatusRow) (Input, error) {
	today := t.Today(s.clock)
	now := t.Now(s.clock)

	fcRows, err := q.ListForecastForItem(ctx, sqlc.ListForecastForItemParams{
		TenantID: t.ID, ItemID: row.ItemID, Day: postgres.Date(today),
	})
	if err != nil {
		return Input{}, fmt.Errorf("replenishment: прогноз: %w", err)
	}

	result := forecast.Result{
		Model: forecast.ModelM0,
		Metrics: forecast.Metrics{
			Model: forecast.ModelM0,
			Sigma: row.Sigma,
			WAPE:  row.Wape,
			Bias:  row.Bias,
		},
	}
	if row.MetricsModel != nil {
		result.Model = forecast.Model(*row.MetricsModel)
		result.Metrics.Model = result.Model
	}
	for _, f := range fcRows {
		result.Points = append(result.Points, forecast.Point{
			Day: postgres.Day(f.Day),
			Qty: postgres.Qty(f.Qty),
		})
	}
	// Записей прогноза нет — значит модель M0, чем бы ни были метрики.
	if len(result.Points) == 0 {
		result.Model = forecast.ModelM0
		result.Metrics.Model = forecast.ModelM0
	}

	incRows, err := q.ListIncomingForItem(ctx, sqlc.ListIncomingForItemParams{
		TenantID: t.ID, ItemID: row.ItemID,
	})
	if err != nil {
		return Input{}, fmt.Errorf("replenishment: поставки в пути: %w", err)
	}
	incoming := make([]Delivery, 0, len(incRows))
	for _, inc := range incRows {
		if !inc.ExpectedAt.Valid {
			continue
		}
		incoming = append(incoming, Delivery{
			Day: postgres.Day(inc.ExpectedAt),
			Qty: postgres.Qty(inc.Qty),
		})
	}

	consumed, err := q.SumConsumptionForDay(ctx, sqlc.SumConsumptionForDayParams{
		TenantID: t.ID, ItemID: row.ItemID,
		Tz: t.Loc().String(), Day: postgres.Date(today),
	})
	if err != nil {
		return Input{}, fmt.Errorf("replenishment: расход за сегодня: %w", err)
	}

	in := Input{
		Today:         today,
		OnHand:        postgres.Qty(row.OnHand),
		Incoming:      incoming,
		Forecast:      result,
		ConsumedToday: postgres.Qty(consumed),
		ServiceLevel:  int(row.ServiceLevel),
		ManualMin:     postgres.Qty(row.ManualMinQty),
		MinOrder:      postgres.Qty(row.MinOrderQty),
		Pack:          postgres.Qty(row.PackMultiple),
	}

	if row.SupplierID.Valid {
		lead := 0
		if row.LeadTimeDays != nil {
			lead = int(*row.LeadTimeDays)
		}
		in.Window = ComputeWindow(now, t.Loc(), Supplier{
			LeadTimeDays:     lead,
			DeliveryWeekdays: weekdaysFromDB(row.DeliveryWeekdays),
			Cutoff:           postgres.ClockTime(row.OrderCutoff),
		})
	} else {
		// Без поставщика окна поставки не существует: система не может
		// сказать, когда заказывать. Статус считается, но «заказать сегодня»
		// не предлагается.
		in.Window = Window{
			OrderDay: today,
			D1:       today.AddDays(1),
			D2:       today.AddDays(1),
			OrderBy:  now,
		}
	}
	return in, nil
}

// persist кладёт решение в read-модель дашборда.
func (s *Service) persist(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, row sqlc.GetItemForStatusRow, d Decision) error {
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
	// Дедлайн отсечки имеет смысл только когда есть что заказывать.
	if row.SupplierID.Valid && d.Status == StatusOrderToday {
		params.OrderBy = postgres.Time(d.Explanation.OrderBy)
	}

	if err := q.UpsertItemStatus(ctx, params); err != nil {
		return fmt.Errorf("replenishment: запись статуса: %w", err)
	}
	return nil
}

func snapshot(d Decision, t tenant.Tenant) stock.StatusSnapshot {
	out := stock.StatusSnapshot{
		Code:           string(d.Status),
		RecommendedQty: d.RecommendedQty,
	}
	if !d.StockoutDate.IsZero() {
		day := d.StockoutDate.String()
		out.StockoutDate = &day
	}
	if d.Status == StatusOrderToday && !d.Explanation.OrderBy.IsZero() {
		deadline := d.Explanation.OrderBy.In(t.Loc()).Format(time.RFC3339)
		out.OrderBy = &deadline
	}
	return out
}

func weekdaysFromDB(days []int16) []int {
	out := make([]int, 0, len(days))
	for _, d := range days {
		out = append(out, int(d))
	}
	return out
}
