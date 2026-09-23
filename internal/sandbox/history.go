package sandbox

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// generateHistory пишет 90 дней движений: расход, списания и пополнения
// с «человеческими» ошибками (§7.2).
//
// Движения идут прямо в журнал, минуя сервис склада: тот не принимает
// записи дальше семи дней назад (FR-9), и это правильно — история
// генерируется, а не вносится.
func (s *Service) generateHistory(
	ctx context.Context, q *sqlc.Queries, tn tenant.Tenant,
	warehouseID, ownerID uuid.UUID, items []createdItem, seed int64,
) error {
	today := tn.Today(s.clock)
	start := today.AddDays(-HistoryDays)

	// Движений получается несколько тысяч. Поштучные INSERT-ы не укладываются
	// в бюджет создания демо (3 секунды, §7.1), поэтому копим их в памяти
	// и пишем одним COPY.
	batch := make([]movement, 0, len(items)*HistoryDays*2)
	balances := make(map[uuid.UUID]qty.Qty, len(items))

	for i, item := range items {
		// Свой поток на позицию: добавление позиции в каталог не должно
		// менять историю остальных.
		rng := newRNG(seed, uint64(i)+1)

		produced, balance := s.generateItemHistory(tn, ownerID, item, rng, start)
		batch = append(batch, produced...)
		balances[item.ID] = balance
	}

	if err := s.writeMovements(ctx, q, tn, warehouseID, ownerID, batch); err != nil {
		return err
	}

	// Остатки выставляются после: они должны совпасть с суммой журнала.
	for itemID, balance := range balances {
		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID: tn.ID, WarehouseID: warehouseID, ItemID: itemID,
		}); err != nil {
			return fmt.Errorf("sandbox: остаток: %w", err)
		}
		if _, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
			WarehouseID: warehouseID,
			ItemID:      itemID,
			Delta:       balance.Decimal(),
			Now:         postgres.Time(s.clock.Now()),
		}); err != nil {
			return fmt.Errorf("sandbox: применение остатка: %w", err)
		}
	}
	return nil
}

// writeMovements пишет пачку движений одним запросом (§7.1).
//
// COPY здесь применить нельзя: PostgreSQL не поддерживает COPY FROM для
// таблиц с row-level security. Разворот массивов даёт тот же единственный
// заход в базу и не требует отключать RLS.
func (s *Service) writeMovements(
	ctx context.Context, q *sqlc.Queries, tn tenant.Tenant,
	warehouseID, ownerID uuid.UUID, batch []movement,
) error {
	if len(batch) == 0 {
		return nil
	}

	params := sqlc.BulkInsertMovementsParams{
		TenantID:    tn.ID,
		WarehouseID: warehouseID,
		CreatedBy:   ownerID,
		Ids:         make([]uuid.UUID, 0, len(batch)),
		ItemIds:     make([]uuid.UUID, 0, len(batch)),
		Types:       make([]string, 0, len(batch)),
		Quantities:  make([]decimal.Decimal, 0, len(batch)),
		OccurredAts: make([]pgtype.Timestamptz, 0, len(batch)),
		Reasons:     make([]string, 0, len(batch)),
		Comments:    make([]string, 0, len(batch)),
	}

	for _, m := range batch {
		// Знак ставит тип движения — то же правило, что и в сервисе склада.
		value := m.Qty.Abs()
		if m.Type == stock.TypeUsage || m.Type == stock.TypeWriteoff {
			value = value.Neg()
		}

		params.Ids = append(params.Ids, uuid.Must(uuid.NewV7()))
		params.ItemIds = append(params.ItemIds, m.ItemID)
		params.Types = append(params.Types, string(m.Type))
		params.Quantities = append(params.Quantities, value.Decimal())
		params.OccurredAts = append(params.OccurredAts, postgres.Time(m.At))
		params.Reasons = append(params.Reasons, m.Reason)
		params.Comments = append(params.Comments, m.Note)
	}

	written, err := q.BulkInsertMovements(ctx, params)
	if err != nil {
		return fmt.Errorf("sandbox: запись движений: %w", err)
	}
	if int(written) != len(batch) {
		return fmt.Errorf("sandbox: записано %d движений из %d", written, len(batch))
	}
	return nil
}

