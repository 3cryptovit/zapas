package orders

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Notifier сообщает о расхождении при приёмке (§5.5).
type Notifier interface {
	ReceiptMismatch(ctx context.Context, tx postgres.Tx, t tenant.Tenant, order Order) error
}

// Service — операции с заказами.
type Service struct {
	db    *postgres.DB
	clock clock.Clock
	// statuses пересчитывает статусы после отправки и приёмки: и то и другое
	// меняет позицию запаса.
	statuses *replenishment.Service
	// stock пишет приход при приёмке в той же транзакции, что и смена
	// статуса заказа (FR-20).
	stock    *stock.Service
	notifier Notifier
}

func NewService(db *postgres.DB, cl clock.Clock, statuses *replenishment.Service, stocks *stock.Service, notifier Notifier) *Service {
	return &Service{db: db, clock: cl, statuses: statuses, stock: stocks, notifier: notifier}
}

// CreateInput — черновик заказа: одна кнопка на поставщика в блоке
// «Заказать сегодня» (FR-18).
type CreateInput struct {
	SupplierID uuid.UUID
	Note       string
	Lines      []CreateLine
	CreatedBy  uuid.UUID
}

// CreateLine — строка черновика. Количество в базовой единице.
type CreateLine struct {
	ItemID uuid.UUID
	Qty    qty.Qty
}

