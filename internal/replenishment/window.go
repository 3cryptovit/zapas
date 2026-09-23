// Package replenishment отвечает на один вопрос по каждой позиции: «если не
// заказать сегодня, хватит ли до поставки по следующему заказу?» (§5).
//
// Как и forecast, пакет состоит из чистых функций: ни БД, ни time.Now().
package replenishment

import (
	"time"

	"github.com/vostapenko/zapas/internal/clock"
)

// Supplier — условия поставки, влияющие на окна (§5.1).
type Supplier struct {
	LeadTimeDays int
	// DeliveryWeekdays — дни доставки в нумерации ISO: пн = 1 … вс = 7.
	DeliveryWeekdays []int
	Cutoff           clock.TimeOfDay
}

// Window — окна поставки на сегодня и на завтра.
type Window struct {
	// OrderDay — день, к которому относится «заказать сегодня». После отсечки
	// это уже завтра: сегодняшнюю заявку поставщик не примет.
	OrderDay clock.Day
	// D1 — дата поставки, если заказать сейчас.
	D1 clock.Day
	// D2 — дата поставки, если отложить заказ на день.
	D2 clock.Day
	// OrderBy — дедлайн отсечки, который видит владелец.
	OrderBy time.Time
	// AfterCutoff — отсечка на сегодня уже прошла.
	AfterCutoff bool
}

// CanWait сообщает, что откладывать заказ ничего не стоит: и сегодня, и завтра
// поставка придёт в один и тот же день, поэтому напоминания сегодня не будет.
func (w Window) CanWait() bool { return w.D1.Equal(w.D2) }

// ComputeWindow считает d1 и d2 для поставщика на момент now (§5.1).
func ComputeWindow(now time.Time, loc *time.Location, s Supplier) Window {
	if loc == nil {
		loc = time.UTC
	}
	today := clock.DayIn(now, loc)

	// После времени отсечки расчёт идёт от завтрашнего дня.
	orderDay := today
	afterCutoff := !s.Cutoff.Before(now, loc)
	if afterCutoff {
		orderDay = today.AddDays(1)
	}

	return Window{
		OrderDay:    orderDay,
		D1:          deliveryDate(orderDay, s),
		D2:          deliveryDate(orderDay.AddDays(1), s),
		OrderBy:     s.Cutoff.On(orderDay, loc),
		AfterCutoff: afterCutoff,
	}
}

// deliveryDate — ближайший день доставки не раньше «день заказа + срок поставки».
func deliveryDate(orderDay clock.Day, s Supplier) clock.Day {
	earliest := orderDay.AddDays(max(s.LeadTimeDays, 0))
	if len(s.DeliveryWeekdays) == 0 {
		// Поставщик возит в любой день.
		return earliest
	}
	allowed := weekdaySet(s.DeliveryWeekdays)
	if allowed == 0 {
		return earliest
	}
	// Не больше недели перебора: хотя бы один день недели точно разрешён.
	for i := 0; i < 7; i++ {
		day := earliest.AddDays(i)
		if allowed&(1<<day.Weekday()) != 0 {
			return day
		}
	}
	return earliest
}

func weekdaySet(weekdays []int) uint8 {
	var set uint8
	for _, wd := range weekdays {
		if wd >= 1 && wd <= 7 {
			set |= 1 << wd
		}
	}
	return set
}