// generateItemHistory считает историю одной позиции и возвращает движения
// вместе с итоговым остатком. В БД ничего не пишет: запись идёт одним COPY.
func (s *Service) generateItemHistory(
	tn tenant.Tenant, ownerID uuid.UUID, item createdItem,
	rng *rand.Rand, start clock.Day,
) ([]movement, qty.Qty) {
	spec := item.Spec
	pack := qty.MustParse(spec.Pack)

	out := make([]movement, 0, HistoryDays*2)

	// Стартовый запас — примерно на неделю, округлённый до упаковки.
	balance := qty.FromInt(1).MulFloat(spec.Base * 7).CeilTo(pack)

	out = append(out, movement{
		ItemID: item.ID,
		Type:   stock.TypeOpening,
		Qty:    balance,
		At:     start.AddDays(-1).Time(tn.Loc()).Add(8 * time.Hour),
		By:     ownerID,
		Note:   "Начальный остаток",
	})

	// nextArrival — день, когда приедет заказанное. −1 означает,
	// что заказ не оформлен.
	nextArrival := -1
	arriving := qty.Zero()

	for dayIndex := 0; dayIndex < HistoryDays; dayIndex++ {
		day := start.AddDays(dayIndex)

		// Поставка приехала.
		if nextArrival == dayIndex {
			actual := arriving
			// В 10% случаев привозят не ровно столько, сколько заказали.
			if rng.Float64() < 0.10 {
				actual = arriving.MulFloat(0.8 + rng.Float64()*0.15)
			}
			if actual.IsPositive() {
				out = append(out, movement{
					ItemID: item.ID,
					Type:   stock.TypeReceipt,
					Qty:    actual,
					At:     day.Time(tn.Loc()).Add(9 * time.Hour),
					By:     ownerID,
					Note:   "Приёмка поставки",
				})
				balance = balance.Add(actual)
			}
			nextArrival, arriving = -1, qty.Zero()
		}

		// Спрос дня. Если остатка не хватает — списывается только то,
		// что есть: так появляются дни дефицита, которые прогноз исключает.
		wanted := qty.FromInt(1).MulFloat(demand(rng, spec, dayIndex, day))
		used := wanted
		if used.GreaterThan(balance) {
			used = balance
		}
		if used.IsPositive() {
			out = append(out, movement{
				ItemID: item.ID,
				Type:   stock.TypeUsage,
				Qty:    used,
				At:     day.Time(tn.Loc()).Add(14 * time.Hour),
				By:     ownerID,
			})
			balance = balance.Sub(used)
		}

		// Списания 1–3% от расхода: порча, бой, проливы.
		if used.IsPositive() && rng.Float64() < 0.25 {
			lost := used.MulFloat(writeoffRate(rng))
			if lost.IsPositive() && !lost.GreaterThan(balance) {
				out = append(out, movement{
					ItemID: item.ID,
					Type:   stock.TypeWriteoff,
					Qty:    lost,
					At:     day.Time(tn.Loc()).Add(20 * time.Hour),
					By:     ownerID,
					Reason: pickReason(rng),
				})
				balance = balance.Sub(lost)
			}
		}

		// Решение о заказе. Правило упрощённое: запаса меньше чем на
		// четыре дня — заказываем недельную партию.
		//
		// Последние три дня генератор намеренно не заказывает: на входе
		// в демо должно быть что показать — красное и «заказать сегодня» (§7.2).
		if dayIndex >= HistoryDays-3 {
			continue
		}
		if nextArrival < 0 && balance.Float64() < spec.Base*4 {
			// Раз в десять решений владелец забывает заказать: получаются
			// 2–3 дня дефицита, как в жизни (§7.2).
			if rng.Float64() < 0.10 {
				continue
			}
			arriving = qty.FromInt(1).MulFloat(spec.Base * 7).CeilTo(pack)
			nextArrival = dayIndex + s.supplierLead(spec.Supplier)
		}
	}

	return out, balance
}

// supplierLead — срок поставки поставщика в днях.
func (s *Service) supplierLead(name string) int {
	for _, sup := range suppliers {
		if sup.Name == name {
			// Плюс день на то, что поставщик возит не каждый день.
			return sup.LeadDays + 1
		}
	}
	return 2
}

var writeoffReasons = []string{
	string(stock.ReasonSpoiled),
	string(stock.ReasonBroken),
	string(stock.ReasonExpired),
	string(stock.ReasonTasting),
}

func pickReason(rng *rand.Rand) string {
	return writeoffReasons[rng.IntN(len(writeoffReasons))]
}

// movement — одно движение генератора.
type movement struct {
	ItemID uuid.UUID
	Type   stock.Type
	Qty    qty.Qty
	At     time.Time
	By     uuid.UUID
	Reason string
	Note   string
}