// Create собирает черновик. Если строки не переданы, берутся рекомендации
// из read-модели: владелец нажал «Заказать» у поставщика.
func (s *Service) Create(ctx context.Context, t tenant.Tenant, in CreateInput) (Order, error) {
	var orderID uuid.UUID

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		if _, err := q.GetSupplier(ctx, sqlc.GetSupplierParams{TenantID: t.ID, ID: in.SupplierID}); err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("orders: поставщик: %w", err)
		}

		lines := in.Lines
		if len(lines) == 0 {
			suggested, err := s.suggestionsFor(ctx, q, t, in.SupplierID)
			if err != nil {
				return err
			}
			lines = suggested
		}
		if len(lines) == 0 {
			return ErrEmptyOrder
		}

		order, err := q.CreatePurchaseOrder(ctx, sqlc.CreatePurchaseOrderParams{
			ID:         uuid.Must(uuid.NewV7()),
			TenantID:   t.ID,
			SupplierID: in.SupplierID,
			Note:       in.Note,
			CreatedBy:  uuid.NullUUID{UUID: in.CreatedBy, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("orders: создание заказа: %w", err)
		}
		orderID = order.ID

		for _, l := range lines {
			if !l.Qty.IsPositive() {
				continue
			}
			// Условия закупки фиксируются в строке: заказ должен остаться
			// таким, каким его отправили, даже если поставщик поменяет упаковку.
			terms, err := q.GetSupplierItem(ctx, sqlc.GetSupplierItemParams{
				TenantID: t.ID, SupplierID: in.SupplierID, ItemID: l.ItemID,
			})
			purchaseUnit := ""
			unitFactor := qty.FromInt(1)
			var price *qty.Qty
			if err == nil {
				purchaseUnit = terms.PurchaseUnit
				unitFactor = postgres.Qty(terms.UnitFactor)
				if p, ok := postgres.NullQty(terms.Price); ok {
					price = &p
				}
			} else if !postgres.IsNoRows(err) {
				return fmt.Errorf("orders: условия закупки: %w", err)
			}

			if err := q.UpsertPurchaseOrderLine(ctx, sqlc.UpsertPurchaseOrderLineParams{
				TenantID:     t.ID,
				OrderID:      order.ID,
				ItemID:       l.ItemID,
				QtyOrdered:   postgres.DecimalOf(l.Qty),
				PurchaseUnit: purchaseUnit,
				UnitFactor:   postgres.DecimalOf(unitFactor),
				Price:        postgres.NullDecimalOf(price),
			}); err != nil {
				return fmt.Errorf("orders: строка заказа: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return Order{}, err
	}
	return s.Get(ctx, t, orderID)
}

// suggestionsFor собирает рекомендации по поставщику из read-модели.
func (s *Service) suggestionsFor(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, supplierID uuid.UUID) ([]CreateLine, error) {
	rows, err := q.Suggestions(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("orders: рекомендации: %w", err)
	}
	out := make([]CreateLine, 0, len(rows))
	for _, r := range rows {
		if r.SupplierID != supplierID {
			continue
		}
		out = append(out, CreateLine{ItemID: r.ItemID, Qty: postgres.Qty(r.RecommendedQty)})
	}
	return out, nil
}

// UpdateLines правит строки черновика: владелец может поправить количества (FR-18).
func (s *Service) UpdateLines(ctx context.Context, t tenant.Tenant, orderID uuid.UUID, lines []CreateLine) (Order, error) {
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		order, err := q.GetPurchaseOrder(ctx, sqlc.GetPurchaseOrderParams{TenantID: t.ID, ID: orderID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("orders: чтение заказа: %w", err)
		}
		if order.Status != sqlc.PurchaseOrderStatusDraft {
			return ErrWrongStatus
		}

		for _, l := range lines {
			if !l.Qty.IsPositive() {
				// Нулевое количество означает «убрать строку из заказа».
				if err := q.DeletePurchaseOrderLine(ctx, sqlc.DeletePurchaseOrderLineParams{
					TenantID: t.ID, OrderID: orderID, ItemID: l.ItemID,
				}); err != nil {
					return fmt.Errorf("orders: удаление строки: %w", err)
				}
				continue
			}

			terms, err := q.GetSupplierItem(ctx, sqlc.GetSupplierItemParams{
				TenantID: t.ID, SupplierID: order.SupplierID, ItemID: l.ItemID,
			})
			purchaseUnit := ""
			unitFactor := qty.FromInt(1)
			if err == nil {
				purchaseUnit = terms.PurchaseUnit
				unitFactor = postgres.Qty(terms.UnitFactor)
			} else if !postgres.IsNoRows(err) {
				return fmt.Errorf("orders: условия закупки: %w", err)
			}

			if err := q.UpsertPurchaseOrderLine(ctx, sqlc.UpsertPurchaseOrderLineParams{
				TenantID:     t.ID,
				OrderID:      orderID,
				ItemID:       l.ItemID,
				QtyOrdered:   postgres.DecimalOf(l.Qty),
				PurchaseUnit: purchaseUnit,
				UnitFactor:   postgres.DecimalOf(unitFactor),
			}); err != nil {
				return fmt.Errorf("orders: строка заказа: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return Order{}, err
	}
	return s.Get(ctx, t, orderID)
}

// Send помечает заказ отправленным: фиксирует время и ожидаемую дату d1,
// с этого момента количество учитывается «в пути» (FR-19).
func (s *Service) Send(ctx context.Context, t tenant.Tenant, orderID uuid.UUID) (Order, error) {
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		order, err := q.GetPurchaseOrderForUpdate(ctx, sqlc.GetPurchaseOrderForUpdateParams{
			TenantID: t.ID, ID: orderID,
		})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("orders: блокировка заказа: %w", err)
		}
		switch order.Status {
		case sqlc.PurchaseOrderStatusSent:
			return ErrAlreadySent
		case sqlc.PurchaseOrderStatusDraft:
		default:
			return ErrWrongStatus
		}

		lines, err := q.ListPurchaseOrderLines(ctx, sqlc.ListPurchaseOrderLinesParams{
			TenantID: t.ID, OrderID: orderID,
		})
		if err != nil {
			return fmt.Errorf("orders: строки заказа: %w", err)
		}
		if len(lines) == 0 {
			return ErrEmptyOrder
		}

		supplier, err := q.GetSupplier(ctx, sqlc.GetSupplierParams{TenantID: t.ID, ID: order.SupplierID})
		if err != nil {
			return fmt.Errorf("orders: поставщик: %w", err)
		}

		// Ожидаемая дата — это d1: поставка при заказе сейчас (§5.1).
		window := replenishment.ComputeWindow(t.Now(s.clock), t.Loc(), replenishment.Supplier{
			LeadTimeDays:     int(supplier.LeadTimeDays),
			DeliveryWeekdays: weekdays(supplier.DeliveryWeekdays),
			Cutoff:           postgres.ClockTime(supplier.OrderCutoff),
		})

		if err := q.SendPurchaseOrder(ctx, sqlc.SendPurchaseOrderParams{
			TenantID:   t.ID,
			ID:         orderID,
			SentAt:     postgres.Time(t.Now(s.clock)),
			ExpectedAt: postgres.Date(window.D1),
		}); err != nil {
			return fmt.Errorf("orders: отправка заказа: %w", err)
		}

		// Позиции стали «в пути» — статусы меняются, повторных напоминаний
		// по ним быть не должно (§2).
		return s.recalculate(ctx, tx, t, lines)
	})
	if err != nil {
		return Order{}, err
	}
	return s.Get(ctx, t, orderID)
}

// Cancel отменяет заказ.
func (s *Service) Cancel(ctx context.Context, t tenant.Tenant, orderID uuid.UUID) (Order, error) {
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		order, err := q.GetPurchaseOrderForUpdate(ctx, sqlc.GetPurchaseOrderForUpdateParams{
			TenantID: t.ID, ID: orderID,
		})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("orders: блокировка заказа: %w", err)
		}
		if order.Status == sqlc.PurchaseOrderStatusReceived {
			return ErrAlreadyReceived
		}
		if order.Status == sqlc.PurchaseOrderStatusCancelled {
			return nil // отмена отменённого — не ошибка
		}

		if err := q.CancelPurchaseOrder(ctx, sqlc.CancelPurchaseOrderParams{
			TenantID: t.ID, ID: orderID, CancelledAt: postgres.Time(t.Now(s.clock)),
		}); err != nil {
			return fmt.Errorf("orders: отмена заказа: %w", err)
		}

		lines, err := q.ListPurchaseOrderLines(ctx, sqlc.ListPurchaseOrderLinesParams{
			TenantID: t.ID, OrderID: orderID,
		})
		if err != nil {
			return fmt.Errorf("orders: строки заказа: %w", err)
		}
		// Количество перестало быть «в пути»: статусы надо пересчитать.
		return s.recalculate(ctx, tx, t, lines)
	})
	if err != nil {
		return Order{}, err
	}
	return s.Get(ctx, t, orderID)
}

