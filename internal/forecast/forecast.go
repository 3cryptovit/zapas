// Package forecast считает прогноз расхода на 14 дней вперёд (§4).
//
// Пакет намеренно не знает ни про БД, ни про часы: на вход подаётся готовый ряд
// дней, на выход — прогноз и метрики. Поэтому он целиком покрывается табличными
// тестами с заранее посчитанными ответами (§4.4).
//
// Внутри математика идёт в float64: прогноз — это оценка, а не учётная запись.
// Наружу количества выходят уже как qty.Qty и дальше живут в numeric(14,3).
package forecast

import (
	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Параметры расчёта из ТЗ.
const (
	HorizonDays     = 14 // глубина прогноза
	BacktestDays    = 28 // сколько дней проверяем на бэктесте
	BacktestHorizon = 7  // на сколько дней вперёд строится каждый прогноз бэктеста
	SeasonWeeks     = 8  // окно для индекса дня недели
	OutlierWeeks    = 8  // окно для отсечения выбросов
	HistoryDays     = 90 // сколько истории собирает ночная задача

	MinDaysForM1 = 7  // меньше — модели нет вообще
	MinDaysForM2 = 28 // меньше — только среднее по дню недели
)

// AlphaGrid — перебор коэффициента сглаживания для M2 (§4.2).
var AlphaGrid = []float64{0.1, 0.2, 0.3, 0.4, 0.5}

// Model — выбранная модель прогноза.
type Model string

const (
	ModelM0 Model = "M0" // ручной минимум: данных меньше 7 дней
	ModelM1 Model = "M1" // среднее по дню недели
	ModelM2 Model = "M2" // сглаживание с сезонностью
)

// Observation — один день ряда расхода.
type Observation struct {
	Day clock.Day
	Qty qty.Qty
	// HasData: в тенанте был хотя бы один расход или пересчёт в этот день.
	// «Нет данных ≠ ноль» — день без данных исключается из ряда (§4.1).
	HasData bool
	// Stockout: остаток доходил до нуля. Реальный спрос был выше учтённого,
	// и модель не должна учиться на заниженной цифре.
	Stockout bool
}

// Point — прогноз на конкретный день.
type Point struct {
	Day clock.Day `json:"day"`
	Qty qty.Qty   `json:"qty"`
}

// Metrics — результат бэктеста (§4.3).
type Metrics struct {
	Model Model `json:"model"`
	// WAPE — взвешенная средняя абсолютная ошибка. MAPE не годится: при днях
	// с нулевым расходом он уходит в бесконечность.
	WAPE float64 `json:"wape"`
	// Bias — систематическая ошибка: положительный означает «завышает».
	Bias float64 `json:"bias"`
	// Sigma — стандартное отклонение дневной ошибки, идёт в страховой запас (§5.2).
	Sigma float64 `json:"sigma"`
	// Alpha — подобранный коэффициент сглаживания, только для M2.
	Alpha        float64 `json:"alpha,omitempty"`
	WindowDays   int     `json:"window_days"`
	DaysWithData int     `json:"days_with_data"`
}

// Accuracy — точность для интерфейса: 1 − WAPE, но не ниже нуля (§4.3).
func (m Metrics) Accuracy() float64 {
	acc := 1 - m.WAPE
	if acc < 0 {
		return 0
	}
	return acc
}

// Result — прогноз на горизонт и метрики выбранной модели.
type Result struct {
	Model   Model   `json:"model"`
	Points  []Point `json:"points"`
	Metrics Metrics `json:"metrics"`
}

// Total — суммарный прогноз на всём горизонте.
func (r Result) Total() qty.Qty {
	total := qty.Zero()
	for _, p := range r.Points {
		total = total.Add(p.Qty)
	}
	return total
}

// On возвращает прогноз на конкретный день; для дня вне горизонта — ноль.
func (r Result) On(day clock.Day) qty.Qty {
	for _, p := range r.Points {
		if p.Day.Equal(day) {
			return p.Qty
		}
	}
	return qty.Zero()
}

// Compute строит прогноз на horizon дней начиная с asOf.
//
// series — история строго до asOf в произвольном порядке; дни без данных и дни
// дефицита можно не отфильтровывать, это делает сам пакет.
func Compute(series []Observation, asOf clock.Day, horizon int) Result {
	if horizon <= 0 {
		horizon = HorizonDays
	}
	days := make([]clock.Day, 0, horizon)
	for i := 0; i < horizon; i++ {
		days = append(days, asOf.AddDays(i))
	}

	clean := cleanSeries(series, asOf)

	// Меньше недели данных — прогноза нет, статус считается по ручному минимуму.
	if len(clean) < MinDaysForM1 {
		return Result{
			Model:   ModelM0,
			Points:  zeroPoints(days),
			Metrics: Metrics{Model: ModelM0, WAPE: 1, DaysWithData: len(clean)},
		}
	}

	m1 := backtest(clean, fitM1)
	best, bestFit, bestMetrics := ModelM1, fitM1, m1

	if len(clean) >= MinDaysForM2 {
		// α подбирается перебором по тому же бэктесту (§4.2).
		for _, alpha := range AlphaGrid {
			fit := fitM2(alpha)
			m := backtest(clean, fit)
			// Строгое «меньше»: при равенстве остаётся более простая модель.
			if m.WAPE < bestMetrics.WAPE {
				best, bestFit, bestMetrics = ModelM2, fit, m
				bestMetrics.Alpha = alpha
			}
		}
	}

	model := bestFit(clean)
	points := make([]Point, 0, len(days))
	for _, d := range days {
		points = append(points, Point{Day: d, Qty: fromFloat(model.Predict(d))})
	}

	bestMetrics.Model = best
	bestMetrics.WindowDays = BacktestDays
	bestMetrics.DaysWithData = len(clean)

	return Result{Model: best, Points: points, Metrics: bestMetrics}
}

func zeroPoints(days []clock.Day) []Point {
	points := make([]Point, 0, len(days))
	for _, d := range days {
		points = append(points, Point{Day: d, Qty: qty.Zero()})
	}
	return points
}

func fromFloat(v float64) qty.Qty {
	if v <= 0 {
		return qty.Zero()
	}
	return qty.FromInt(1).MulFloat(v)
}
