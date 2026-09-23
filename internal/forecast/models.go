package forecast

import (
	"github.com/vostapenko/zapas/internal/clock"
)

// predictor — обученная модель: умеет назвать ожидаемый расход на любой день.
type predictor interface {
	Predict(day clock.Day) float64
}

// fitter обучает модель на очищенном ряде.
type fitter func(pts []point) predictor

// --- M1: среднее по дню недели ---

type m1 struct {
	byWeekday [8]float64 // индекс 1..7, ISO
	fallback  float64
	known     [8]bool
}

// fitM1 берёт среднее по последним четырём одноимённым дням: понедельник
// предсказывается по понедельникам (§4.2).
func fitM1(pts []point) predictor {
	const lookback = 4

	m := &m1{}
	all := make([]float64, 0, len(pts))
	for _, p := range pts {
		all = append(all, p.qty)
	}
	m.fallback = mean(all)

	for wd := 1; wd <= 7; wd++ {
		recent := make([]float64, 0, lookback)
		// Идём от свежих дней к старым и берём не больше четырёх.
		for i := len(pts) - 1; i >= 0 && len(recent) < lookback; i-- {
			if pts[i].day.Weekday() == wd {
				recent = append(recent, pts[i].qty)
			}
		}
		if len(recent) > 0 {
			m.byWeekday[wd] = mean(recent)
			m.known[wd] = true
		}
	}
	return m
}

func (m *m1) Predict(day clock.Day) float64 {
	wd := day.Weekday()
	if m.known[wd] {
		return m.byWeekday[wd]
	}
	// Этого дня недели в истории ещё не было — берём общее среднее.
	return m.fallback
}

// --- M2: экспоненциальное сглаживание с сезонностью ---

type m2 struct {
	level    float64
	seasonal [8]float64
}

// fitM2 возвращает обучатель с фиксированным α: сам α подбирается перебором
// снаружи, по бэктесту.
func fitM2(alpha float64) fitter {
	return func(pts []point) predictor {
		m := &m2{}
		m.seasonal = seasonalIndex(pts)

		// Уровень стартует со среднего за первую неделю без сезонности:
		// так рекурсия не зависит от случайного значения первого дня.
		warmup := pts
		if len(warmup) > 7 {
			warmup = warmup[:7]
		}
		deseasonalized := make([]float64, 0, len(warmup))
		for _, p := range warmup {
			deseasonalized = append(deseasonalized, p.qty/m.seasonal[p.day.Weekday()])
		}
		m.level = mean(deseasonalized)

		// L_t = α·(c_t / s_w(t)) + (1−α)·L_{t−1}
		for _, p := range pts {
			m.level = alpha*(p.qty/m.seasonal[p.day.Weekday()]) + (1-alpha)*m.level
		}
		return m
	}
}

// F_d = L_T · s_w(d)
func (m *m2) Predict(day clock.Day) float64 {
	v := m.level * m.seasonal[day.Weekday()]
	if v < 0 {
		return 0
	}
	return v
}

// seasonalIndex считает s_w = среднее по дню недели / общее среднее
// по последним 8 неделям (§4.2). Индекс 1.0 означает «обычный день».
func seasonalIndex(pts []point) [8]float64 {
	idx := [8]float64{}
	for i := range idx {
		idx[i] = 1
	}

	window := pts
	if len(window) > SeasonWeeks*7 {
		window = window[len(window)-SeasonWeeks*7:]
	}

	all := make([]float64, 0, len(window))
	byWeekday := make([][]float64, 8)
	for _, p := range window {
		all = append(all, p.qty)
		wd := p.day.Weekday()
		byWeekday[wd] = append(byWeekday[wd], p.qty)
	}

	overall := mean(all)
	if overall <= 0 {
		// Расхода не было вовсе — сезонности тоже нет.
		return idx
	}

	for wd := 1; wd <= 7; wd++ {
		if len(byWeekday[wd]) == 0 {
			continue
		}
		s := mean(byWeekday[wd]) / overall
		// Защита от деления на ноль в рекурсии уровня: полностью пустой день
		// недели не должен обнулять сглаживание.
		if s < 0.05 {
			s = 0.05
		}
		idx[wd] = s
	}
	return idx
}
