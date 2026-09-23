package forecast

import "math"

// backtest проверяет модель так же, как её потом будут использовать: за каждый
// из последних 28 дней строит прогноз на 1–7 дней вперёд по данным строго до
// этого дня и сравнивает с фактом (§4.3).
func backtest(pts []point, fit fitter) Metrics {
	if len(pts) < MinDaysForM1 {
		return Metrics{WAPE: 1, WindowDays: BacktestDays, DaysWithData: len(pts)}
	}

	// Индекс по дню — чтобы быстро найти факт для дня прогноза.
	actual := make(map[clock2Key]float64, len(pts))
	for _, p := range pts {
		actual[key(p)] = p.qty
	}

	// Origin — день, на который «встаёт» модель. Оставляем минимум неделю истории.
	firstOrigin := len(pts) - BacktestDays
	if firstOrigin < MinDaysForM1 {
		firstOrigin = MinDaysForM1
	}

	var sumAbsErr, sumActual, sumErr float64
	errors := make([]float64, 0, BacktestDays*BacktestHorizon)

	for i := firstOrigin; i < len(pts); i++ {
		// Обучаем только на том, что было известно до origin.
		model := fit(pts[:i])
		origin := pts[i-1].day

		for h := 1; h <= BacktestHorizon; h++ {
			day := origin.AddDays(h)
			fact, ok := actual[clock2Key{day.Year, int(day.Month), day.Date}]
			if !ok {
				// День без данных или дефицитный — сравнивать не с чем.
				continue
			}
			predicted := model.Predict(day)
			e := predicted - fact

			sumAbsErr += math.Abs(e)
			sumActual += fact
			sumErr += e
			errors = append(errors, e)
		}
	}

	m := Metrics{WindowDays: BacktestDays, DaysWithData: len(pts)}
	switch {
	case len(errors) == 0:
		// Проверять нечего — считаем модель непроверенной, а не идеальной.
		m.WAPE = 1
	case sumActual <= 0:
		// Факт нулевой: любая ненулевая ошибка — это 100% промаха.
		if sumAbsErr == 0 {
			m.WAPE = 0
		} else {
			m.WAPE = 1
		}
	default:
		m.WAPE = sumAbsErr / sumActual
		m.Bias = sumErr / sumActual
	}
	m.Sigma = stddev(errors)
	return m
}

type clock2Key struct {
	year  int
	month int
	date  int
}

func key(p point) clock2Key {
	return clock2Key{p.day.Year, int(p.day.Month), p.day.Date}
}

// stddev — стандартное отклонение ошибки прогноза; идёт в страховой запас.
func stddev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	m := mean(values)
	var sum float64
	for _, v := range values {
		d := v - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)-1))
}