func weekdays(days []int16) []int {
	out := make([]int, 0, len(days))
	for _, d := range days {
		out = append(out, int(d))
	}
	return out
}

func (s *Service) recalculate(ctx context.Context, tx postgres.Tx, t tenant.Tenant, lines []sqlc.ListPurchaseOrderLinesRow) error {
	if s.statuses == nil {
		return nil
	}
	for _, l := range lines {
		if _, err := s.statuses.Recalculate(ctx, tx, t, l.ItemID); err != nil {
			return fmt.Errorf("orders: пересчёт статуса: %w", err)
		}
	}
	return nil
}

// OrderText собирает текст заявки поставщику (FR-19).
//
// Формат простой и рассчитан на копирование в мессенджер: поставщик читает
// его глазами, а не парсит.
func OrderText(tenantName string, o Order, expected clock.Day) string {
	var b strings.Builder

	b.WriteString("Заказ от «")
	b.WriteString(tenantName)
	b.WriteString("»\n")
	if !expected.IsZero() {
		b.WriteString("Поставка: ")
		b.WriteString(formatDay(expected))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	for _, l := range o.Lines {
		b.WriteString("• ")
		b.WriteString(l.ItemName)
		b.WriteString(" — ")
		// Заявку читает человек, поэтому числа без лишних нулей: «2 кор. (24 л)».
		if l.PurchaseUnit != "" && l.UnitFactor.IsPositive() {
			packs := l.InPurchaseUnits(l.QtyOrdered)
			b.WriteString(packs.Human())
			b.WriteString(" ")
			b.WriteString(l.PurchaseUnit)
			b.WriteString(" (")
			b.WriteString(l.QtyOrdered.Human())
			b.WriteString(" ")
			b.WriteString(unitLabel(l.BaseUnit))
			b.WriteString(")")
		} else {
			b.WriteString(l.QtyOrdered.Human())
			b.WriteString(" ")
			b.WriteString(unitLabel(l.BaseUnit))
		}
		b.WriteString("\n")
	}

	if o.Note != "" {
		b.WriteString("\n")
		b.WriteString(o.Note)
		b.WriteString("\n")
	}
	return b.String()
}

var monthsGenitive = [...]string{
	"", "января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

func formatDay(d clock.Day) string {
	return fmt.Sprintf("%d %s", d.Date, monthsGenitive[int(d.Month)])
}

func unitLabel(unit string) string {
	switch unit {
	case "kg":
		return "кг"
	case "l":
		return "л"
	case "pcs":
		return "шт"
	default:
		return unit
	}
}
