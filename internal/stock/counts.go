package stock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// CountStatus — статус документа пересчёта (FR-12).
type CountStatus string

const (
	CountDraft  CountStatus = "draft"
	CountPosted CountStatus = "posted"
)

// CountScope — что попало в документ.
type CountScope string

const (
	ScopeAll      CountScope = "all"      // все позиции
	ScopeCategory CountScope = "category" // одна категория
	ScopeKey      CountScope = "key"      // «ключевые» позиции
)

// Count — документ инвентаризации.
type Count struct {
	ID        uuid.UUID   `json:"id"`
	Status    CountStatus `json:"status"`
	Scope     CountScope  `json:"scope"`
	Note      string      `json:"note,omitempty"`
	CreatedBy *uuid.UUID  `json:"created_by,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	PostedAt  *time.Time  `json:"posted_at,omitempty"`
	Lines     []CountLine `json:"lines,omitempty"`
}

// CountLine — строка пересчёта.
type CountLine struct {
	ItemID   uuid.UUID `json:"item_id"`
	ItemName string    `json:"item_name"`
	BaseUnit string    `json:"base_unit"`
	// ExpectedQty — учёт на момент открытия документа.
	ExpectedQty qty.Qty `json:"expected_qty"`
	// CurrentQty — учёт прямо сейчас. Если он разошёлся с expected_qty, значит
	// между вводом и проведением по позиции прошло движение (FR-13).
	CurrentQty qty.Qty `json:"current_qty"`
	// CountedQty — внесённый факт; пусто, пока позицию не пересчитали.
	CountedQty *qty.Qty `json:"counted_qty,omitempty"`
	// Diff — расхождение факта с текущим учётом.
	Diff *qty.Qty `json:"diff,omitempty"`
}

// CountChangedError сообщает, по каким позициям учёт изменился с момента ввода.
type CountChangedError struct {
	Items []CountLine
}

func (e *CountChangedError) Error() string {
	return fmt.Sprintf("stock: учёт изменился по %d позициям с момента ввода", len(e.Items))
}

func (e *CountChangedError) Unwrap() error { return ErrCountChanged }

// CreateCount открывает документ пересчёта и заполняет его строки текущим
// учётным остатком.
func (s *Service) CreateCount(ctx context.Context, t tenant.Tenant, scope CountScope, note string, itemIDs []uuid.UUID, by uuid.UUID) (Count, error) {
	var out Count

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		warehouse, err := q.GetDefaultWarehouse(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("stock: склад тенанта: %w", err)
		}

		// Пустой список означает «все живые позиции».
		if len(itemIDs) == 0 {
			itemIDs, err = q.ListActiveItemIDs(ctx, t.ID)
			if err != nil {
				return fmt.Errorf("stock: список позиций: %w", err)
			}
		}

		row, err := q.CreateStockCount(ctx, sqlc.CreateStockCountParams{
			ID:          uuid.Must(uuid.NewV7()),
			TenantID:    t.ID,
			WarehouseID: warehouse.ID,
			Scope:       string(scope),
			Note:        note,
			CreatedBy:   uuid.NullUUID{UUID: by, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("stock: создание пересчёта: %w", err)
		}

		balances, err := q.ListBalances(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("stock: остатки: %w", err)
		}
		onHand := make(map[uuid.UUID]qty.Qty, len(balances))
		for _, b := range balances {
			onHand[b.ItemID] = postgres.Qty(b.OnHand)
		}

		for _, itemID := range itemIDs {
			expected := onHand[itemID] // позиция без движений — ноль
			if err := q.UpsertStockCountLine(ctx, sqlc.UpsertStockCountLineParams{
				TenantID:    t.ID,
				CountID:     row.ID,
				ItemID:      itemID,
				ExpectedQty: postgres.DecimalOf(expected),
			}); err != nil {
				return fmt.Errorf("stock: строка пересчёта: %w", err)
			}
		}

		out = countFromRow(row)
		return nil
	})
	if err != nil {
		return Count{}, err
	}
	return s.GetCount(ctx, t, out.ID)
}

// SetCountLines вносит факт по позициям. Черновик можно сохранять сколько
// угодно раз: форма на телефоне заполняется в несколько заходов (FR-15).
func (s *Service) SetCountLines(ctx context.Context, t tenant.Tenant, countID uuid.UUID, lines map[uuid.UUID]qty.Qty) error {
	return s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		count, err := q.GetStockCount(ctx, sqlc.GetStockCountParams{TenantID: t.ID, ID: countID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrItemNotFound
			}
			return fmt.Errorf("stock: чтение пересчёта: %w", err)
		}
		if count.Status == sqlc.CountStatusPosted {
			return ErrCountPosted
		}

		existing, err := q.ListStockCountLines(ctx, sqlc.ListStockCountLinesParams{
			TenantID: t.ID, CountID: countID,
		})
		if err != nil {
			return fmt.Errorf("stock: строки пересчёта: %w", err)
		}
		expected := make(map[uuid.UUID]qty.Qty, len(existing))
		for _, l := range existing {
			expected[l.ItemID] = postgres.Qty(l.ExpectedQty)
		}

		for itemID, counted := range lines {
			if counted.IsNegative() {
				return fmt.Errorf("stock: отрицательный факт по позиции %s", itemID)
			}
			value := counted
			if err := q.UpsertStockCountLine(ctx, sqlc.UpsertStockCountLineParams{
				TenantID:    t.ID,
				CountID:     countID,
				ItemID:      itemID,
				ExpectedQty: postgres.DecimalOf(expected[itemID]),
				CountedQty:  postgres.NullDecimalOf(&value),
			}); err != nil {
				return fmt.Errorf("stock: сохранение строки: %w", err)
			}
		}
		return nil
	})
}

// PostCount проводит пересчёт: по каждой строке с внесённым фактом создаётся
// корректировка «факт − учёт на момент проведения» (FR-13).
//
// Если между вводом и проведением учёт изменился, проведение отклоняется,
// пока владелец не подтвердит его явно: иначе корректировка затрёт движение,
// про которое пересчитывавший не знал.
func (s *Service) PostCount(ctx context.Context, t tenant.Tenant, countID, by uuid.UUID, force bool) (Count, error) {
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		// Блокируем документ: два одновременных «провести» не должны
		// создать две пачки корректировок.
		count, err := q.GetStockCountForUpdate(ctx, sqlc.GetStockCountForUpdateParams{
			TenantID: t.ID, ID: countID,
		})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrItemNotFound
			}
			return fmt.Errorf("stock: блокировка пересчёта: %w", err)
		}
		if count.Status == sqlc.CountStatusPosted {
			return ErrCountPosted
		}

		lines, err := q.ListCountedLines(ctx, sqlc.ListCountedLinesParams{
			TenantID: t.ID, CountID: countID,
		})
		if err != nil {
			return fmt.Errorf("stock: строки пересчёта: %w", err)
		}

		// Время тенанта: корректировки пересчёта — такие же движения,
		// как остальные, и в песочнице их дата обязана совпадать с
		// виртуальным «сегодня».
		now := t.Now(s.clock)
		var changed []CountLine

		for _, line := range lines {
			counted := postgres.QtyOrZero(line.CountedQty)

			if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
				TenantID:    t.ID,
				WarehouseID: count.WarehouseID,
				ItemID:      line.ItemID,
			}); err != nil {
				return fmt.Errorf("stock: подготовка остатка: %w", err)
			}
			currentRaw, err := q.LockBalance(ctx, sqlc.LockBalanceParams{
				WarehouseID: count.WarehouseID,
				ItemID:      line.ItemID,
			})
			if err != nil {
				return fmt.Errorf("stock: блокировка остатка: %w", err)
			}
			current := postgres.Qty(currentRaw)
			expectedAtEntry := postgres.Qty(line.ExpectedQty)

			// Учёт разъехался с тем, что видел пересчитывавший.
			if !force && !current.Equal(expectedAtEntry) {
				countedCopy := counted
				changed = append(changed, CountLine{
					ItemID:      line.ItemID,
					ExpectedQty: expectedAtEntry,
					CurrentQty:  current,
					CountedQty:  &countedCopy,
				})
				continue
			}

			delta := counted.Sub(current)
			if delta.IsZero() {
				// Факт совпал с учётом — движения не нужно.
				continue
			}

			if _, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
				ID:          uuid.Must(uuid.NewV7()),
				TenantID:    t.ID,
				WarehouseID: count.WarehouseID,
				ItemID:      line.ItemID,
				Type:        sqlc.MovementTypeAdjustment,
				Qty:         postgres.DecimalOf(delta),
				OccurredAt:  postgres.Time(now),
				CreatedBy:   uuid.NullUUID{UUID: by, Valid: true},
				Comment:     "Корректировка по пересчёту",
				CountID:     uuid.NullUUID{UUID: countID, Valid: true},
			}); err != nil {
				return wrapInsertError(err)
			}

			if _, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
				WarehouseID: count.WarehouseID,
				ItemID:      line.ItemID,
				Delta:       postgres.DecimalOf(delta),
				Now:         postgres.Time(now),
			}); err != nil {
				return wrapBalanceError(err, current, delta.Abs())
			}

			if _, err := s.status.Recalculate(ctx, tx, t, line.ItemID); err != nil {
				return fmt.Errorf("stock: пересчёт статуса: %w", err)
			}
		}

		if len(changed) > 0 {
			// Транзакция откатится целиком: частично проведённого пересчёта
			// в системе быть не должно.
			return &CountChangedError{Items: changed}
		}

		return q.PostStockCount(ctx, sqlc.PostStockCountParams{
			TenantID: t.ID,
			ID:       countID,
			PostedAt: postgres.Time(now),
			PostedBy: uuid.NullUUID{UUID: by, Valid: true},
		})
	})
	if err != nil {
		return Count{}, err
	}
	return s.GetCount(ctx, t, countID)
}

// GetCount отдаёт документ со строками и расхождениями.
func (s *Service) GetCount(ctx context.Context, t tenant.Tenant, countID uuid.UUID) (Count, error) {
	var out Count

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		row, err := q.GetStockCount(ctx, sqlc.GetStockCountParams{TenantID: t.ID, ID: countID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrItemNotFound
			}
			return fmt.Errorf("stock: чтение пересчёта: %w", err)
		}
		out = countFromRow(row)

		lines, err := q.ListStockCountLines(ctx, sqlc.ListStockCountLinesParams{
			TenantID: t.ID, CountID: countID,
		})
		if err != nil {
			return fmt.Errorf("stock: строки пересчёта: %w", err)
		}
		for _, l := range lines {
			line := CountLine{
				ItemID:      l.ItemID,
				ItemName:    l.ItemName,
				BaseUnit:    l.BaseUnit,
				ExpectedQty: postgres.Qty(l.ExpectedQty),
				CurrentQty:  postgres.Qty(l.CurrentQty),
			}
			if counted, ok := postgres.NullQty(l.CountedQty); ok {
				diff := counted.Sub(line.CurrentQty)
				line.CountedQty = &counted
				line.Diff = &diff
			}
			out.Lines = append(out.Lines, line)
		}
		return nil
	})
	return out, err
}

// ListCounts отдаёт список пересчётов, свежие сверху.
func (s *Service) ListCounts(ctx context.Context, t tenant.Tenant, limit int) ([]Count, error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = DefaultPageSize
	}
	var out []Count
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListStockCounts(ctx, sqlc.ListStockCountsParams{
			TenantID: t.ID, Limit: int32(limit),
		})
		if err != nil {
			return fmt.Errorf("stock: список пересчётов: %w", err)
		}
		for _, row := range rows {
			out = append(out, countFromRow(row))
		}
		return nil
	})
	return out, err
}

func countFromRow(row sqlc.StockCount) Count {
	return Count{
		ID:        row.ID,
		Status:    CountStatus(row.Status),
		Scope:     CountScope(row.Scope),
		Note:      row.Note,
		CreatedBy: ptrUUID(row.CreatedBy),
		CreatedAt: postgres.TimeOrZero(row.CreatedAt),
		PostedAt:  postgres.TimePtr(row.PostedAt),
	}
}
