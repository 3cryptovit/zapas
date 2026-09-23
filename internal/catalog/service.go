package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Service — операции над номенклатурой и поставщиками.
type Service struct {
	db    *postgres.DB
	clock clock.Clock
}

func NewService(db *postgres.DB, cl clock.Clock) *Service {
	return &Service{db: db, clock: cl}
}

// --- позиции ---

// CreateItemInput — что нужно, чтобы завести позицию.
type CreateItemInput struct {
	Name         string
	BaseUnit     BaseUnit
	CategoryID   *uuid.UUID
	SupplierID   *uuid.UUID
	ServiceLevel int
	ManualMinQty qty.Qty
}

// CreateItem заводит позицию и сразу строку остатка: дашборд должен показывать
// новую позицию с нулём, а не пропускать её.
func (s *Service) CreateItem(ctx context.Context, t tenant.Tenant, in CreateItemInput) (Item, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Item{}, fmt.Errorf("catalog: пустое название позиции")
	}
	if !in.BaseUnit.Valid() {
		return Item{}, ErrInvalidUnit
	}
	if in.ServiceLevel == 0 {
		in.ServiceLevel = 95
	}
	if !ValidServiceLevel(in.ServiceLevel) {
		return Item{}, ErrInvalidService
	}

	var out Item
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		warehouse, err := q.GetDefaultWarehouse(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("catalog: склад тенанта: %w", err)
		}

		row, err := q.CreateItem(ctx, sqlc.CreateItemParams{
			ID:                uuid.Must(uuid.NewV7()),
			TenantID:          t.ID,
			CategoryID:        nullUUID(in.CategoryID),
			Name:              in.Name,
			BaseUnit:          string(in.BaseUnit),
			DefaultSupplierID: nullUUID(in.SupplierID),
			ServiceLevel:      int16(in.ServiceLevel),
			ManualMinQty:      postgres.DecimalOf(in.ManualMinQty),
		})
		if err != nil {
			if postgres.IsCode(err, postgres.CodeUniqueViolation) {
				return ErrNameTaken
			}
			return fmt.Errorf("catalog: создание позиции: %w", err)
		}

		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID:    t.ID,
			WarehouseID: warehouse.ID,
			ItemID:      row.ID,
		}); err != nil {
			return fmt.Errorf("catalog: строка остатка: %w", err)
		}

		out = itemFromRow(row)
		return nil
	})
	return out, err
}

// UpdateItemInput — частичное изменение позиции (PATCH).
type UpdateItemInput struct {
	Name         *string
	CategoryID   *uuid.UUID
	SupplierID   *uuid.UUID
	ServiceLevel *int
	ManualMinQty *qty.Qty
}

// UpdateItem меняет только переданные поля.
func (s *Service) UpdateItem(ctx context.Context, t tenant.Tenant, id uuid.UUID, in UpdateItemInput) (Item, error) {
	if in.ServiceLevel != nil && !ValidServiceLevel(*in.ServiceLevel) {
		return Item{}, ErrInvalidService
	}

	params := sqlc.UpdateItemParams{TenantID: t.ID, ID: id}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return Item{}, fmt.Errorf("catalog: пустое название позиции")
		}
		params.Name = &name
	}
	if in.CategoryID != nil {
		params.CategoryID = uuid.NullUUID{UUID: *in.CategoryID, Valid: true}
	}
	if in.SupplierID != nil {
		params.DefaultSupplierID = uuid.NullUUID{UUID: *in.SupplierID, Valid: true}
	}
	if in.ServiceLevel != nil {
		level := int16(*in.ServiceLevel)
		params.ServiceLevel = &level
	}
	if in.ManualMinQty != nil {
		params.ManualMinQty = postgres.NullDecimalOf(in.ManualMinQty)
	}

	var out Item
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).UpdateItem(ctx, params)
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			if postgres.IsCode(err, postgres.CodeUniqueViolation) {
				return ErrNameTaken
			}
			return fmt.Errorf("catalog: изменение позиции: %w", err)
		}
		out = itemFromRow(row)
		return nil
	})
	return out, err
}

