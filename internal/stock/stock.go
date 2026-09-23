// Package stock ведёт движения, остатки и инвентаризацию (§3).
//
// Главное правило: остаток никогда не редактируется напрямую. Он всегда равен
// сумме неизменяемых движений, и любую цифру можно объяснить по журналу
// (ADR-002).
package stock

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Type — тип движения (§3.3).
type Type string

const (
	// TypeOpening — начальный остаток при импорте. В расход не идёт.
	TypeOpening Type = "opening"
	// TypeReceipt — приход: приёмка заказа или закупка без заказа.
	TypeReceipt Type = "receipt"
	// TypeUsage — расход за смену. Идёт в прогноз.
	TypeUsage Type = "usage"
	// TypeWriteoff — списание: порча, бой, просрочка. Причина обязательна.
	TypeWriteoff Type = "writeoff"
	// TypeAdjustment — корректировка при проведении пересчёта.
	TypeAdjustment Type = "adjustment"
	// TypeReversal — сторно ошибочного движения.
	TypeReversal Type = "reversal"
)

// Valid сообщает, что тип известен.
func (t Type) Valid() bool {
	switch t {
	case TypeOpening, TypeReceipt, TypeUsage, TypeWriteoff, TypeAdjustment, TypeReversal:
		return true
	default:
		return false
	}
}

// UserCreatable сообщает, что движение этого типа можно создать через
// POST /movements. Корректировки рождаются при проведении пересчёта,
// сторно — через отдельный эндпоинт, начальный остаток — при импорте.
func (t Type) UserCreatable() bool {
	return t == TypeReceipt || t == TypeUsage || t == TypeWriteoff
}

// Increases сообщает, что движение увеличивает остаток.
func (t Type) Increases() bool { return t == TypeOpening || t == TypeReceipt }

// Decreases сообщает, что движение уменьшает остаток.
func (t Type) Decreases() bool { return t == TypeUsage || t == TypeWriteoff }

// CountsAsConsumption сообщает, что движение идёт в расход для прогноза (§3.3).
func (t Type) CountsAsConsumption() bool {
	return t == TypeUsage || t == TypeWriteoff || t == TypeAdjustment
}

// Label — название типа для журнала и интерфейса.
func (t Type) Label() string {
	switch t {
	case TypeOpening:
		return "Начальный остаток"
	case TypeReceipt:
		return "Приход"
	case TypeUsage:
		return "Расход"
	case TypeWriteoff:
		return "Списание"
	case TypeAdjustment:
		return "Корректировка"
	case TypeReversal:
		return "Сторно"
	default:
		return string(t)
	}
}

// Reason — причина списания (FR-6).
type Reason string

const (
	ReasonSpoiled Reason = "spoiled" // испортилось
	ReasonBroken  Reason = "broken"  // бой
	ReasonExpired Reason = "expired" // истёк срок
	ReasonTasting Reason = "tasting" // проливы, дегустации
	ReasonOther   Reason = "other"
)

// ValidReason сообщает, что причина известна.
func ValidReason(r string) bool {
	switch Reason(r) {
	case ReasonSpoiled, ReasonBroken, ReasonExpired, ReasonTasting, ReasonOther:
		return true
	default:
		return false
	}
}

// ReasonLabel — название причины для интерфейса.
func ReasonLabel(r string) string {
	switch Reason(r) {
	case ReasonSpoiled:
		return "Испортилось"
	case ReasonBroken:
		return "Бой"
	case ReasonExpired:
		return "Истёк срок"
	case ReasonTasting:
		return "Проливы и дегустации"
	case ReasonOther:
		return "Другое"
	default:
		return r
	}
}

// MaxBackdateDays — насколько далеко назад можно поставить время события
// (FR-9). Дальше — только через пересчёт: иначе задним числом переписывается
// история, на которой уже построен прогноз.
const MaxBackdateDays = 7

// Ошибки уровня сервиса.
var (
	// ErrInsufficientStock — расход сверх остатка. Наружу уходит 409
	// с текущим остатком (FR-8).
	ErrInsufficientStock = errors.New("stock: недостаточно остатка")
	ErrItemNotFound      = errors.New("stock: позиция не найдена")
	ErrItemArchived      = errors.New("stock: позиция в архиве")
	ErrAlreadyReversed   = errors.New("stock: движение уже сторнировано")
	ErrCannotReverse     = errors.New("stock: это движение нельзя сторнировать")
	ErrOccurredTooOld    = fmt.Errorf("stock: время события дальше %d дней назад", MaxBackdateDays)
	ErrOccurredInFuture  = errors.New("stock: время события в будущем")
	ErrCountPosted       = errors.New("stock: пересчёт уже проведён")
	ErrCountChanged      = errors.New("stock: учёт изменился с момента ввода")
)

// InsufficientStockError несёт текущий остаток: интерфейс показывает его
// в сообщении «На складе 0,8 л, списать 1,5 л нельзя».
type InsufficientStockError struct {
	OnHand    qty.Qty
	Requested qty.Qty
}

func (e *InsufficientStockError) Error() string {
	return fmt.Sprintf("stock: на складе %s, списать %s нельзя", e.OnHand, e.Requested)
}

func (e *InsufficientStockError) Unwrap() error { return ErrInsufficientStock }

// Movement — движение в журнале.
type Movement struct {
	ID          uuid.UUID  `json:"id"`
	ItemID      uuid.UUID  `json:"item_id"`
	ItemName    string     `json:"item_name,omitempty"`
	BaseUnit    string     `json:"base_unit,omitempty"`
	Type        Type       `json:"type"`
	TypeLabel   string     `json:"type_label"`
	Qty         qty.Qty    `json:"qty"`
	OccurredAt  time.Time  `json:"occurred_at"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	AuthorName  string     `json:"author_name,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	ReasonLabel string     `json:"reason_label,omitempty"`
	Comment     string     `json:"comment,omitempty"`
	OrderID     *uuid.UUID `json:"order_id,omitempty"`
	CountID     *uuid.UUID `json:"count_id,omitempty"`
	ReversesID  *uuid.UUID `json:"reverses_id,omitempty"`
	// Reversed — движение уже сторновано, повторно нельзя (FR-7).
	Reversed bool `json:"reversed"`
}

// Balance — остаток позиции (FR-16).
type Balance struct {
	ItemID uuid.UUID `json:"item_id"`
	OnHand qty.Qty   `json:"on_hand"`
	// OnOrder — в пути по отправленным заказам.
	OnOrder   qty.Qty   `json:"on_order"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InventoryPosition — позиция запаса: on_hand + on_order (§5.2).
func (b Balance) InventoryPosition() qty.Qty { return b.OnHand.Add(b.OnOrder) }

// signedQty расставляет знак по типу движения. Наружу количество всегда
// передаётся положительным, знак ставит сервер (§11).
func signedQty(t Type, q qty.Qty) qty.Qty {
	switch {
	case t.Decreases():
		return q.Abs().Neg()
	case t.Increases():
		return q.Abs()
	default:
		// adjustment и reversal приходят уже со знаком.
		return q
	}
}
