// Package seed наполняет локальную базу рабочим тенантом.
//
// Это не генератор песочницы (§7.2): здесь ровно столько данных, чтобы было
// куда войти и что потрогать руками при разработке.
package seed

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

// DemoEmail и DemoPassword — учётка для локальной разработки. В проде режим
// seed не запускается: реальных клиентов в MVP нет (§12.2).
const (
	DemoEmail    = "owner@zapas.local"
	DemoPassword = "локальный-пароль-42"
	DemoTenant   = "Кофейня «Локальная»"
)

// Result — что получилось создать.
type Result struct {
	TenantID uuid.UUID
	OwnerID  uuid.UUID
	Email    string
	Password string
	Created  bool
}

// Options — чем наполнять. Пустые поля заменяются значениями
// для локальной разработки.
type Options struct {
	// Email и Password владельца. В проде задаются явно: учётка
	// с общеизвестным паролем на публичном адресе — это дыра.
	Email    string
	Password string
	// TenantName — название организации.
	TenantName string
	// Timezone — часовой пояс: от него зависит «день» в прогнозе (§12).
	Timezone string
	// WithSampleData: заводить ли демонстрационные позиции. В проде
	// владелец заводит свои, а не разбирает чужие.
	WithSampleData bool
}

func (o Options) withDefaults() Options {
	if o.Email == "" {
		o.Email = DemoEmail
	}
	if o.Password == "" {
		o.Password = DemoPassword
	}
	if o.TenantName == "" {
		o.TenantName = DemoTenant
	}
	if o.Timezone == "" {
		o.Timezone = "Europe/Moscow"
	}
	return o
}

