package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Password — пароль обоих тестовых пользователей.
const Password = "тестовый-пароль-42"

// Fixture — заготовленный тенант со складом и пользователями.
type Fixture struct {
	Env    *Env
	Tenant tenant.Tenant
	Owner  uuid.UUID
	Staff  uuid.UUID
	// OwnerEmail и StaffEmail нужны тестам входа.
	OwnerEmail string
	StaffEmail string
	// Warehouse — единственный склад тенанта (в MVP их не больше одного).
	Warehouse uuid.UUID
}

// NewTenant заводит тенанта, склад, владельца и сотрудника.
//
// Тенанты и есть граница изоляции, поэтому каждому тесту хватает своего:
// пересоздавать базу на каждый случай не нужно.
func (e *Env) NewTenant(t *testing.T, name string) *Fixture {
	t.Helper()
	ctx := context.Background()

	tenantID := uuid.Must(uuid.NewV7())
	f := &Fixture{
		Env:   e,
		Owner: uuid.Must(uuid.NewV7()),
		Staff: uuid.Must(uuid.NewV7()),
		// Email уникален глобально, поэтому в адрес идёт весь id тенанта:
		// у двух UUIDv7, созданных в одну миллисекунду, совпадает префикс.
		OwnerEmail: "owner-" + tenantID.String() + "@example.test",
		StaffEmail: "staff-" + tenantID.String() + "@example.test",
		Warehouse:  uuid.Must(uuid.NewV7()),
		Tenant: tenant.Tenant{
			ID:       tenantID,
			Name:     name,
			Location: time.UTC,
			Settings: tenant.DefaultSettings(),
		},
	}

	// Создание тенанта идёт от обслуживающей роли: RLS ещё некому применять.
	err := e.Maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		if _, err := q.CreateTenant(ctx, sqlc.CreateTenantParams{
			ID:       tenantID,
			Name:     name,
			Timezone: "UTC",
			Settings: []byte(`{}`),
		}); err != nil {
			return err
		}
		if _, err := q.CreateWarehouse(ctx, sqlc.CreateWarehouseParams{
			ID: f.Warehouse, TenantID: tenantID, Name: "Основной склад",
		}); err != nil {
			return err
		}

		hash, err := auth.HashPassword(Password)
		if err != nil {
			return err
		}
		for _, u := range []struct {
			id    uuid.UUID
			email string
			role  sqlc.UserRole
			name  string
		}{
			{f.Owner, f.OwnerEmail, sqlc.UserRoleOwner, "Владелец"},
			{f.Staff, f.StaffEmail, sqlc.UserRoleStaff, "Бариста"},
		} {
			if _, err := q.CreateUser(ctx, sqlc.CreateUserParams{
				ID:           u.id,
				TenantID:     tenantID,
				Lower:        u.email,
				PasswordHash: hash,
				Name:         u.name,
				Role:         u.role,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("создание тенанта: %v", err)
	}
	return f
}

// NewItem заводит позицию и строку остатка.
func (f *Fixture) NewItem(t *testing.T, name, unit string) uuid.UUID {
	t.Helper()
	return f.NewItemWithMin(t, name, unit, "0")
}

// NewItemWithMin заводит позицию с ручным минимальным остатком (модель M0).
func (f *Fixture) NewItemWithMin(t *testing.T, name, unit, manualMin string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	itemID := uuid.Must(uuid.NewV7())

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.CreateItem(ctx, sqlc.CreateItemParams{
			ID:           itemID,
			TenantID:     f.Tenant.ID,
			Name:         name,
			BaseUnit:     unit,
			ServiceLevel: 95,
			ManualMinQty: qty.MustParse(manualMin).Decimal(),
		}); err != nil {
			return err
		}
		return q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID:    f.Tenant.ID,
			WarehouseID: f.Warehouse,
			ItemID:      itemID,
		})
	})
	if err != nil {
		t.Fatalf("создание позиции %q: %v", name, err)
	}
	return itemID
}

// NewSupplier заводит поставщика с условиями доставки.
func (f *Fixture) NewSupplier(t *testing.T, name string, leadDays int, weekdays []int16, cutoff string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())

	parsedCutoff, err := time.Parse("15:04", cutoff)
	if err != nil {
		t.Fatalf("время отсечки %q: %v", cutoff, err)
	}

	err = f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		_, err := sqlc.New(tx).CreateSupplier(ctx, sqlc.CreateSupplierParams{
			ID:               id,
			TenantID:         f.Tenant.ID,
			Name:             name,
			LeadTimeDays:     int16(leadDays),
			DeliveryWeekdays: weekdays,
			OrderCutoff:      pgTime(parsedCutoff),
		})
		return err
	})
	if err != nil {
		t.Fatalf("создание поставщика %q: %v", name, err)
	}
	return id
}

// OnHand читает текущий остаток позиции.
func (f *Fixture) OnHand(t *testing.T, itemID uuid.UUID) qty.Qty {
	t.Helper()
	ctx := context.Background()

	var out qty.Qty
	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetBalance(ctx, sqlc.GetBalanceParams{
			TenantID: f.Tenant.ID, ItemID: itemID,
		})
		if err != nil {
			return err
		}
		out = postgres.Qty(row.OnHand)
		return nil
	})
	if err != nil {
		t.Fatalf("чтение остатка: %v", err)
	}
	return out
}

// SumMovements складывает журнал движений позиции. Он и есть источник истины:
// остаток обязан совпасть с этой суммой (FR-17).
func (f *Fixture) SumMovements(t *testing.T, itemID uuid.UUID) qty.Qty {
	t.Helper()
	ctx := context.Background()

	total := qty.Zero()
	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).SumMovementsByItem(ctx, f.Tenant.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ItemID == itemID {
				total = postgres.Qty(row.Total)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("сумма движений: %v", err)
	}
	return total
}

// ArchiveItem отправляет позицию в архив: она не удаляется, но новых движений
// не принимает (FR-1).
func (f *Fixture) ArchiveItem(t *testing.T, itemID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).ArchiveItem(ctx, sqlc.ArchiveItemParams{
			TenantID:   f.Tenant.ID,
			ID:         itemID,
			ArchivedAt: postgres.Time(f.Env.Clock.Now()),
		})
	})
	if err != nil {
		t.Fatalf("архивация позиции: %v", err)
	}
}
