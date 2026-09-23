package orders

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// ReceiveLine — фактически принятое количество по строке.
type ReceiveLine struct {
	ItemID uuid.UUID
	Qty    qty.Qty
}

// Receive проводит приёмку (FR-20).
//
// Движения receipt создаются одной транзакцией вместе со сменой статуса
// заказа: наполовину принятого заказа в системе быть не должно. Повторное
// нажатие не создаёт второй приход — заказ заблокирован и уже не в статусе
// «отправлен».
//
// Фактические количества по умолчанию равны заказанным; расхождения
// сохраняются в строках и, если они больше 5%, попадают в ленту (§5.5).
func (s *Service) Receive(ctx context.Context, t tenant.Tenant, orderID uuid.UUID, actual []ReceiveLine, by uuid.UUID, idempotencyKey string) (Order, error) {
	byItem := make(map[uuid.UUID]qty.Qty, len(actual))
	for _, l := range actual {
		byItem[l.ItemID] = l.Qty
	}

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
		case sqlc.PurchaseOrderStatusReceived:
			return ErrAlreadyReceived
		case sqlc.PurchaseOrderStatusSent:
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

		// Время тенанта, а не системное: в песочнице часы сдвинуты, и
		// приход с системным временем оказывается «в прошлом» на всю
		// промотку — склад такой приход не принимает (FR-9).
		now := t.Now(s.clock)

		// Статус заказа меняется до создания приходов: пересчёт статуса
		// позиции внутри RecordInTx считает «в пути» по отправленным заказам,
		// и этот заказ уже не должен в них попадать.
		if err := q.ReceivePurchaseOrder(ctx, sqlc.ReceivePurchaseOrderParams{
			TenantID: t.ID, ID: orderID, ReceivedAt: postgres.Time(now),
		}); err != nil {
			return fmt.Errorf("orders: проведение приёмки: %w", err)
		}

		for _, line := range lines {
			ordered := postgres.Qty(line.QtyOrdered)

			// По умолчанию принято столько же, сколько заказано.
			received := ordered
			if v, ok := byItem[line.ItemID]; ok {
				received = v
			}
			if received.IsNegative() {
				return fmt.Errorf("orders: отрицательное количество по позиции %s", line.ItemName)
			}

			if err := q.SetPurchaseOrderLineReceived(ctx, sqlc.SetPurchaseOrderLineReceivedParams{
				TenantID:    t.ID,
				OrderID:     orderID,
				ItemID:      line.ItemID,
				QtyReceived: postgres.NullDecimalOf(&received),
			}); err != nil {
				return fmt.Errorf("orders: фактическое количество: %w", err)
			}

			if !received.IsPositive() {
				// Позицию не привезли вовсе — прихода нет, расхождение
				// останется в строке.
				continue
			}

			// Ключ идемпотентности детерминированный: повтор запроса
			// с тем же ключом не создаст второй приход даже в обход статуса.
			key := idempotencyKey + ":" + orderID.String() + ":" + line.ItemID.String()

			if _, err := s.stock.RecordInTx(ctx, tx, t, stock.RecordRequest{
				ItemID:         line.ItemID,
				Type:           stock.TypeReceipt,
				Qty:            received,
				OccurredAt:     now,
				Comment:        "Приёмка заказа",
				OrderID:        &orderID,
				IdempotencyKey: key,
				CreatedBy:      &by,
			}); err != nil {
				return fmt.Errorf("orders: приход по позиции %s: %w", line.ItemName, err)
			}
		}

		return nil
	})
	if err != nil {
		return Order{}, err
	}

	received, err := s.Get(ctx, t, orderID)
	if err != nil {
		return Order{}, err
	}

	// Расхождение больше 5% — в ленту (§5.5). Уведомление не критично
	// для самой приёмки, поэтому идёт отдельной транзакцией.
	if s.notifier != nil && received.HasSignificantMismatch() {
		notifyErr := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
			return s.notifier.ReceiptMismatch(ctx, tx, t, received)
		})
		if notifyErr != nil {
			return received, fmt.Errorf("orders: уведомление о расхождении: %w", notifyErr)
		}
	}
	return received, nil
}