// Run создаёт тенанта с владельцем и складом. Повторный запуск
// ничего не дублирует.
func Run(ctx context.Context, db, maint *postgres.DB, cl clock.Clock, opts Options, log *slog.Logger) (Result, error) {
	opts = opts.withDefaults()

	// Проверяем через обслуживающую роль: тенант ещё не известен.
	var existing *sqlc.User
	err := maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		u, err := sqlc.New(tx).GetUserByEmail(ctx, opts.Email)
		if err != nil {
			if postgres.IsNoRows(err) {
				return nil
			}
			return err
		}
		existing = &u
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("seed: поиск владельца: %w", err)
	}
	if existing != nil {
		log.Info("seed: пользователь уже существует, пропускаем",
			slog.String("email", opts.Email),
			slog.String("tenant_id", existing.TenantID.String()))
		return Result{
			TenantID: existing.TenantID,
			OwnerID:  existing.ID,
			Email:    opts.Email,
		}, nil
	}

	tenantID := uuid.Must(uuid.NewV7())
	ownerID := uuid.Must(uuid.NewV7())
	warehouseID := uuid.Must(uuid.NewV7())

	hash, err := auth.HashPassword(opts.Password)
	if err != nil {
		return Result{}, err
	}

	// Тенант и владелец создаются обслуживающей ролью: политика RLS на tenants
	// пропустит вставку только со своим app.tenant_id, а его ещё нет.
	err = maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		if _, err := q.CreateTenant(ctx, sqlc.CreateTenantParams{
			ID:       tenantID,
			Name:     opts.TenantName,
			Timezone: opts.Timezone,
			Settings: []byte(`{}`),
		}); err != nil {
			return fmt.Errorf("создание тенанта: %w", err)
		}
		if _, err := q.CreateWarehouse(ctx, sqlc.CreateWarehouseParams{
			ID: warehouseID, TenantID: tenantID, Name: "Основной склад",
		}); err != nil {
			return fmt.Errorf("создание склада: %w", err)
		}
		if _, err := q.CreateUser(ctx, sqlc.CreateUserParams{
			ID:           ownerID,
			TenantID:     tenantID,
			Lower:        opts.Email,
			PasswordHash: hash,
			Name:         "Владелец",
			Role:         sqlc.UserRoleOwner,
		}); err != nil {
			return fmt.Errorf("создание владельца: %w", err)
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("seed: %w", err)
	}

	if !opts.WithSampleData {
		log.Info("seed: тенант создан",
			slog.String("tenant", opts.TenantName),
			slog.String("email", opts.Email))
		return Result{
			TenantID: tenantID,
			OwnerID:  ownerID,
			Email:    opts.Email,
			Password: opts.Password,
			Created:  true,
		}, nil
	}

	// Справочники заводим уже под ролью приложения — так же, как это делал бы
	// владелец через интерфейс.
	err = db.InTenantTx(ctx, tenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		categories := map[string]uuid.UUID{}
		for _, name := range []string{"Молочка", "Кофе", "Сиропы", "Упаковка"} {
			row, err := q.CreateCategory(ctx, sqlc.CreateCategoryParams{
				ID: uuid.Must(uuid.NewV7()), TenantID: tenantID, Name: name,
			})
			if err != nil {
				return fmt.Errorf("категория %q: %w", name, err)
			}
			categories[name] = row.ID
		}

		// Молочная ферма из §7.2: возит по пн и чт, срок 1 день, отсечка 16:00.
		dairy, err := q.CreateSupplier(ctx, sqlc.CreateSupplierParams{
			ID:               uuid.Must(uuid.NewV7()),
			TenantID:         tenantID,
			Name:             "Молочная ферма",
			Contact:          "@dairy_farm",
			LeadTimeDays:     1,
			DeliveryWeekdays: []int16{1, 4},
			OrderCutoff:      postgres.TimeOfDay(clock.TimeOfDay{Hour: 16}),
		})
		if err != nil {
			return fmt.Errorf("поставщик: %w", err)
		}

		items := []struct {
			name     string
			unit     string
			category string
			// pack — кратность упаковки в базовых единицах.
			pack       string
			unitLabel  string
			unitFactor string
			opening    string
		}{
			{"Молоко 3,2%", "l", "Молочка", "12", "кор.", "12", "24"},
			{"Сливки 33%", "l", "Молочка", "6", "кор.", "6", "12"},
			{"Зерно Бразилия", "kg", "Кофе", "1", "уп.", "1", "8"},
			{"Сироп карамель", "l", "Сиропы", "1", "бут.", "1", "3"},
			{"Стаканы 350 мл", "pcs", "Упаковка", "50", "уп.", "50", "400"},
		}

		for _, it := range items {
			categoryID := categories[it.category]
			item, err := q.CreateItem(ctx, sqlc.CreateItemParams{
				ID:                uuid.Must(uuid.NewV7()),
				TenantID:          tenantID,
				CategoryID:        uuid.NullUUID{UUID: categoryID, Valid: true},
				Name:              it.name,
				BaseUnit:          it.unit,
				DefaultSupplierID: uuid.NullUUID{UUID: dairy.ID, Valid: true},
				ServiceLevel:      95,
				ManualMinQty:      qty.Zero().Decimal(),
			})
			if err != nil {
				return fmt.Errorf("позиция %q: %w", it.name, err)
			}

			if _, err := q.UpsertSupplierItem(ctx, sqlc.UpsertSupplierItemParams{
				TenantID:     tenantID,
				SupplierID:   dairy.ID,
				ItemID:       item.ID,
				PurchaseUnit: it.unitLabel,
				UnitFactor:   qty.MustParse(it.unitFactor).Decimal(),
				MinOrderQty:  qty.Zero().Decimal(),
				PackMultiple: qty.MustParse(it.pack).Decimal(),
			}); err != nil {
				return fmt.Errorf("условия закупки %q: %w", it.name, err)
			}

			if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
				TenantID: tenantID, WarehouseID: warehouseID, ItemID: item.ID,
			}); err != nil {
				return fmt.Errorf("остаток %q: %w", it.name, err)
			}

			// Начальный остаток — отдельный тип движения: в расход он не идёт (§3.3).
			opening := qty.MustParse(it.opening)
			if _, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
				ID:          uuid.Must(uuid.NewV7()),
				TenantID:    tenantID,
				WarehouseID: warehouseID,
				ItemID:      item.ID,
				Type:        sqlc.MovementTypeOpening,
				Qty:         opening.Decimal(),
				OccurredAt:  postgres.Time(cl.Now()),
				CreatedBy:   uuid.NullUUID{UUID: ownerID, Valid: true},
				Comment:     "Начальный остаток",
			}); err != nil {
				return fmt.Errorf("начальный остаток %q: %w", it.name, err)
			}
			if _, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
				WarehouseID: warehouseID,
				ItemID:      item.ID,
				Delta:       opening.Decimal(),
				Now:         postgres.Time(cl.Now()),
			}); err != nil {
				return fmt.Errorf("применение остатка %q: %w", it.name, err)
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("seed: справочники: %w", err)
	}

	log.Info("seed: тенант создан с демонстрационными данными",
		slog.String("tenant", opts.TenantName),
		slog.String("email", opts.Email),
	)

	return Result{
		TenantID: tenantID,
		OwnerID:  ownerID,
		Email:    opts.Email,
		Password: opts.Password,
		Created:  true,
	}, nil
}