// ArchiveItem убирает позицию из работы. Удаления нет: её движения остаются
// в журнале, а история нужна прогнозу (FR-1).
func (s *Service) ArchiveItem(ctx context.Context, t tenant.Tenant, id uuid.UUID) error {
	return s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetItem(ctx, sqlc.GetItemParams{TenantID: t.ID, ID: id}); err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("catalog: чтение позиции: %w", err)
		}
		return q.ArchiveItem(ctx, sqlc.ArchiveItemParams{
			TenantID:   t.ID,
			ID:         id,
			ArchivedAt: postgres.Time(s.clock.Now()),
		})
	})
}

// GetItem отдаёт одну позицию.
func (s *Service) GetItem(ctx context.Context, t tenant.Tenant, id uuid.UUID) (Item, error) {
	var out Item
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetItem(ctx, sqlc.GetItemParams{TenantID: t.ID, ID: id})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("catalog: чтение позиции: %w", err)
		}
		out = itemFromRow(row)
		return nil
	})
	return out, err
}

// ListItems отдаёт номенклатуру. До 500 позиций без пагинации (§6.1).
func (s *Service) ListItems(ctx context.Context, t tenant.Tenant, includeArchived bool) ([]Item, error) {
	var out []Item
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListItems(ctx, sqlc.ListItemsParams{
			TenantID:        t.ID,
			IncludeArchived: includeArchived,
		})
		if err != nil {
			return fmt.Errorf("catalog: список позиций: %w", err)
		}
		out = make([]Item, 0, len(rows))
		for _, row := range rows {
			item := Item{
				ID:           row.ID,
				Name:         row.Name,
				BaseUnit:     BaseUnit(row.BaseUnit),
				CategoryID:   ptrUUID(row.CategoryID),
				SupplierID:   ptrUUID(row.DefaultSupplierID),
				ServiceLevel: int(row.ServiceLevel),
				ManualMinQty: postgres.Qty(row.ManualMinQty),
				ArchivedAt:   postgres.TimePtr(row.ArchivedAt),
			}
			item.UnitLabel = item.BaseUnit.Label()
			if row.CategoryName != nil {
				item.CategoryName = *row.CategoryName
			}
			if row.SupplierName != nil {
				item.SupplierName = *row.SupplierName
			}
			out = append(out, item)
		}
		return nil
	})
	return out, err
}

// --- категории ---

// ListCategories отдаёт плоский список категорий.
func (s *Service) ListCategories(ctx context.Context, t tenant.Tenant) ([]Category, error) {
	var out []Category
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListCategories(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("catalog: категории: %w", err)
		}
		out = make([]Category, 0, len(rows))
		for _, row := range rows {
			out = append(out, Category{ID: row.ID, Name: row.Name})
		}
		return nil
	})
	return out, err
}

// CreateCategory заводит категорию.
func (s *Service) CreateCategory(ctx context.Context, t tenant.Tenant, name string) (Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Category{}, fmt.Errorf("catalog: пустое название категории")
	}

	var out Category
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).CreateCategory(ctx, sqlc.CreateCategoryParams{
			ID: uuid.Must(uuid.NewV7()), TenantID: t.ID, Name: name,
		})
		if err != nil {
			if postgres.IsCode(err, postgres.CodeUniqueViolation) {
				return ErrNameTaken
			}
			return fmt.Errorf("catalog: создание категории: %w", err)
		}
		out = Category{ID: row.ID, Name: row.Name}
		return nil
	})
	return out, err
}

// --- поставщики ---

// CreateSupplierInput — условия поставки (FR-5).
type CreateSupplierInput struct {
	Name             string
	Contact          string
	LeadTimeDays     int
	DeliveryWeekdays []int
	OrderCutoff      clock.TimeOfDay
}

