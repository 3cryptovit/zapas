// Package orders ведёт заказы поставщику и приёмку (§5.4).
//
// Система готовит и отслеживает заказ, а отправляет его владелец сам:
// текст заявки копируется в мессенджер или почту (§1, допущения).
package orders

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Status — статус заказа (§5.4).
type Status string

const (
	StatusDraft     Status = "draft"     // черновик из «Заказать сегодня»
	StatusSent      Status = "sent"      // отправлен, количество считается «в пути»
	StatusReceived  Status = "received"  // принят, движения прихода созданы
	StatusCancelled Status = "cancelled" // отменён
)

// Label — статус по-русски.
func (s Status) Label() string {
	switch s {
	case StatusDraft:
		return "Черновик"
	case StatusSent:
		return "Отправлен"
	case StatusReceived:
		return "Принят"
	case StatusCancelled:
		return "Отменён"
	default:
		return string(s)
	}
}

// Ошибки уровня сервиса.
var (
	ErrNotFound        = errors.New("orders: заказ не найден")
	ErrEmptyOrder      = errors.New("orders: в заказе нет строк")
	ErrWrongStatus     = errors.New("orders: действие недоступно в текущем статусе")
	ErrAlreadySent     = errors.New("orders: заказ уже отправлен")
	ErrAlreadyReceived = errors.New("orders: заказ уже принят")
	ErrNoSupplier      = errors.New("orders: у позиции нет поставщика")
)

// Order — заказ поставщику.
type Order struct {
	ID           uuid.UUID `json:"id"`
	SupplierID   uuid.UUID `json:"supplier_id"`
	SupplierName string    `json:"supplier_name,omitempty"`
	Status       Status    `json:"status"`
	StatusLabel  string    `json:"status_label"`
	// ExpectedAt — дата поставки d1, зафиксированная при отправке (§5.1).
	ExpectedAt  clock.Day  `json:"expected_at"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	ReceivedAt  *time.Time `json:"received_at,omitempty"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`
	Note        string     `json:"note,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Lines       []Line     `json:"lines,omitempty"`
	// Text — готовая заявка поставщику с кнопкой «Скопировать» (FR-19).
	Text string `json:"text,omitempty"`
	// Late — к концу ожидаемого дня заказ не принят (FR-21).
	Late bool `json:"late"`
}

// Line — строка заказа. Количества хранятся в базовой единице,
// единица закупки — представление для владельца.
type Line struct {
	ItemID   uuid.UUID `json:"item_id"`
	ItemName string    `json:"item_name"`
	BaseUnit string    `json:"base_unit"`
	// QtyOrdered — заказано в базовой единице.
	QtyOrdered qty.Qty `json:"qty_ordered"`
	// QtyReceived — фактически принято; пусто до приёмки.
	QtyReceived *qty.Qty `json:"qty_received,omitempty"`
	// PurchaseUnit и UnitFactor фиксируются в момент создания заказа:
	// поставщик может потом поменять условия, а заказ должен остаться
	// таким, каким его отправили.
	PurchaseUnit string   `json:"purchase_unit,omitempty"`
	UnitFactor   qty.Qty  `json:"unit_factor"`
	Price        *qty.Qty `json:"price,omitempty"`
}

// InPurchaseUnits переводит количество в единицы закупки: 24 л → 2 коробки.
func (l Line) InPurchaseUnits(base qty.Qty) qty.Qty {
	if !l.UnitFactor.IsPositive() {
		return base
	}
	return qty.FromDecimal(base.Decimal().Div(l.UnitFactor.Decimal()))
}

// Mismatch — расхождение факта с заказом по строке.
func (l Line) Mismatch() qty.Qty {
	if l.QtyReceived == nil {
		return qty.Zero()
	}
	return l.QtyReceived.Sub(l.QtyOrdered)
}

// MismatchThreshold — порог, после которого расхождение при приёмке
// попадает в ленту (§5.5).
const MismatchThreshold = 0.05

// HasSignificantMismatch сообщает, что факт отличается от заказа больше
// чем на 5%.
func (o Order) HasSignificantMismatch() bool {
	ordered := qty.Zero()
	received := qty.Zero()
	for _, l := range o.Lines {
		ordered = ordered.Add(l.QtyOrdered)
		if l.QtyReceived != nil {
			received = received.Add(*l.QtyReceived)
		}
	}
	if !ordered.IsPositive() {
		return false
	}
	diff := received.Sub(ordered).Abs().Float64() / ordered.Float64()
	return diff > MismatchThreshold
}
