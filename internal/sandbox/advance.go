package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/orders"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Ошибки симулятора.
var (
	ErrNotSandbox   = errors.New("sandbox: тенант не демонстрационный")
	ErrHorizonLimit = fmt.Errorf("sandbox: не больше %d виртуальных дней вперёд", MaxVirtualDays)
	ErrBadDays      = errors.New("sandbox: промотать можно на 1 или 7 дней")
)

// Simulator двигает виртуальное время песочницы (§7.3).
type Simulator struct {
	svc    *Service
	orders *orders.Service
	stock  *stock.Service
	notify *notify.Service
}

func NewSimulator(svc *Service, ordersSvc *orders.Service, stocks *stock.Service, notifier *notify.Service) *Simulator {
	return &Simulator{svc: svc, orders: ordersSvc, stock: stocks, notify: notifier}
}

// AdvanceResult — что произошло за промотку.
type AdvanceResult struct {
	Days int `json:"days"`
	// Today — новая виртуальная дата.
	Today clock.Day `json:"today"`
	// Received — принято поставок.
	Received int `json:"received"`
	// Ordered — оформлено заказов автопилотом.
	Ordered int `json:"ordered"`
	// Notifications — сколько уведомлений появилось в ленте.
	Notifications int `json:"notifications"`
}

// Advance проматывает время на days суток (§7.3).
//
// Шаг одного дня:
//  1. сдвинуть виртуальные часы;
//  2. принять заказы, у которых наступила дата поставки;
//  3. сгенерировать расход дня;
//  4. пересчитать прогноз и статусы;
//  5. сформировать сводку и алерты дня.
func (s *Simulator) Advance(ctx context.Context, tn tenant.Tenant, days int) (AdvanceResult, error) {
	if !tn.IsSandbox {
		return AdvanceResult{}, ErrNotSandbox
	}
	if days != 1 && days != 7 {
		return AdvanceResult{}, ErrBadDays
	}

	// Не больше 60 виртуальных дней вперёд на одну песочницу (§7.5).
	elapsed := int(tn.ClockOffset.Hours() / 24)
	if elapsed+days > MaxVirtualDays {
		return AdvanceResult{}, ErrHorizonLimit
	}

	owner, err := s.owner(ctx, tn)
	if err != nil {
		return AdvanceResult{}, err
	}

	result := AdvanceResult{Days: days}

	for i := 0; i < days; i++ {
		// 1. Сдвигаем часы. Дальше tn уже живёт в новом дне.
		shifted, err := s.shiftClock(ctx, tn)
		if err != nil {
			return AdvanceResult{}, err
		}
		tn = shifted

		// 2. Принимаем приехавшее.
		received, err := s.receiveArrivals(ctx, tn, owner)
		if err != nil {
			return AdvanceResult{}, err
		}
		result.Received += received

		// 3. Генерируем расход дня: как будто поработали сотрудники.
		if err := s.consumeDay(ctx, tn, owner); err != nil {
			return AdvanceResult{}, err
		}

		// 4. Ночной пересчёт прогноза и статусов.
		if _, err := s.svc.pipeline.Run(ctx, tn); err != nil {
			return AdvanceResult{}, fmt.Errorf("sandbox: пересчёт: %w", err)
		}

		// 5. Автопилот: симулятор сам оформляет заказы по рекомендациям.
		// Без него позиции краснеют — это и есть наглядное сравнение
		// «с системой и без неё» (§7.3).
		if tn.Autopilot {
			ordered, err := s.autoOrder(ctx, tn, owner)
			if err != nil {
				return AdvanceResult{}, err
			}
			result.Ordered += ordered
		}

		// 6. Сводка и алерты дня — в ленту (внешние каналы демо отключены).
		sent, err := s.notify.SendDigest(ctx, tn)
		if err != nil {
			return AdvanceResult{}, fmt.Errorf("sandbox: сводка: %w", err)
		}
		if sent {
			result.Notifications++
		}
		late, err := s.notify.SendLateAlerts(ctx, tn)
		if err != nil {
			return AdvanceResult{}, fmt.Errorf("sandbox: алерты опоздания: %w", err)
		}
		result.Notifications += late
	}

	result.Today = tn.Today(s.svc.clock)
	return result, nil
}

// shiftClock двигает виртуальные часы тенанта на сутки вперёд.
func (s *Simulator) shiftClock(ctx context.Context, tn tenant.Tenant) (tenant.Tenant, error) {
	var offset time.Duration

	err := s.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		raw, err := sqlc.New(tx).AdvanceTenantClock(ctx, sqlc.AdvanceTenantClockParams{
			ID:      tn.ID,
			Column2: postgres.Interval(24 * time.Hour),
		})
		if err != nil {
			return fmt.Errorf("sandbox: сдвиг часов: %w", err)
		}
		offset = postgres.Duration(raw)
		return nil
	})
	if err != nil {
		return tenant.Tenant{}, err
	}

	tn.ClockOffset = offset
	return tn, nil
}

