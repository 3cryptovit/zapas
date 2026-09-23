// Package catalog ведёт номенклатуру, категории и поставщиков (§3.1, §3.2).
package catalog

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// BaseUnit — базовая единица учёта. Закупка может идти в другой единице
// с коэффициентом (коробка = 12 л), но учёт всегда в базовой (FR-2).
type BaseUnit string

const (
	UnitKg    BaseUnit = "kg"
	UnitLiter BaseUnit = "l"
	UnitPcs   BaseUnit = "pcs"
)

// Valid сообщает, что единица известна.
func (u BaseUnit) Valid() bool { return u == UnitKg || u == UnitLiter || u == UnitPcs }

// Label — единица по-русски.
func (u BaseUnit) Label() string {
	switch u {
	case UnitKg:
		return "кг"
	case UnitLiter:
		return "л"
	case UnitPcs:
		return "шт"
	default:
		return string(u)
	}
}

// defaultCutoff — время отсечки у поставщика, заведённого импортом.
// Настоящее значение владелец укажет сам (FR-5).
var defaultCutoff = clock.TimeOfDay{Hour: 16}

// ServiceLevels — допустимые уровни сервиса (§5.2).
var ServiceLevels = []int{90, 95, 99}

// ValidServiceLevel сообщает, что уровень сервиса допустим.
func ValidServiceLevel(level int) bool {
	for _, l := range ServiceLevels {
		if l == level {
			return true
		}
	}
	return false
}

// Ошибки уровня сервиса.
var (
	ErrNotFound       = errors.New("catalog: не найдено")
	ErrNameTaken      = errors.New("catalog: такое название уже есть")
	ErrInvalidUnit    = errors.New("catalog: неизвестная единица измерения")
	ErrInvalidService = errors.New("catalog: недопустимый уровень сервиса")
	ErrBadWeekdays    = errors.New("catalog: некорректные дни доставки")
)

// Item — позиция номенклатуры.
type Item struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	BaseUnit     BaseUnit   `json:"base_unit"`
	UnitLabel    string     `json:"unit_label"`
	CategoryID   *uuid.UUID `json:"category_id,omitempty"`
	CategoryName string     `json:"category_name,omitempty"`
	SupplierID   *uuid.UUID `json:"default_supplier_id,omitempty"`
	SupplierName string     `json:"supplier_name,omitempty"`
	ServiceLevel int        `json:"service_level"`
	// ManualMinQty — ручной минимальный остаток: запасной вариант, пока
	// прогноза нет (модель M0).
	ManualMinQty qty.Qty    `json:"manual_min_qty"`
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
}

// Archived сообщает, что позиция в архиве.
func (i Item) Archived() bool { return i.ArchivedAt != nil }

// Category — плоский список категорий (FR-3).
type Category struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Supplier — поставщик с условиями поставки (FR-5).
type Supplier struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Contact      string    `json:"contact,omitempty"`
	LeadTimeDays int       `json:"lead_time_days"`
	// DeliveryWeekdays — дни доставки в нумерации ISO: пн = 1 … вс = 7.
	DeliveryWeekdays []int           `json:"delivery_weekdays"`
	OrderCutoff      clock.TimeOfDay `json:"order_cutoff"`
	ArchivedAt       *time.Time      `json:"archived_at,omitempty"`
}

// Terms — условия закупки позиции у поставщика (FR-6).
type Terms struct {
	SupplierID uuid.UUID `json:"supplier_id"`
	ItemID     uuid.UUID `json:"item_id"`
	// PurchaseUnit — единица закупки: «кор.», «уп.», «шт».
	PurchaseUnit string `json:"purchase_unit"`
	// UnitFactor — сколько базовых единиц в единице закупки: коробка = 12 л.
	UnitFactor qty.Qty `json:"unit_factor"`
	// MinOrderQty — минимальная партия в базовых единицах.
	MinOrderQty qty.Qty `json:"min_order_qty"`
	// PackMultiple — кратность упаковки: заказ округляется вверх до неё (§5.2).
	PackMultiple qty.Qty  `json:"pack_multiple"`
	Price        *qty.Qty `json:"price,omitempty"`
}

// InPurchaseUnits переводит количество из базовых единиц в единицы закупки:
// 24 л при коэффициенте 12 — это 2 коробки.
func (t Terms) InPurchaseUnits(base qty.Qty) qty.Qty {
	if !t.UnitFactor.IsPositive() {
		return base
	}
	return qty.FromDecimal(base.Decimal().Div(t.UnitFactor.Decimal()))
}

// ValidWeekdays проверяет дни доставки: список непустой, без выхода за 1..7.
func ValidWeekdays(days []int) bool {
	if len(days) == 0 || len(days) > 7 {
		return false
	}
	seen := map[int]bool{}
	for _, d := range days {
		if d < 1 || d > 7 || seen[d] {
			return false
		}
		seen[d] = true
	}
	return true
}
