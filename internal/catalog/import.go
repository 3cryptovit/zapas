package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// ImportResult — что получилось импортировать (FR-4).
type ImportResult struct {
	// DryRun: был только предпросмотр, в базе ничего не изменилось.
	DryRun bool `json:"dry_run"`
	// Items — сколько позиций будет создано или создано.
	Items int `json:"items"`
	// Categories и Suppliers — сколько справочников заведено попутно.
	Categories int `json:"categories"`
	Suppliers  int `json:"suppliers"`
	// WithOpening — по скольким позициям записан начальный остаток.
	WithOpening int         `json:"with_opening"`
	Errors      []RowError  `json:"errors,omitempty"`
	Preview     []ImportRow `json:"preview,omitempty"`
}

// OK сообщает, что импорт прошёл или пройдёт без ошибок.
func (r ImportResult) OK() bool { return len(r.Errors) == 0 }

// Import загружает номенклатуру и начальные остатки (FR-4).
//
// Порядок жёсткий: сначала разбор с ошибками по строкам, потом — если
// ошибок нет и dryRun снят — запись одной транзакцией. Либо всё, либо
// ничего: половина загруженного каталога хуже, чем пустой.
func (s *Service) Import(ctx context.Context, t tenant.Tenant, parsed ParseResult, createdBy uuid.UUID, dryRun bool) (ImportResult, error) {
	result := ImportResult{
		DryRun:  dryRun,
		Items:   len(parsed.Rows),
		Errors:  parsed.Errors,
		Preview: parsed.Rows,
	}
	for _, row := range parsed.Rows {
		if row.OpeningQty.IsPositive() {
			result.WithOpening++
		}
	}

	if len(parsed.Errors) > 0 {
		// С ошибками не импортируем даже по явному запросу: частичный
		// импорт пришлось бы разбирать вручную.
		result.DryRun = true
		return result, nil
	}
	if dryRun {
		return result, nil
	}

	// Проверка на конфликт с уже заведёнными позициями идёт внутри
	// транзакции: между предпросмотром и импортом каталог мог измениться.
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		warehouse, err := q.GetDefaultWarehouse(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("catalog: склад тенанта: %w", err)
		}

		categories, created, err := s.resolveCategories(ctx, q, t, parsed.Rows)
		if err != nil {
			return err
		}
		result.Categories = created

		suppliers, createdSuppliers, err := s.resolveSuppliers(ctx, q, t, parsed.Rows)
		if err != nil {
			return err
		}
		result.Suppliers = createdSuppliers

		for _, row := range parsed.Rows {
			itemID, err := s.importItem(ctx, q, t, row, categories, suppliers)
			if err != nil {
				return err
			}

			if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
				TenantID: t.ID, WarehouseID: warehouse.ID, ItemID: itemID,
			}); err != nil {
				return fmt.Errorf("catalog: строка остатка: %w", err)
			}

			if !row.OpeningQty.IsPositive() {
				continue
			}

			// Начальный остаток — отдельный тип движения: в расход
			// для прогноза он не идёт (§3.3).
			if _, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
				ID:          uuid.Must(uuid.NewV7()),
				TenantID:    t.ID,
				WarehouseID: warehouse.ID,
				ItemID:      itemID,
				Type:        sqlc.MovementTypeOpening,
				Qty:         postgres.DecimalOf(row.OpeningQty),
				OccurredAt:  postgres.Time(t.Now(s.clock)),
				CreatedBy:   uuid.NullUUID{UUID: createdBy, Valid: true},
				Comment:     "Импорт из CSV",
			}); err != nil {
				return fmt.Errorf("catalog: начальный остаток %q: %w", row.Name, err)
			}
			if _, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
				WarehouseID: warehouse.ID,
				ItemID:      itemID,
				Delta:       postgres.DecimalOf(row.OpeningQty),
				Now:         postgres.Time(s.clock.Now()),
			}); err != nil {
				return fmt.Errorf("catalog: применение остатка %q: %w", row.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

// resolveCategories заводит недостающие категории и возвращает их по названию.
func (s *Service) resolveCategories(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, rows []ImportRow) (map[string]uuid.UUID, int, error) {
	existing, err := q.ListCategories(ctx, t.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("catalog: категории: %w", err)
	}

	byName := make(map[string]uuid.UUID, len(existing))
	for _, c := range existing {
		byName[strings.ToLower(c.Name)] = c.ID
	}

	created := 0
	for _, row := range rows {
		if row.Category == "" {
			continue
		}
		key := strings.ToLower(row.Category)
		if _, ok := byName[key]; ok {
			continue
		}
		c, err := q.CreateCategory(ctx, sqlc.CreateCategoryParams{
			ID: uuid.Must(uuid.NewV7()), TenantID: t.ID, Name: row.Category,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("catalog: категория %q: %w", row.Category, err)
		}
		byName[key] = c.ID
		created++
	}
	return byName, created, nil
}

// resolveSuppliers заводит недостающих поставщиков.
//
// Условия поставки в файле не указываются: в CSV им не место, их владелец
// задаёт руками (FR-5). Новому поставщику ставятся безопасные значения —
// срок 1 день и ежедневная доставка.
func (s *Service) resolveSuppliers(ctx context.Context, q *sqlc.Queries, t tenant.Tenant, rows []ImportRow) (map[string]uuid.UUID, int, error) {
	existing, err := q.ListSuppliers(ctx, sqlc.ListSuppliersParams{TenantID: t.ID, IncludeArchived: true})
	if err != nil {
		return nil, 0, fmt.Errorf("catalog: поставщики: %w", err)
	}

	byName := make(map[string]uuid.UUID, len(existing))
	for _, sup := range existing {
		byName[strings.ToLower(sup.Name)] = sup.ID
	}

	created := 0
	for _, row := range rows {
		if row.Supplier == "" {
			continue
		}
		key := strings.ToLower(row.Supplier)
		if _, ok := byName[key]; ok {
			continue
		}
		sup, err := q.CreateSupplier(ctx, sqlc.CreateSupplierParams{
			ID:               uuid.Must(uuid.NewV7()),
			TenantID:         t.ID,
			Name:             row.Supplier,
			LeadTimeDays:     1,
			DeliveryWeekdays: []int16{1, 2, 3, 4, 5, 6, 7},
			OrderCutoff:      postgres.TimeOfDay(defaultCutoff),
		})
		if err != nil {
			return nil, 0, fmt.Errorf("catalog: поставщик %q: %w", row.Supplier, err)
		}
		byName[key] = sup.ID
		created++
	}
	return byName, created, nil
}

func (s *Service) importItem(
	ctx context.Context, q *sqlc.Queries, t tenant.Tenant, row ImportRow,
	categories, suppliers map[string]uuid.UUID,
) (uuid.UUID, error) {
	params := sqlc.CreateItemParams{
		ID:           uuid.Must(uuid.NewV7()),
		TenantID:     t.ID,
		Name:         row.Name,
		BaseUnit:     string(row.BaseUnit),
		ServiceLevel: int16(row.ServiceLevel),
		ManualMinQty: postgres.DecimalOf(row.ManualMinQty),
	}
	if id, ok := categories[strings.ToLower(row.Category)]; ok && row.Category != "" {
		params.CategoryID = uuid.NullUUID{UUID: id, Valid: true}
	}

	supplierID, hasSupplier := suppliers[strings.ToLower(row.Supplier)]
	if hasSupplier && row.Supplier != "" {
		params.DefaultSupplierID = uuid.NullUUID{UUID: supplierID, Valid: true}
	}

	item, err := q.CreateItem(ctx, params)
	if err != nil {
		if postgres.IsCode(err, postgres.CodeUniqueViolation) {
			return uuid.Nil, fmt.Errorf("catalog: позиция %q уже есть: %w", row.Name, ErrNameTaken)
		}
		return uuid.Nil, fmt.Errorf("catalog: позиция %q: %w", row.Name, err)
	}

	// Условия закупки заводим, только если поставщик указан.
	if hasSupplier && row.Supplier != "" {
		if _, err := q.UpsertSupplierItem(ctx, sqlc.UpsertSupplierItemParams{
			TenantID:     t.ID,
			SupplierID:   supplierID,
			ItemID:       item.ID,
			PurchaseUnit: row.PurchaseUnit,
			UnitFactor:   postgres.DecimalOf(defaultFactor(row.UnitFactor)),
			MinOrderQty:  postgres.DecimalOf(row.MinOrderQty),
			PackMultiple: postgres.DecimalOf(row.PackMultiple),
			Price:        postgres.NullDecimalOf(row.Price),
		}); err != nil {
			return uuid.Nil, fmt.Errorf("catalog: условия закупки %q: %w", row.Name, err)
		}
	}
	return item.ID, nil
}

// defaultFactor: коэффициент единицы закупки не может быть нулевым.
func defaultFactor(v qty.Qty) qty.Qty {
	if !v.IsPositive() {
		return qty.FromInt(1)
	}
	return v
}
