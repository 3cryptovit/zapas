// Package pipeline собирает ряд дневного расхода и запускает ночной пересчёт
// прогноза (§4.1, §4.4).
//
// Живёт отдельно от forecast, чтобы тот остался пакетом чистых функций:
// здесь есть и БД, и часы.
package pipeline

import (
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// rawMovement — движение расхода до сборки в ряд.
type rawMovement struct {
	ItemID uuid.UUID
	Day    clock.Day
	// Qty хранится со знаком журнала: расход отрицательный.
	Qty qty.Qty
	// FromCount: движение — корректировка по пересчёту. Такой расход
	// размазывается по интервалу с прошлого пересчёта.
	FromCount bool
}

// buildSeries превращает движения в дневной ряд расхода по каждой позиции.
//
// Правила очистки из §4.1 применяются здесь и в forecast.Compute:
//   - расход = −(usage + writeoff + adjustment);
//   - день без движений и без пересчёта исключается, а не считается нулём;
//   - расход между двумя пересчётами распределяется поровну по дням интервала;
//   - дни дефицита помечаются и в обучение не идут.
func buildSeries(
	movements []rawMovement,
	daysWithData map[clock.Day]bool,
	stockoutDays map[itemDay]bool,
	from, to clock.Day,
) map[uuid.UUID][]forecast.Observation {
	byItem := map[uuid.UUID][]rawMovement{}
	for _, m := range movements {
		byItem[m.ItemID] = append(byItem[m.ItemID], m)
	}

	out := make(map[uuid.UUID][]forecast.Observation, len(byItem))
	for itemID, items := range byItem {
		daily := distribute(items, from)

		observations := make([]forecast.Observation, 0, to.Sub(from)+1)
		for d := from; !d.After(to); d = d.AddDays(1) {
			observations = append(observations, forecast.Observation{
				Day:      d,
				Qty:      daily[d],
				HasData:  daysWithData[d],
				Stockout: stockoutDays[itemDay{itemID, d}],
			})
		}
		out[itemID] = observations
	}
	return out
}

type itemDay struct {
	ItemID uuid.UUID
	Day    clock.Day
}

// distribute раскладывает движения позиции по дням.
//
// Обычный расход ложится в свой день. Корректировка по пересчёту — это расход
// за весь интервал с прошлого пересчёта, поэтому она делится поровну по дням
// этого интервала: в режиме «только пересчёт» иначе получился бы один день
// с огромным расходом и неделя нулей.
func distribute(movements []rawMovement, from clock.Day) map[clock.Day]qty.Qty {
	daily := map[clock.Day]qty.Qty{}

	// Предыдущий пересчёт — граница интервала распределения.
	prevCount := clock.Day{}

	for _, m := range movements {
		// Расход хранится отрицательным, в ряд идёт положительным.
		consumed := m.Qty.Neg()

		if !m.FromCount {
			daily[m.Day] = daily[m.Day].Add(consumed)
			continue
		}

		start := prevCount.AddDays(1)
		if prevCount.IsZero() || start.Before(from) {
			start = from
		}
		prevCount = m.Day

		// Корректировка «в плюс» означает, что нашлось больше учтённого:
		// это не расход, в ряд она идёт как есть, в свой день.
		if consumed.IsNegative() || start.After(m.Day) {
			daily[m.Day] = daily[m.Day].Add(consumed)
			continue
		}

		days := m.Day.Sub(start) + 1
		if days <= 1 {
			daily[m.Day] = daily[m.Day].Add(consumed)
			continue
		}

		// Делим поровну, остаток от округления отдаём последнему дню,
		// чтобы сумма по интервалу совпала с исходной цифрой.
		share := consumed.MulFloat(1 / float64(days))
		spread := qty.Zero()
		for d := start; d.Before(m.Day); d = d.AddDays(1) {
			daily[d] = daily[d].Add(share)
			spread = spread.Add(share)
		}
		daily[m.Day] = daily[m.Day].Add(consumed.Sub(spread))
	}
	return daily
}
