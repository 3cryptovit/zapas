package stock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// StatusRecalculator пересчитывает статус позиции после движения.
//
// Интерфейс, а не прямой вызов replenishment: модуль берёт чужую логику через
// границу модуля (CONVENTIONS.md). Заодно это развязывает тесты склада от прогноза.
type StatusRecalculator interface {
	// Recalculate вызывается внутри той же транзакции, что и движение:
	// расчёт дешёвый (14 чисел прогноза на позицию), поэтому идёт
	// синхронно (§5.3).
	Recalculate(ctx context.Context, tx postgres.Tx, t tenant.Tenant, itemID uuid.UUID) (StatusSnapshot, error)
}

// StatusSnapshot — то, что интерфейс показывает сразу после движения,
// без второго запроса (§11).
type StatusSnapshot struct {
	Code           string  `json:"code"`
	StockoutDate   *string `json:"stockout_date,omitempty"`
	OrderBy        *string `json:"order_by,omitempty"`
	RecommendedQty qty.Qty `json:"recommended_qty"`
}

// noopRecalculator используется, пока модуль пополнения не подключён:
// склад обязан работать и без прогноза.
type noopRecalculator struct{}

func (noopRecalculator) Recalculate(context.Context, postgres.Tx, tenant.Tenant, uuid.UUID) (StatusSnapshot, error) {
	return StatusSnapshot{Code: "no_forecast"}, nil
}

// Service — операции склада.
type Service struct {
	db     *postgres.DB
	clock  clock.Clock
	status StatusRecalculator
}

func NewService(db *postgres.DB, cl clock.Clock, status StatusRecalculator) *Service {
	if status == nil {
		status = noopRecalculator{}
	}
	return &Service{db: db, clock: cl, status: status}
}

// RecordRequest — заявка на движение.
type RecordRequest struct {
	ItemID uuid.UUID
	Type   Type
	// Qty — положительное количество; знак ставит сервер по типу.
	Qty        qty.Qty
	OccurredAt time.Time
	Reason     string
	Comment    string
	OrderID    *uuid.UUID
	CountID    *uuid.UUID
	// IdempotencyKey обязателен для движений (§11): повтор формы с плохой
	// связи не должен создавать второе списание.
	IdempotencyKey string
	CreatedBy      *uuid.UUID
}

// RecordResult — ответ 201: движение, новый остаток и статус сразу.
type RecordResult struct {
	Movement Movement
	Balance  Balance
	Status   StatusSnapshot
	// Duplicate: запрос повторился с тем же ключом, новое движение не создано.
	Duplicate bool
}

// Record записывает движение по порядку из §10.2.
//
//	повтор по ключу → блокировка остатка → проверка на минус →
//	вставка движения → изменение остатка → пересчёт статуса
//
// Всё в одной транзакции: остаток и журнал не могут разойтись даже при падении
// процесса посередине.
func (s *Service) Record(ctx context.Context, t tenant.Tenant, req RecordRequest) (RecordResult, error) {
	if err := s.validate(t, &req); err != nil {
		return RecordResult{}, err
	}

	var out RecordResult
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		res, err := s.recordTx(ctx, tx, t, req)
		if err != nil {
			return err
		}
		out = res
		return nil
	})
	if err != nil {
		return RecordResult{}, err
	}
	return out, nil
}

// RecordInTx записывает движение внутри уже открытой транзакции.
//
// Нужен приёмке заказа: приход по всем строкам, смена статуса заказа
// и пересчёт статусов обязаны быть одной транзакцией (FR-20).
func (s *Service) RecordInTx(ctx context.Context, tx postgres.Tx, t tenant.Tenant, req RecordRequest) (RecordResult, error) {
	if err := s.validate(t, &req); err != nil {
		return RecordResult{}, err
	}
	return s.recordTx(ctx, tx, t, req)
}