// Get отдаёт заказ со строками и готовым текстом заявки.
func (s *Service) Get(ctx context.Context, t tenant.Tenant, orderID uuid.UUID) (Order, error) {
	var out Order

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		row, err := q.GetPurchaseOrder(ctx, sqlc.GetPurchaseOrderParams{TenantID: t.ID, ID: orderID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("orders: чтение заказа: %w", err)
		}
		out = orderFromRow(row)

		supplier, err := q.GetSupplier(ctx, sqlc.GetSupplierParams{TenantID: t.ID, ID: row.SupplierID})
		if err == nil {
			out.SupplierName = supplier.Name
		} else if !postgres.IsNoRows(err) {
			return fmt.Errorf("orders: поставщик: %w", err)
		}

		lines, err := q.ListPurchaseOrderLines(ctx, sqlc.ListPurchaseOrderLinesParams{
			TenantID: t.ID, OrderID: orderID,
		})
		if err != nil {
			return fmt.Errorf("orders: строки заказа: %w", err)
		}
		for _, l := range lines {
			line := Line{
				ItemID:       l.ItemID,
				ItemName:     l.ItemName,
				BaseUnit:     l.BaseUnit,
				QtyOrdered:   postgres.Qty(l.QtyOrdered),
				PurchaseUnit: l.PurchaseUnit,
				UnitFactor:   postgres.Qty(l.UnitFactor),
			}
			if got, ok := postgres.NullQty(l.QtyReceived); ok {
				line.QtyReceived = &got
			}
			if price, ok := postgres.NullQty(l.Price); ok {
				line.Price = &price
			}
			out.Lines = append(out.Lines, line)
		}

		out.Text = OrderText(t.Name, out, out.ExpectedAt)
		out.Late = isLate(out, t.Today(s.clock))
		return nil
	})
	return out, err
}

// List отдаёт список заказов, свежие сверху.
func (s *Service) List(ctx context.Context, t tenant.Tenant, status *Status, limit int) ([]Order, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	params := sqlc.ListPurchaseOrdersParams{TenantID: t.ID, Limit: int32(limit)}
	if status != nil {
		st := sqlc.PurchaseOrderStatus(*status)
		params.Status = &st
	}

	var out []Order
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListPurchaseOrders(ctx, params)
		if err != nil {
			return fmt.Errorf("orders: список заказов: %w", err)
		}
		today := t.Today(s.clock)
		for _, r := range rows {
			order := orderFromRow(sqlc.PurchaseOrder{
				ID:          r.ID,
				TenantID:    r.TenantID,
				SupplierID:  r.SupplierID,
				Status:      r.Status,
				ExpectedAt:  r.ExpectedAt,
				SentAt:      r.SentAt,
				ReceivedAt:  r.ReceivedAt,
				CancelledAt: r.CancelledAt,
				Note:        r.Note,
				CreatedBy:   r.CreatedBy,
				CreatedAt:   r.CreatedAt,
			})
			order.SupplierName = r.SupplierName
			order.Late = isLate(order, today)
			out = append(out, order)
		}
		return nil
	})
	return out, err
}

// isLate: к концу ожидаемого дня заказ не принят (FR-21).
func isLate(o Order, today clock.Day) bool {
	return o.Status == StatusSent && !o.ExpectedAt.IsZero() && o.ExpectedAt.Before(today)
}

func orderFromRow(row sqlc.PurchaseOrder) Order {
	o := Order{
		ID:          row.ID,
		SupplierID:  row.SupplierID,
		Status:      Status(row.Status),
		ExpectedAt:  postgres.Day(row.ExpectedAt),
		SentAt:      postgres.TimePtr(row.SentAt),
		ReceivedAt:  postgres.TimePtr(row.ReceivedAt),
		CancelledAt: postgres.TimePtr(row.CancelledAt),
		Note:        row.Note,
		CreatedAt:   postgres.TimeOrZero(row.CreatedAt),
	}
	o.StatusLabel = o.Status.Label()
	if row.CreatedBy.Valid {
		id := row.CreatedBy.UUID
		o.CreatedBy = &id
	}
	return o
}
