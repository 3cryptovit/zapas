package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Service создаёт и обслуживает демо-тенанты.
type Service struct {
	// db — роль приложения: данные создаются так же, как их создавал бы владелец.
	db *postgres.DB
	// maint — обслуживающая роль: сам тенант и его удаление (ADR-005).
	maint    *postgres.DB
	clock    clock.Clock
	pipeline *pipeline.Service
	log      *slog.Logger
	ttl      time.Duration
	// base — публичный адрес: по нему проверяется Origin при создании демо.
	base string
}

// baseURL — публичный адрес приложения.
func (s *Service) baseURL() string { return s.base }

func NewService(db, maint *postgres.DB, cl clock.Clock, pipe *pipeline.Service, ttl time.Duration, base string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Service{db: db, maint: maint, clock: cl, pipeline: pipe, ttl: ttl, base: base, log: log}
}

// Created — что получилось создать.
type Created struct {
	TenantID  uuid.UUID
	OwnerID   uuid.UUID
	Email     string
	Seed      int64
	ExpiresAt time.Time
}

// Create собирает демо-тенанта: справочники, 90 дней истории, прогноз
// и статусы (§7.1).
//
// Всё в одной транзакции: если что-то упало, полупустого тенанта
// не остаётся. Прогноз считается после — он и так идемпотентен.
func (s *Service) Create(ctx context.Context, seed int64) (Created, error) {
	if seed == 0 {
		seed = s.clock.Now().UnixNano()
	}

	tenantID := uuid.Must(uuid.NewV7())
	ownerID := uuid.Must(uuid.NewV7())
	warehouseID := uuid.Must(uuid.NewV7())
	expiresAt := s.clock.Now().Add(s.ttl)

	// Email уникален глобально, а демо-тенантов много: подставляем id.
	email := fmt.Sprintf("demo-%s@sandbox.local", tenantID)

	hash, err := auth.HashPassword(fmt.Sprintf("demo-%s", tenantID))
	if err != nil {
		return Created{}, err
	}

	// Тенант и владелец создаются обслуживающей ролью: политика RLS
	// пропустит вставку только со своим app.tenant_id, а его ещё нет.
	err = s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		if _, err := q.CreateTenant(ctx, sqlc.CreateTenantParams{
			ID:        tenantID,
			Name:      "Кофейня «Демо»",
			Timezone:  "Europe/Moscow",
			IsSandbox: true,
			ExpiresAt: postgres.Time(expiresAt),
			Seed:      seed,
			Settings:  []byte(`{}`),
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
			Lower:        email,
			PasswordHash: hash,
			Name:         "Владелец демо",
			Role:         sqlc.UserRoleOwner,
		}); err != nil {
			return fmt.Errorf("создание владельца: %w", err)
		}
		return nil
	})
	if err != nil {
		return Created{}, fmt.Errorf("sandbox: %w", err)
	}

	tn := tenant.Tenant{
		ID:        tenantID,
		Name:      "Кофейня «Демо»",
		Location:  tenant.LoadLocation("Europe/Moscow"),
		IsSandbox: true,
		ExpiresAt: &expiresAt,
		Seed:      seed,
		Settings:  tenant.DefaultSettings(),
	}

	if err := s.fill(ctx, tn, warehouseID, ownerID, seed); err != nil {
		// Неудача при наполнении не должна оставлять пустого тенанта.
		s.deleteTenant(ctx, tenantID)
		return Created{}, err
	}

	// Прогноз и статусы: без них дашборд пустой.
	if _, err := s.pipeline.Run(ctx, tn); err != nil {
		s.deleteTenant(ctx, tenantID)
		return Created{}, fmt.Errorf("sandbox: прогноз: %w", err)
	}

	s.log.InfoContext(ctx, "создана песочница",
		slog.String("tenant_id", tenantID.String()),
		slog.Int64("seed", seed),
	)

	return Created{
		TenantID:  tenantID,
		OwnerID:   ownerID,
		Email:     email,
		Seed:      seed,
		ExpiresAt: expiresAt,
	}, nil
}

// fill наполняет тенанта справочниками и историей.
func (s *Service) fill(ctx context.Context, tn tenant.Tenant, warehouseID, ownerID uuid.UUID, seed int64) error {
	return s.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		supplierIDs, err := s.createSuppliers(ctx, q, tn)
		if err != nil {
			return err
		}
		categoryIDs, err := s.createCategories(ctx, q, tn)
		if err != nil {
			return err
		}
		items, err := s.createItems(ctx, q, tn, warehouseID, categoryIDs, supplierIDs)
		if err != nil {
			return err
		}

		return s.generateHistory(ctx, q, tn, warehouseID, ownerID, items, seed)
	})
}