func (s *Service) recordTx(ctx context.Context, tx postgres.Tx, t tenant.Tenant, req RecordRequest) (RecordResult, error) {
	var out RecordResult

	err := func() error {
		q := sqlc.New(tx)

		// 1. Повтор с тем же ключом отдаёт уже созданное движение.
		if req.IdempotencyKey != "" {
			existing, err := q.GetMovementByIdempotencyKey(ctx, sqlc.GetMovementByIdempotencyKeyParams{
				TenantID:       t.ID,
				IdempotencyKey: &req.IdempotencyKey,
			})
			if err == nil {
				out.Movement = movementFromRow(existing)
				out.Duplicate = true
				balance, err := s.readBalance(ctx, q, t.ID, existing.ItemID)
				if err != nil {
					return err
				}
				out.Balance = balance
				return nil
			}
			if !postgres.IsNoRows(err) {
				return fmt.Errorf("stock: проверка ключа идемпотентности: %w", err)
			}
		}

		item, err := q.GetItem(ctx, sqlc.GetItemParams{TenantID: t.ID, ID: req.ItemID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrItemNotFound
			}
			return fmt.Errorf("stock: чтение позиции: %w", err)
		}
		if item.ArchivedAt.Valid {
			return ErrItemArchived
		}

		warehouse, err := q.GetDefaultWarehouse(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("stock: склад тенанта: %w", err)
		}

		delta := signedQty(req.Type, req.Qty)

		// 2. Блокируем строку остатка: конкурентные списания одной позиции
		// идут по очереди, поэтому проверка ниже честная.
		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID:    t.ID,
			WarehouseID: warehouse.ID,
			ItemID:      req.ItemID,
		}); err != nil {
			return fmt.Errorf("stock: подготовка остатка: %w", err)
		}
		onHandRaw, err := q.LockBalance(ctx, sqlc.LockBalanceParams{
			WarehouseID: warehouse.ID,
			ItemID:      req.ItemID,
		})
		if err != nil {
			return fmt.Errorf("stock: блокировка остатка: %w", err)
		}
		onHand := postgres.Qty(onHandRaw)

		// 3. В минус не уходим: правильный путь в такой ситуации — пересчёт.
		if onHand.Add(delta).IsNegative() {
			return &InsufficientStockError{OnHand: onHand, Requested: req.Qty.Abs()}
		}

		// 4. Вставляем движение.
		row, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
			ID:             uuid.Must(uuid.NewV7()),
			TenantID:       t.ID,
			WarehouseID:    warehouse.ID,
			ItemID:         req.ItemID,
			Type:           sqlc.MovementType(req.Type),
			Qty:            postgres.DecimalOf(delta),
			OccurredAt:     postgres.Time(req.OccurredAt),
			CreatedBy:      nullUUID(req.CreatedBy),
			Reason:         nullString(req.Reason),
			Comment:        req.Comment,
			OrderID:        nullUUID(req.OrderID),
			CountID:        nullUUID(req.CountID),
			ReversesID:     uuid.NullUUID{},
			IdempotencyKey: nullString(req.IdempotencyKey),
		})
		if err != nil {
			return wrapInsertError(err)
		}

		// 5. Остаток меняется в той же транзакции.
		newOnHand, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
			WarehouseID: warehouse.ID,
			ItemID:      req.ItemID,
			Delta:       postgres.DecimalOf(delta),
			Now:         postgres.Time(s.clock.Now()),
		})
		if err != nil {
			return wrapBalanceError(err, onHand, req.Qty)
		}

		// 6. Статус пересчитывается сразу: дашборд и карточка должны
		// обновиться без второго запроса.
		status, err := s.status.Recalculate(ctx, tx, t, req.ItemID)
		if err != nil {
			return fmt.Errorf("stock: пересчёт статуса: %w", err)
		}

		out.Movement = movementFromRow(row)
		out.Movement.ItemName = item.Name
		out.Movement.BaseUnit = item.BaseUnit
		out.Balance = Balance{
			ItemID:    req.ItemID,
			OnHand:    postgres.Qty(newOnHand),
			UpdatedAt: s.clock.Now(),
		}
		out.Status = status
		return nil
	}()
	if err != nil {
		return RecordResult{}, err
	}
	return out, nil
}

