package forecast

import (
	"math"
	"slices"

	"github.com/vostapenko/zapas/internal/clock"
)

// point — очищенное наблюдение во внутреннем представлении.
type point struct {
	day clock.Day
	qty float64
}

// cleanSeries приводит сырой ряд к виду, на котором можно учить модель (§4.1):
// отбрасывает дни без данных и дни дефицита, сортирует по возрастанию,
// обрезает выбросы и отсекает всё, что не раньше asOf.
func cleanSeries(series []Observation, asOf clock.Day) []point {
	out := make([]point, 0, len(series))
	for _, obs := range series {
		switch {
		case !obs.HasData:
			// Нет данных — не ноль. День просто выпадает из ряда.
			continue
		case obs.Stockout:
			// Остаток доходил до нуля: спрос был выше учтённого.
			continue
		case !obs.Day.Before(asOf):
			// Будущее и сегодняшний день в обучение не идут.
			continue
		case obs.Qty.IsNegative():
			// Отрицательный расход означает ошибку сборки ряда; безопаснее
			// пропустить день, чем учить модель на минусе.
			continue
		}
		out = append(out, point{day: obs.Day, qty: obs.Qty.Float64()})
	}

	slices.SortFunc(out, func(a, b point) int {
		return a.day.Time(timeUTC).Compare(b.day.Time(timeUTC))
	})
	clipOutliers(out)
	return out
}

// clipOutliers обрезает значения выше Q3 + 3·IQR за последние 8 недель:
// разовое мероприятие не должно раздувать заказы на неделю вперёд (§4.1).
func clipOutliers(pts []point) {
	if len(pts) < 8 {
		return
	}
	window := pts
	if len(window) > OutlierWeeks*7 {
		window = window[len(window)-OutlierWeeks*7:]
	}

	values := make([]float64, len(window))
	for i, p := range window {
		values[i] = p.qty
	}
	slices.Sort(values)

	q1 := quantile(values, 0.25)
	q3 := quantile(values, 0.75)
	// На ровном ряде IQR равен нулю и порог сходится к Q3 — это и нужно:
	// среди полусотни «десяток» одна «двухсотка» и есть выброс.
	// Единственный случай, когда обрезать нельзя, — Q3 = 0 у позиции, которая
	// расходуется редко: иначе порог обнулил бы весь расход.
	if q3 <= 0 {
		return
	}
	threshold := q3 + 3*(q3-q1)

	for i := range window {
		if window[i].qty > threshold {
			window[i].qty = threshold
		}
	}
}

// quantile — линейная интерполяция по отсортированному срезу.
func quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