// CreateSupplier заводит поставщика.
func (s *Service) CreateSupplier(ctx context.Context, t tenant.Tenant, in CreateSupplierInput) (Supplier, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Supplier{}, fmt.Errorf("catalog: пустое название поставщика")
	}
	if !ValidWeekdays(in.DeliveryWeekdays) {
		return Supplier{}, ErrBadWeekdays
	}
	if in.LeadTimeDays < 0 || in.LeadTimeDays > 60 {
		return Supplier{}, fmt.Errorf("catalog: срок поставки вне диапазона 0–60 дней")
	}

	var out Supplier
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).CreateSupplier(ctx, sqlc.CreateSupplierParams{
			ID:               uuid.Must(uuid.NewV7()),
			TenantID:         t.ID,
			Name:             in.Name,
			Contact:          in.Contact,
			LeadTimeDays:     int16(in.LeadTimeDays),
			DeliveryWeekdays: toInt16(in.DeliveryWeekdays),
			OrderCutoff:      postgres.TimeOfDay(in.OrderCutoff),
		})
		if err != nil {
			if postgres.IsCode(err, postgres.CodeUniqueViolation) {
				return ErrNameTaken
			}
			return fmt.Errorf("catalog: создание поставщика: %w", err)
		}
		out = supplierFromRow(row)
		return nil
	})
	return out, err
}

// ListSuppliers отдаёт поставщиков.
func (s *Service) ListSuppliers(ctx context.Context, t tenant.Tenant, includeArchived bool) ([]Supplier, error) {
	var out []Supplier
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListSuppliers(ctx, sqlc.ListSuppliersParams{
			TenantID:        t.ID,
			IncludeArchived: includeArchived,
		})
		if err != nil {
			return fmt.Errorf("catalog: поставщики: %w", err)
		}
		out = make([]Supplier, 0, len(rows))
		for _, row := range rows {
			out = append(out, supplierFromRow(row))
		}
		return nil
	})
	return out, err
}

// GetSupplier отдаёт одного поставщика.
func (s *Service) GetSupplier(ctx context.Context, t tenant.Tenant, id uuid.UUID) (Supplier, error) {
	var out Supplier
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetSupplier(ctx, sqlc.GetSupplierParams{TenantID: t.ID, ID: id})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrNotFound
			}
			return fmt.Errorf("catalog: чтение поставщика: %w", err)
		}
		out = supplierFromRow(row)
		return nil
	})
	return out, err
}

// SetTerms задаёт условия закупки позиции у поставщика (FR-6).
func (s *Service) SetTerms(ctx context.Context, t tenant.Tenant, in Terms) (Terms, error) {
	if !in.UnitFactor.IsPositive() {
		return Terms{}, fmt.Errorf("catalog: коэффициент единицы закупки должен быть больше нуля")
	}

	var out Terms
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).UpsertSupplierItem(ctx, sqlc.UpsertSupplierItemParams{
			TenantID:     t.ID,
			SupplierID:   in.SupplierID,
			ItemID:       in.ItemID,
			PurchaseUnit: in.PurchaseUnit,
			UnitFactor:   postgres.DecimalOf(in.UnitFactor),
			MinOrderQty:  postgres.DecimalOf(in.MinOrderQty),
			PackMultiple: postgres.DecimalOf(in.PackMultiple),
			Price:        postgres.NullDecimalOf(in.Price),
		})
		if err != nil {
			if postgres.IsCode(err, postgres.CodeForeignKeyViolation) {
				return ErrNotFound
			}
			return fmt.Errorf("catalog: условия закупки: %w", err)
		}
		out = termsFromRow(row)
		return nil
	})
	return out, err
}

// GetTermsForItem отдаёт условия закупки у поставщика по умолчанию —
// именно они идут в расчёт рекомендуемого количества (§5.2).
func (s *Service) GetTermsForItem(ctx context.Context, t tenant.Tenant, itemID uuid.UUID) (Terms, bool, error) {
	var (
		out   Terms
		found bool
	)
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetSupplierItemForItem(ctx, sqlc.GetSupplierItemForItemParams{
			TenantID: t.ID, ItemID: itemID,
		})
		if err != nil {
			if postgres.IsNoRows(err) {
				return nil // условий нет — не ошибка
			}
			return fmt.Errorf("catalog: условия закупки: %w", err)
		}
		out = termsFromRow(row)
		found = true
		return nil
	})
	return out, found, err
}
