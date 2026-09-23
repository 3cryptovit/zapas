package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

// SeedHistory набивает историю расхода прямо в журнал, минуя сервис.
//
// Через сервис так нельзя: он не принимает движения дальше семи дней назад
// (FR-9). Генератор песочницы работает так же — пишет историю напрямую.
//
// Возвращает остаток, который получился после всех движений.
func (f *Fixture) SeedHistory(
	t *testing.T,
	itemID uuid.UUID,
	from clock.Day,
	days int,
	perDay func(i int, day clock.Day) string,
) qty.Qty {
	t.Helper()
	ctx := context.Background()

	// Начальный остаток с запасом: иначе CHECK (on_hand >= 0) не даст
	// записать расход, а дни дефицита исказят ряд.
	opening := qty.Zero()
	consumption := make([]qty.Qty, days)
	for i := 0; i < days; i++ {
		consumption[i] = qty.MustParse(perDay(i, from.AddDays(i)))
		opening = opening.Add(consumption[i])
	}
	opening = opening.Add(qty.FromInt(1000))

	balance := opening

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID: f.Tenant.ID, WarehouseID: f.Warehouse, ItemID: itemID,
		}); err != nil {
			return err
		}

		// Начальный остаток ставим за день до начала истории: в расход
		// он не идёт (§3.3), но обеспечивает положительный баланс.
		openingAt := from.AddDays(-1).Time(f.Tenant.Loc()).Add(8 * time.Hour)
		if _, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
			ID:          uuid.Must(uuid.NewV7()),
			TenantID:    f.Tenant.ID,
			WarehouseID: f.Warehouse,
			ItemID:      itemID,
			Type:        sqlc.MovementTypeOpening,
			Qty:         opening.Decimal(),
			OccurredAt:  postgres.Time(openingAt),
			CreatedBy:   uuid.NullUUID{UUID: f.Owner, Valid: true},
			Comment:     "Начальный остаток истории",
		}); err != nil {
			return err
		}

		for i := 0; i < days; i++ {
			used := consumption[i]
			if used.IsZero() {
				continue
			}
			// Полдень дня в поясе тенанта: так движение попадает точно
			// в свой календарный день.
			at := from.AddDays(i).Time(f.Tenant.Loc()).Add(12 * time.Hour)
			if _, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
				ID:          uuid.Must(uuid.NewV7()),
				TenantID:    f.Tenant.ID,
				WarehouseID: f.Warehouse,
				ItemID:      itemID,
				Type:        sqlc.MovementTypeUsage,
				Qty:         used.Neg().Decimal(),
				OccurredAt:  postgres.Time(at),
				CreatedBy:   uuid.NullUUID{UUID: f.Staff, Valid: true},
			}); err != nil {
				return err
			}
			balance = balance.Sub(used)
		}

		_, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
			WarehouseID: f.Warehouse,
			ItemID:      itemID,
			Delta:       balance.Decimal(),
			Now:         postgres.Time(f.Env.Clock.Now()),
		})
		return err
	})
	if err != nil {
		t.Fatalf("набивка истории: %v", err)
	}
	return balance
}

// SetOnHand выставляет остаток корректировкой: тесту бывает нужно поставить
// позицию в заранее заданное состояние, не выдумывая цепочку движений.
func (f *Fixture) SetOnHand(t *testing.T, itemID uuid.UUID, target string) {
	t.Helper()
	ctx := context.Background()
	want := qty.MustParse(target)

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID: f.Tenant.ID, WarehouseID: f.Warehouse, ItemID: itemID,
		}); err != nil {
			return err
		}
		current, err := q.LockBalance(ctx, sqlc.LockBalanceParams{
			WarehouseID: f.Warehouse, ItemID: itemID,
		})
		if err != nil {
			return err
		}
		delta := want.Sub(postgres.Qty(current))
		if delta.IsZero() {
			return nil
		}

		if _, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
			ID:          uuid.Must(uuid.NewV7()),
			TenantID:    f.Tenant.ID,
			WarehouseID: f.Warehouse,
			ItemID:      itemID,
			Type:        sqlc.MovementTypeAdjustment,
			Qty:         delta.Decimal(),
			OccurredAt:  postgres.Time(f.Env.Clock.Now()),
			CreatedBy:   uuid.NullUUID{UUID: f.Owner, Valid: true},
			Comment:     "Выставление остатка в тесте",
		}); err != nil {
			return err
		}
		_, err = q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
			WarehouseID: f.Warehouse,
			ItemID:      itemID,
			Delta:       delta.Decimal(),
			Now:         postgres.Time(f.Env.Clock.Now()),
		})
		return err
	})
	if err != nil {
		t.Fatalf("выставление остатка: %v", err)
	}
}

// SetTerms задаёт условия закупки позиции у поставщика.
func (f *Fixture) SetTerms(t *testing.T, supplierID, itemID uuid.UUID, purchaseUnit, unitFactor, pack, minOrder string) {
	t.Helper()
	ctx := context.Background()

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		_, err := sqlc.New(tx).UpsertSupplierItem(ctx, sqlc.UpsertSupplierItemParams{
			TenantID:     f.Tenant.ID,
			SupplierID:   supplierID,
			ItemID:       itemID,
			PurchaseUnit: purchaseUnit,
			UnitFactor:   qty.MustParse(unitFactor).Decimal(),
			MinOrderQty:  qty.MustParse(minOrder).Decimal(),
			PackMultiple: qty.MustParse(pack).Decimal(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("условия закупки: %v", err)
	}
}

// SetDefaultSupplier привязывает поставщика к позиции.
func (f *Fixture) SetDefaultSupplier(t *testing.T, itemID, supplierID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		_, err := sqlc.New(tx).UpdateItem(ctx, sqlc.UpdateItemParams{
			TenantID:          f.Tenant.ID,
			ID:                itemID,
			DefaultSupplierID: uuid.NullUUID{UUID: supplierID, Valid: true},
		})
		return err
	})
	if err != nil {
		t.Fatalf("поставщик по умолчанию: %v", err)
	}
}

// ItemStatusOnOrder читает «в пути» из read-модели дашборда.
func (f *Fixture) ItemStatusOnOrder(t *testing.T, itemID uuid.UUID) qty.Qty {
	t.Helper()
	return postgres.Qty(f.itemStatus(t, itemID).OnOrder)
}

// ItemStatusCode читает код статуса позиции.
func (f *Fixture) ItemStatusCode(t *testing.T, itemID uuid.UUID) string {
	t.Helper()
	return string(f.itemStatus(t, itemID).Status)
}

func (f *Fixture) itemStatus(t *testing.T, itemID uuid.UUID) sqlc.ItemStatus {
	t.Helper()
	ctx := context.Background()

	var out sqlc.ItemStatus
	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetItemStatus(ctx, sqlc.GetItemStatusParams{
			TenantID: f.Tenant.ID, ItemID: itemID,
		})
		if err != nil {
			return err
		}
		out = row
		return nil
	})
	if err != nil {
		t.Fatalf("чтение статуса позиции: %v", err)
	}
	return out
}

// LinkTelegram привязывает чат Telegram к пользователю: без этого слать
// уведомления некуда.
func (f *Fixture) LinkTelegram(t *testing.T, userID uuid.UUID, chatID int64) {
	t.Helper()
	ctx := context.Background()

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).SetUserTelegramChatID(ctx, sqlc.SetUserTelegramChatIDParams{
			ID:             userID,
			TelegramChatID: &chatID,
		})
	})
	if err != nil {
		t.Fatalf("привязка Telegram: %v", err)
	}
}