// receiveArrivals принимает заказы, у которых наступила дата поставки.
func (s *Simulator) receiveArrivals(ctx context.Context, tn tenant.Tenant, owner uuid.UUID) (int, error) {
	today := tn.Today(s.svc.clock)

	var due []uuid.UUID
	err := s.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		sent := sqlc.PurchaseOrderStatusSent
		rows, err := sqlc.New(tx).ListPurchaseOrders(ctx, sqlc.ListPurchaseOrdersParams{
			TenantID: tn.ID, Status: &sent, Limit: 200,
		})
		if err != nil {
			return fmt.Errorf("sandbox: открытые заказы: %w", err)
		}
		for _, o := range rows {
			if !o.ExpectedAt.Valid {
				continue
			}
			if postgres.Day(o.ExpectedAt).After(today) {
				continue
			}
			due = append(due, o.ID)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	rng := newRNG(tenantSeed(tn), uint64(today.Sub(clock.Day{})))

	received := 0
	for _, orderID := range due {
		var actual []orders.ReceiveLine

		// В 10% случаев привозят не ровно столько, сколько заказали (§7.3).
		if rng.Float64() < 0.10 {
			order, err := s.orders.Get(ctx, tn, orderID)
			if err != nil {
				return received, err
			}
			for _, l := range order.Lines {
				actual = append(actual, orders.ReceiveLine{
					ItemID: l.ItemID,
					Qty:    l.QtyOrdered.MulFloat(0.85 + rng.Float64()*0.1),
				})
			}
		}

		if _, err := s.orders.Receive(ctx, tn, orderID, actual, owner, uuid.NewString()); err != nil {
			return received, fmt.Errorf("sandbox: приёмка: %w", err)
		}
		received++
	}
	return received, nil
}

// consumeDay списывает дневной расход по модели генератора.
func (s *Simulator) consumeDay(ctx context.Context, tn tenant.Tenant, owner uuid.UUID) error {
	today := tn.Today(s.svc.clock)
	dayIndex := HistoryDays + int(tn.ClockOffset.Hours()/24)

	// Время события — текущее виртуальное «сейчас», а не фиксированный час:
	// сервис склада не принимает движения из будущего (FR-9), а виртуальные
	// часы могут стоять раньше условного конца смены.
	occurredAt := tn.Now(s.svc.clock)

	return s.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		warehouse, err := q.GetDefaultWarehouse(ctx, tn.ID)
		if err != nil {
			return fmt.Errorf("sandbox: склад: %w", err)
		}

		rows, err := q.ListItems(ctx, sqlc.ListItemsParams{TenantID: tn.ID})
		if err != nil {
			return fmt.Errorf("sandbox: позиции: %w", err)
		}
		specs := specsByName()

		for i, row := range rows {
			spec, ok := specs[row.Name]
			if !ok {
				// Позицию завёл сам посетитель — её расход не выдумываем.
				continue
			}

			rng := newRNG(tenantSeed(tn), uint64(i)*1000+uint64(dayIndex))
			wanted := qty.FromInt(1).MulFloat(demand(rng, spec, dayIndex, today))

			onHand, err := q.LockBalance(ctx, sqlc.LockBalanceParams{
				WarehouseID: warehouse.ID, ItemID: row.ID,
			})
			if err != nil {
				return fmt.Errorf("sandbox: остаток: %w", err)
			}
			balance := postgres.Qty(onHand)

			// Списываем только то, что есть: дефицит — часть демонстрации.
			used := wanted
			if used.GreaterThan(balance) {
				used = balance
			}
			if !used.IsPositive() {
				continue
			}

			if _, err := s.stock.RecordInTx(ctx, tx, tn, stock.RecordRequest{
				ItemID:         row.ID,
				Type:           stock.TypeUsage,
				Qty:            used,
				OccurredAt:     occurredAt,
				Comment:        "Расход за смену",
				IdempotencyKey: fmt.Sprintf("sim:%s:%s", today, row.ID),
				CreatedBy:      &owner,
			}); err != nil {
				return fmt.Errorf("sandbox: расход дня: %w", err)
			}
		}
		return nil
	})
}

// autoOrder оформляет и отправляет заказы по рекомендациям (§7.3).
func (s *Simulator) autoOrder(ctx context.Context, tn tenant.Tenant, owner uuid.UUID) (int, error) {
	var supplierIDs []uuid.UUID

	err := s.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).Suggestions(ctx, tn.ID)
		if err != nil {
			return fmt.Errorf("sandbox: рекомендации: %w", err)
		}
		seen := map[uuid.UUID]bool{}
		for _, r := range rows {
			if seen[r.SupplierID] {
				continue
			}
			seen[r.SupplierID] = true
			supplierIDs = append(supplierIDs, r.SupplierID)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	placed := 0
	for _, supplierID := range supplierIDs {
		order, err := s.orders.Create(ctx, tn, orders.CreateInput{
			SupplierID: supplierID,
			Note:       "Оформлено автопилотом демо",
			CreatedBy:  owner,
		})
		if err != nil {
			if errors.Is(err, orders.ErrEmptyOrder) {
				continue
			}
			return placed, fmt.Errorf("sandbox: автозаказ: %w", err)
		}
		if _, err := s.orders.Send(ctx, tn, order.ID); err != nil {
			return placed, fmt.Errorf("sandbox: отправка автозаказа: %w", err)
		}
		placed++
	}
	return placed, nil
}

// specsByName — каталог генератора по названию позиции.
func specsByName() map[string]itemSpec {
	out := make(map[string]itemSpec, len(catalog))
	for _, spec := range catalog {
		out[spec.Name] = spec
	}
	return out
}

// tenantSeed — зерно генератора. Лежит в самом тенанте, поэтому промотка
// воспроизводима: одинаковый seed даёт одинаковые данные (§7.2).
func tenantSeed(tn tenant.Tenant) int64 { return tn.Seed }

// owner находит владельца демо: он автор всех движений симулятора.
func (s *Simulator) owner(ctx context.Context, tn tenant.Tenant) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		users, err := sqlc.New(tx).ListRecipients(ctx, tn.ID)
		if err != nil {
			return fmt.Errorf("sandbox: владелец демо: %w", err)
		}
		if len(users) == 0 {
			return fmt.Errorf("sandbox: у демо нет владельца")
		}
		id = users[0].ID
		return nil
	})
	return id, err
}
