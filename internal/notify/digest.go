package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/tenant"
)

// SendDigest собирает и ставит в очередь ежедневную сводку (§5.5).
//
// Пустую сводку не шлём: сообщение «всё в порядке» каждое утро быстро
// перестают читать, и тогда не заметят важное.
func (s *Service) SendDigest(ctx context.Context, t tenant.Tenant) (bool, error) {
	today := t.Today(s.clock)

	var sent bool
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		data, err := s.buildDigest(ctx, tx, t, today)
		if err != nil {
			return err
		}
		if data.Empty() {
			return nil
		}
		sent = true
		return s.Emit(ctx, tx, t, TypeDailyDigest, DigestKey(today), RenderDigest(data))
	})
	return sent, err
}

func (s *Service) buildDigest(ctx context.Context, tx postgres.Tx, t tenant.Tenant, today clock.Day) (DigestData, error) {
	q := sqlc.New(tx)

	data := DigestData{
		TenantName:   t.Name,
		Day:          today,
		DashboardURL: s.dashboardLink(),
	}

	rows, err := q.Suggestions(ctx, t.ID)
	if err != nil {
		return DigestData{}, fmt.Errorf("notify: рекомендации: %w", err)
	}

	index := map[uuid.UUID]int{}
	for _, r := range rows {
		// Красные позиции идут отдельным блоком «Критично».
		if replenishment.Status(r.Status) == replenishment.StatusCritical ||
			replenishment.Status(r.Status) == replenishment.StatusOutOfStock {
			continue
		}

		i, ok := index[r.SupplierID]
		if !ok {
			data.Suppliers = append(data.Suppliers, DigestSupplier{
				Name:       r.SupplierName,
				Cutoff:     postgres.ClockTime(r.OrderCutoff),
				DeliveryAt: deliveryDay(r.OrderBy, t),
			})
			i = len(data.Suppliers) - 1
			index[r.SupplierID] = i
		}

		line := DigestLine{
			Name:         r.ItemName,
			Qty:          postgres.Qty(r.RecommendedQty),
			Unit:         r.BaseUnit,
			PurchaseUnit: r.PurchaseUnit,
			StockoutDate: postgres.Day(r.StockoutDate),
		}
		if factor := postgres.Qty(r.UnitFactor); factor.IsPositive() && r.PurchaseUnit != "" {
			packs := qty.FromDecimal(line.Qty.Decimal().Div(factor.Decimal()))
			line.PurchaseQty = &packs
		}
		data.Suppliers[i].Lines = append(data.Suppliers[i].Lines, line)
	}

	// Блок «Критично»: позиции, которые не доживут до ближайшей поставки.
	statuses, err := q.ListItemStatuses(ctx, t.ID)
	if err != nil {
		return DigestData{}, fmt.Errorf("notify: статусы: %w", err)
	}
	names, err := s.itemNames(ctx, q, t)
	if err != nil {
		return DigestData{}, err
	}

	for _, st := range statuses {
		status := replenishment.Status(st.Status)
		if !status.IsRed() {
			continue
		}
		data.Critical = append(data.Critical, DigestCritical{
			Name:         names[st.ItemID],
			StockoutDate: postgres.Day(st.StockoutDate),
			NextDelivery: explanationD1(st.Explanation),
		})
	}
	return data, nil
}