func (s *Service) createSuppliers(ctx context.Context, q *sqlc.Queries, tn tenant.Tenant) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID, len(suppliers))

	for _, spec := range suppliers {
		cutoff, err := clock.ParseTimeOfDay(spec.Cutoff)
		if err != nil {
			return nil, fmt.Errorf("sandbox: отсечка %q: %w", spec.Cutoff, err)
		}
		row, err := q.CreateSupplier(ctx, sqlc.CreateSupplierParams{
			ID:               uuid.Must(uuid.NewV7()),
			TenantID:         tn.ID,
			Name:             spec.Name,
			Contact:          spec.Contact,
			LeadTimeDays:     int16(spec.LeadDays),
			DeliveryWeekdays: spec.Weekdays,
			OrderCutoff:      postgres.TimeOfDay(cutoff),
		})
		if err != nil {
			return nil, fmt.Errorf("sandbox: поставщик %q: %w", spec.Name, err)
		}
		out[spec.Name] = row.ID
	}
	return out, nil
}

func (s *Service) createCategories(ctx context.Context, q *sqlc.Queries, tn tenant.Tenant) (map[string]uuid.UUID, error) {
	out := map[string]uuid.UUID{}

	for _, spec := range catalog {
		if _, ok := out[spec.Category]; ok {
			continue
		}
		row, err := q.CreateCategory(ctx, sqlc.CreateCategoryParams{
			ID: uuid.Must(uuid.NewV7()), TenantID: tn.ID, Name: spec.Category,
		})
		if err != nil {
			return nil, fmt.Errorf("sandbox: категория %q: %w", spec.Category, err)
		}
		out[spec.Category] = row.ID
	}
	return out, nil
}

// createdItem — созданная позиция вместе со спецификацией генератора.
type createdItem struct {
	ID   uuid.UUID
	Spec itemSpec
}

func (s *Service) createItems(
	ctx context.Context, q *sqlc.Queries, tn tenant.Tenant,
	warehouseID uuid.UUID, categories, suppliersByName map[string]uuid.UUID,
) ([]createdItem, error) {
	out := make([]createdItem, 0, len(catalog))

	for _, spec := range catalog {
		supplierID := suppliersByName[spec.Supplier]

		row, err := q.CreateItem(ctx, sqlc.CreateItemParams{
			ID:                uuid.Must(uuid.NewV7()),
			TenantID:          tn.ID,
			CategoryID:        uuid.NullUUID{UUID: categories[spec.Category], Valid: true},
			Name:              spec.Name,
			BaseUnit:          spec.Unit,
			DefaultSupplierID: uuid.NullUUID{UUID: supplierID, Valid: true},
			ServiceLevel:      95,
			ManualMinQty:      qty.Zero().Decimal(),
		})
		if err != nil {
			return nil, fmt.Errorf("sandbox: позиция %q: %w", spec.Name, err)
		}

		price := qty.MustParse(spec.Price)
		if _, err := q.UpsertSupplierItem(ctx, sqlc.UpsertSupplierItemParams{
			TenantID:     tn.ID,
			SupplierID:   supplierID,
			ItemID:       row.ID,
			PurchaseUnit: spec.PurchaseUnit,
			UnitFactor:   qty.MustParse(spec.UnitFactor).Decimal(),
			MinOrderQty:  qty.Zero().Decimal(),
			PackMultiple: qty.MustParse(spec.Pack).Decimal(),
			Price:        postgres.NullDecimalOf(&price),
		}); err != nil {
			return nil, fmt.Errorf("sandbox: условия закупки %q: %w", spec.Name, err)
		}

		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID: tn.ID, WarehouseID: warehouseID, ItemID: row.ID,
		}); err != nil {
			return nil, fmt.Errorf("sandbox: остаток %q: %w", spec.Name, err)
		}

		out = append(out, createdItem{ID: row.ID, Spec: spec})
	}
	return out, nil
}

// deleteTenant убирает тенанта целиком: каскад уносит всё содержимое.
func (s *Service) deleteTenant(ctx context.Context, tenantID uuid.UUID) {
	err := s.maint.InTx(context.WithoutCancel(ctx), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).DeleteTenant(ctx, tenantID)
	})
	if err != nil {
		s.log.ErrorContext(ctx, "не удалось удалить неудавшуюся песочницу",
			slog.String("tenant_id", tenantID.String()),
			slog.String("err", err.Error()),
		)
	}
}

// newRNG строит генератор из seed. Второй поток задаётся номером,
// чтобы разные части генерации не мешали друг другу.
func newRNG(seed int64, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(uint64(seed), stream))
}