// Reverse отменяет ошибочное движение (FR-7).
//
// Само движение не меняется: создаётся обратное со ссылкой на исходное.
// Сторнировать можно один раз — это гарантирует уникальный индекс.
func (s *Service) Reverse(ctx context.Context, t tenant.Tenant, movementID uuid.UUID, by uuid.UUID, canReverseOthers bool) (RecordResult, error) {
	var out RecordResult

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		original, err := q.GetMovement(ctx, sqlc.GetMovementParams{TenantID: t.ID, ID: movementID})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrItemNotFound
			}
			return fmt.Errorf("stock: чтение движения: %w", err)
		}

		// Сторно сторна не бывает: если ошиблись дважды, создаётся новое
		// правильное движение.
		if original.Type == sqlc.MovementTypeReversal {
			return ErrCannotReverse
		}
		// Сотрудник сторнирует только свои движения (§2).
		if !canReverseOthers {
			if !original.CreatedBy.Valid || original.CreatedBy.UUID != by {
				return ErrCannotReverse
			}
		}
		if _, err := q.GetReversalOf(ctx, sqlc.GetReversalOfParams{
			TenantID:   t.ID,
			ReversesID: uuid.NullUUID{UUID: movementID, Valid: true},
		}); err == nil {
			return ErrAlreadyReversed
		} else if !postgres.IsNoRows(err) {
			return fmt.Errorf("stock: проверка сторно: %w", err)
		}

		// Обратный знак: сторно прихода уменьшает остаток и наоборот.
		delta := postgres.Qty(original.Qty).Neg()

		if err := q.EnsureBalance(ctx, sqlc.EnsureBalanceParams{
			TenantID:    t.ID,
			WarehouseID: original.WarehouseID,
			ItemID:      original.ItemID,
		}); err != nil {
			return fmt.Errorf("stock: подготовка остатка: %w", err)
		}
		onHandRaw, err := q.LockBalance(ctx, sqlc.LockBalanceParams{
			WarehouseID: original.WarehouseID,
			ItemID:      original.ItemID,
		})
		if err != nil {
			return fmt.Errorf("stock: блокировка остатка: %w", err)
		}
		onHand := postgres.Qty(onHandRaw)

		// Сторно прихода, который уже израсходован, увело бы остаток в минус.
		if onHand.Add(delta).IsNegative() {
			return &InsufficientStockError{OnHand: onHand, Requested: delta.Abs()}
		}

		now := s.clock.Now()
		row, err := q.InsertMovement(ctx, sqlc.InsertMovementParams{
			ID:          uuid.Must(uuid.NewV7()),
			TenantID:    t.ID,
			WarehouseID: original.WarehouseID,
			ItemID:      original.ItemID,
			Type:        sqlc.MovementTypeReversal,
			Qty:         postgres.DecimalOf(delta),
			OccurredAt:  postgres.Time(now),
			CreatedBy:   uuid.NullUUID{UUID: by, Valid: true},
			Reason:      original.Reason,
			Comment:     "Сторно движения от " + postgres.TimeOrZero(original.OccurredAt).Format("02.01.2006 15:04"),
			ReversesID:  uuid.NullUUID{UUID: movementID, Valid: true},
		})
		if err != nil {
			return wrapInsertError(err)
		}

		newOnHand, err := q.ApplyMovementToBalance(ctx, sqlc.ApplyMovementToBalanceParams{
			WarehouseID: original.WarehouseID,
			ItemID:      original.ItemID,
			Delta:       postgres.DecimalOf(delta),
			Now:         postgres.Time(now),
		})
		if err != nil {
			return wrapBalanceError(err, onHand, delta.Abs())
		}

		status, err := s.status.Recalculate(ctx, tx, t, original.ItemID)
		if err != nil {
			return fmt.Errorf("stock: пересчёт статуса: %w", err)
		}

		out.Movement = movementFromRow(row)
		out.Balance = Balance{ItemID: original.ItemID, OnHand: postgres.Qty(newOnHand), UpdatedAt: now}
		out.Status = status
		return nil
	})
	if err != nil {
		return RecordResult{}, err
	}
	return out, nil
}

func (s *Service) validate(t tenant.Tenant, req *RecordRequest) error {
	if !req.Type.Valid() {
		return fmt.Errorf("stock: неизвестный тип движения %q", req.Type)
	}
	if req.Qty.IsZero() {
		return errors.New("stock: количество не может быть нулевым")
	}
	if req.Type == TypeWriteoff && !ValidReason(req.Reason) {
		return errors.New("stock: у списания должна быть причина")
	}

	now := t.Now(s.clock)
	if req.OccurredAt.IsZero() {
		req.OccurredAt = now
	}
	// Небольшой запас вперёд: часы телефона могут немного спешить.
	if req.OccurredAt.After(now.Add(5 * time.Minute)) {
		return ErrOccurredInFuture
	}
	if req.OccurredAt.Before(now.AddDate(0, 0, -MaxBackdateDays)) {
		return ErrOccurredTooOld
	}
	return nil
}

func (s *Service) readBalance(ctx context.Context, q *sqlc.Queries, tenantID, itemID uuid.UUID) (Balance, error) {
	row, err := q.GetBalance(ctx, sqlc.GetBalanceParams{TenantID: tenantID, ItemID: itemID})
	if err != nil {
		if postgres.IsNoRows(err) {
			return Balance{ItemID: itemID}, nil
		}
		return Balance{}, fmt.Errorf("stock: чтение остатка: %w", err)
	}
	return Balance{
		ItemID:    row.ItemID,
		OnHand:    postgres.Qty(row.OnHand),
		UpdatedAt: postgres.TimeOrZero(row.UpdatedAt),
	}, nil
}

// wrapInsertError переводит нарушения ограничений БД в доменные ошибки.
func wrapInsertError(err error) error {
	switch postgres.ConstraintName(err) {
	case "stock_movements_idempotency_idx":
		// Гонка двух одинаковых запросов: второй проиграл на уникальном
		// индексе. Для клиента это тот же повтор.
		return ErrDuplicateKey
	case "stock_movements_reverses_idx":
		return ErrAlreadyReversed
	case "stock_movements_writeoff_reason":
		return errors.New("stock: у списания должна быть причина")
	case "stock_movements_sign":
		return errors.New("stock: знак количества не соответствует типу движения")
	default:
		return fmt.Errorf("stock: запись движения: %w", err)
	}
}

// wrapBalanceError ловит последний рубеж — CHECK (on_hand >= 0).
func wrapBalanceError(err error, onHand, requested qty.Qty) error {
	if postgres.IsCode(err, postgres.CodeCheckViolation) {
		return &InsufficientStockError{OnHand: onHand, Requested: requested}
	}
	return fmt.Errorf("stock: изменение остатка: %w", err)
}

// ErrDuplicateKey — тот же ключ идемпотентности пришёл одновременно дважды.
var ErrDuplicateKey = errors.New("stock: повтор ключа идемпотентности")

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