func (s *Service) itemNames(ctx context.Context, q *sqlc.Queries, t tenant.Tenant) (map[uuid.UUID]string, error) {
	rows, err := q.ListItems(ctx, sqlc.ListItemsParams{TenantID: t.ID})
	if err != nil {
		return nil, fmt.Errorf("notify: названия позиций: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// deliveryDay достаёт дату ближайшей поставки из дедлайна отсечки.
// Точная дата d1 лежит в объяснении статуса; здесь достаточно дня заказа.
func deliveryDay(orderBy pgtype.Timestamptz, t tenant.Tenant) clock.Day {
	if !orderBy.Valid {
		return clock.Day{}
	}
	return clock.DayIn(orderBy.Time, t.Loc())
}

// explanationD1 вытаскивает d1 из jsonb объяснения статуса.
func explanationD1(raw []byte) clock.Day {
	if len(raw) == 0 {
		return clock.Day{}
	}
	var e struct {
		D1 clock.Day `json:"d1"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return clock.Day{}
	}
	return e.D1
}

// --- напоминание до отсечки и опоздания ---

// CutoffLeadTime — за сколько до отсечки напоминаем (§5.5).
const CutoffLeadTime = 2 * time.Hour

// SendCutoffReminders шлёт напоминания по поставщикам, у которых отсечка
// через два часа, а заказ так и не отправлен.
func (s *Service) SendCutoffReminders(ctx context.Context, t tenant.Tenant) (int, error) {
	now := t.Now(s.clock)
	today := t.Today(s.clock)

	var sent int
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		rows, err := q.Suggestions(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("notify: рекомендации: %w", err)
		}

		type pending struct {
			name   string
			cutoff clock.TimeOfDay
			count  int
		}
		bySupplier := map[uuid.UUID]*pending{}

		for _, r := range rows {
			p, ok := bySupplier[r.SupplierID]
			if !ok {
				p = &pending{name: r.SupplierName, cutoff: postgres.ClockTime(r.OrderCutoff)}
				bySupplier[r.SupplierID] = p
			}
			p.count++
		}

		for supplierID, p := range bySupplier {
			deadline := p.cutoff.On(today, t.Loc())
			// Окно напоминания: два часа до отсечки и ни минутой раньше.
			if now.Before(deadline.Add(-CutoffLeadTime)) || !now.Before(deadline) {
				continue
			}

			// Заказ уже отправлен сегодня — напоминать не о чем.
			hasOrder, err := q.HasOpenOrderForSupplier(ctx, sqlc.HasOpenOrderForSupplierParams{
				TenantID:   t.ID,
				SupplierID: supplierID,
				SentAt:     postgres.Time(today.Time(t.Loc())),
			})
			if err != nil {
				return fmt.Errorf("notify: проверка заказов: %w", err)
			}
			if hasOrder {
				continue
			}

			payload := RenderCutoff(CutoffData{
				SupplierName: p.name,
				Cutoff:       p.cutoff,
				ItemCount:    p.count,
				Link:         s.dashboardLink(),
			})
			if err := s.Emit(ctx, tx, t, TypeCutoffReminder, CutoffKey(supplierID, today), payload); err != nil {
				return err
			}
			sent++
		}
		return nil
	})
	return sent, err
}

// SendLateAlerts сообщает о непринятых поставках (FR-21).
func (s *Service) SendLateAlerts(ctx context.Context, t tenant.Tenant) (int, error) {
	today := t.Today(s.clock)

	var sent int
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		late, err := q.ListLateOrders(ctx, sqlc.ListLateOrdersParams{
			TenantID: t.ID, Today: postgres.Date(today),
		})
		if err != nil {
			return fmt.Errorf("notify: опоздавшие заказы: %w", err)
		}

		for _, o := range late {
			lines, err := q.ListPurchaseOrderLines(ctx, sqlc.ListPurchaseOrderLinesParams{
				TenantID: t.ID, OrderID: o.ID,
			})
			if err != nil {
				return fmt.Errorf("notify: строки заказа: %w", err)
			}

			payload := RenderLate(LateData{
				SupplierName: o.SupplierName,
				ExpectedAt:   postgres.Day(o.ExpectedAt),
				ItemCount:    len(lines),
				Link:         s.orderLink(o.ID),
			})
			if err := s.Emit(ctx, tx, t, TypeOrderLate, LateKey(o.ID), payload); err != nil {
				return err
			}

			// Отметка, чтобы алерт ушёл один раз на заказ.
			if err := q.MarkOrderLateNotified(ctx, sqlc.MarkOrderLateNotifiedParams{
				TenantID: t.ID, ID: o.ID, LateNotifiedAt: postgres.Time(s.clock.Now()),
			}); err != nil {
				return fmt.Errorf("notify: отметка об опоздании: %w", err)
			}
			sent++
		}
		return nil
	})
	return sent, err
}
