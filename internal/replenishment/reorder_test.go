package replenishment_test

import (
	"math"
	"testing"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/replenishment"
)

// flatForecast — прогноз, ровный на всём горизонте, кроме сегодняшнего дня.
func flatForecast(today clock.Day, todayQty, restQty string, model forecast.Model, sigma float64) forecast.Result {
	points := make([]forecast.Point, 0, forecast.HorizonDays)
	for i := 0; i < forecast.HorizonDays; i++ {
		value := restQty
		if i == 0 {
			value = todayQty
		}
		points = append(points, forecast.Point{Day: today.AddDays(i), Qty: qty.MustParse(value)})
	}
	return forecast.Result{
		Model:   model,
		Points:  points,
		Metrics: forecast.Metrics{Model: model, Sigma: sigma},
	}
}

func approx(t *testing.T, got qty.Qty, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got.Float64()-want) > tol {
		t.Errorf("%s = %s, want ≈ %.3f", what, got, want)
	}
}

// TestDecide_ПримерИзКарточки воспроизводит блок «почему такой статус» (§6.2):
// «Сейчас 18 л, в пути 0. До поставки по следующему заказу (пн) нужно 31 л
// + страховой запас 4 л. Не хватает 17 л → заказать 2 кор. (24 л)».
func TestDecide_ПримерИзКарточки(t *testing.T) {
	loc := msk(t)
	now := at(t, loc, "2026-09-23 10:00") // среда
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy)

	// σ подобрана так, чтобы SS = z·σ·√n дал ровно 4 л при n = 5 и z = 1,65.
	const sigma = 1.084155

	got := replenishment.Decide(replenishment.Input{
		Today:         today,
		Window:        window,
		OnHand:        qty.MustParse("18"),
		Forecast:      flatForecast(today, "6.2", "7", forecast.ModelM2, sigma),
		ConsumedToday: qty.MustParse("3.2"),
		ServiceLevel:  95,
		Pack:          qty.MustParse("12"),
	})

	if got.Status != replenishment.StatusOrderToday {
		t.Errorf("статус = %s, want order_today", got.Status)
	}
	approx(t, got.Explanation.NeedUntilD2, 31, 0.01, "потребность до d2")
	approx(t, got.SafetyStock, 4, 0.01, "страховой запас")
	approx(t, got.TargetLevel, 35, 0.02, "целевой уровень S")
	approx(t, got.Explanation.Shortfall, 17, 0.02, "нехватка")

	// Заказ округляется вверх до кратности упаковки: 17 л → 2 коробки = 24 л.
	if got.RecommendedQty.String() != "24.000" {
		t.Errorf("рекомендуемый заказ = %s, want 24.000", got.RecommendedQty)
	}
	if got.Explanation.D2.String() != "2026-09-28" {
		t.Errorf("d2 = %s, want 2026-09-28", got.Explanation.D2)
	}
}

func TestDecide_СегодняшнийРасходНеСчитаетсяДважды(t *testing.T) {
	loc := msk(t)
	now := at(t, loc, "2026-09-23 10:00")
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy)

	base := replenishment.Input{
		Today:        today,
		Window:       window,
		OnHand:       qty.MustParse("100"),
		Forecast:     flatForecast(today, "10", "10", forecast.ModelM1, 0),
		ServiceLevel: 95,
	}

	// Ничего ещё не израсходовано: в S входит весь сегодняшний прогноз.
	fresh := replenishment.Decide(base)

	// Половина дня прошла: в S входит только остаток сегодняшнего прогноза.
	base.ConsumedToday = qty.MustParse("4")
	partial := replenishment.Decide(base)

	diff := fresh.TargetLevel.Sub(partial.TargetLevel)
	if diff.String() != "4.000" {
		t.Errorf("разница целевых уровней = %s, want 4.000", diff)
	}

	// Израсходовано больше прогноза — вклад сегодняшнего дня обнуляется,
	// а не уходит в минус (max(0, F − c)).
	base.ConsumedToday = qty.MustParse("25")
	over := replenishment.Decide(base)
	if got := fresh.TargetLevel.Sub(over.TargetLevel).String(); got != "10.000" {
		t.Errorf("перерасход должен обнулить сегодняшний вклад, разница = %s, want 10.000", got)
	}
}

func TestDecide_ВПутиУчитываетсяТолькоДоD2(t *testing.T) {
	loc := msk(t)
	now := at(t, loc, "2026-09-23 10:00")
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy) // d2 = 2026-09-28

	base := replenishment.Input{
		Today:        today,
		Window:       window,
		OnHand:       qty.MustParse("10"),
		Forecast:     flatForecast(today, "10", "10", forecast.ModelM1, 0),
		ServiceLevel: 95,
	}

	// Поставка приедет в четверг — успевает, значит считается в IP.
	base.Incoming = []replenishment.Delivery{{
		Day: clock.MustParseDay("2026-09-24"), Qty: qty.MustParse("50"),
	}}
	early := replenishment.Decide(base)
	if early.OnOrder.String() != "50.000" {
		t.Errorf("в пути = %s, want 50.000", early.OnOrder)
	}

	// Та же поставка в день d2 уже не помогает: она приедет не раньше, чем
	// заказ, который мы решаем оформить сегодня.
	base.Incoming = []replenishment.Delivery{{
		Day: clock.MustParseDay("2026-09-28"), Qty: qty.MustParse("50"),
	}}
	late := replenishment.Decide(base)
	if late.OnOrder.String() != "0.000" {
		t.Errorf("в пути = %s, want 0.000: поставка в d2 не спасает", late.OnOrder)
	}
}

func TestDecide_РекомендуемоеКоличество(t *testing.T) {
	loc := msk(t)
	now := at(t, loc, "2026-09-23 10:00")
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy)

	tests := []struct {
		name     string
		onHand   string
		minOrder string
		pack     string
		want     string
	}{
		// S = 10 (сегодня) + 10×4 = 50, σ = 0.
		{"без упаковки и минимума — ровно нехватка", "20", "0", "0", "30.000"},
		{"округление вверх до коробки 12", "20", "0", "12", "36.000"},
		{"минимальная партия больше нехватки", "45", "20", "0", "20.000"},
		{"минимальная партия округляется до упаковки", "45", "20", "12", "24.000"},
		{"запаса хватает — заказ не нужен", "60", "0", "12", "0.000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := replenishment.Decide(replenishment.Input{
				Today:        today,
				Window:       window,
				OnHand:       qty.MustParse(tc.onHand),
				Forecast:     flatForecast(today, "10", "10", forecast.ModelM1, 0),
				ServiceLevel: 95,
				MinOrder:     qty.MustParse(tc.minOrder),
				Pack:         qty.MustParse(tc.pack),
			})
			if got.RecommendedQty.String() != tc.want {
				t.Errorf("Q = %s, want %s (S = %s, IP = %s)",
					got.RecommendedQty, tc.want, got.TargetLevel, got.Explanation.InventoryPos)
			}
		})
	}
}

func TestZFactor(t *testing.T) {
	tests := map[int]float64{90: 1.28, 95: 1.65, 99: 2.33, 0: 1.65}
	for level, want := range tests {
		if got := replenishment.ZFactor(level); got != want {
			t.Errorf("ZFactor(%d) = %v, want %v", level, got, want)
		}
	}
}

func TestDecide_Статусы(t *testing.T) {
	loc := msk(t)
	now := at(t, loc, "2026-09-23 10:00") // среда, d1 = чт 24-го, d2 = пн 28-го
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy)

	tests := []struct {
		name   string
		onHand string
		daily  string
		manual string
		model  forecast.Model
		want   replenishment.Status
	}{
		{
			name:   "на складе ноль",
			onHand: "0", daily: "10", model: forecast.ModelM1,
			want: replenishment.StatusOutOfStock,
		},
		{
			name:   "закончится раньше ближайшей поставки",
			onHand: "5", daily: "10", model: forecast.ModelM1,
			want: replenishment.StatusCritical,
		},
		{
			name:   "запаса до d1 хватает, но до d2 — нет",
			onHand: "25", daily: "10", model: forecast.ModelM1,
			want: replenishment.StatusOrderToday,
		},
		{
			name:   "хватает с запасом",
			onHand: "200", daily: "10", model: forecast.ModelM1,
			want: replenishment.StatusOK,
		},
		{
			name:   "мало данных и минимум не задан",
			onHand: "25", daily: "0", manual: "0", model: forecast.ModelM0,
			want: replenishment.StatusNoForecast,
		},
		{
			name:   "мало данных, но остаток ниже ручного минимума",
			onHand: "5", daily: "0", manual: "20", model: forecast.ModelM0,
			want: replenishment.StatusOrderToday,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := replenishment.Decide(replenishment.Input{
				Today:        today,
				Window:       window,
				OnHand:       qty.MustParse(tc.onHand),
				Forecast:     flatForecast(today, tc.daily, tc.daily, tc.model, 0),
				ServiceLevel: 95,
				ManualMin:    qty.MustParse(orZero(tc.manual)),
			})
			if got.Status != tc.want {
				t.Errorf("статус = %s, want %s (остаток %s, S = %s, хватит до %s)",
					got.Status, tc.want, got.Explanation.OnHand, got.TargetLevel, got.StockoutDate)
			}
		})
	}
}

func TestDecide_ЕслиМожноЖдать_НапоминанияНет(t *testing.T) {
	loc := msk(t)
	// Вторник: и сегодня, и завтра поставка придёт в четверг (§5.1).
	now := at(t, loc, "2026-09-22 10:00")
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy)

	if !window.CanWait() {
		t.Fatal("во вторник d1 должно совпасть с d2")
	}

	got := replenishment.Decide(replenishment.Input{
		Today:        today,
		Window:       window,
		OnHand:       qty.MustParse("25"),
		Forecast:     flatForecast(today, "10", "10", forecast.ModelM1, 0),
		ServiceLevel: 95,
	})

	if got.Status == replenishment.StatusOrderToday {
		t.Error("если ждать ничего не стоит, позиция не должна просить заказ сегодня")
	}
}

func TestDecide_ХватитДо_УчитываетОжидаемуюПоставку(t *testing.T) {
	loc := msk(t)
	now := at(t, loc, "2026-09-23 10:00")
	today := clock.DayIn(now, loc)
	window := replenishment.ComputeWindow(now, loc, dairy)

	base := replenishment.Input{
		Today:        today,
		Window:       window,
		OnHand:       qty.MustParse("25"),
		Forecast:     flatForecast(today, "10", "10", forecast.ModelM1, 0),
		ServiceLevel: 95,
	}

	// Без поставки: 25 → 15 → 5 → −5, кончится в пятницу.
	bare := replenishment.Decide(base)
	if got := bare.StockoutDate.String(); got != "2026-09-25" {
		t.Errorf("хватит до %s, want 2026-09-25", got)
	}

	// Поставка в четверг отодвигает дефицит.
	base.Incoming = []replenishment.Delivery{{
		Day: clock.MustParseDay("2026-09-24"), Qty: qty.MustParse("40"),
	}}
	withDelivery := replenishment.Decide(base)
	if !withDelivery.StockoutDate.After(bare.StockoutDate) {
		t.Errorf("поставка должна отодвинуть дефицит: было %s, стало %s",
			bare.StockoutDate, withDelivery.StockoutDate)
	}
}

func TestStatus_ПорядокУхудшения(t *testing.T) {
	// Повторный срочный алерт уходит только при переходе на более тяжёлый
	// статус (§5.5), поэтому порядок должен быть строгим.
	order := []replenishment.Status{
		replenishment.StatusOK,
		replenishment.StatusNoForecast,
		replenishment.StatusOrderToday,
		replenishment.StatusCritical,
		replenishment.StatusOutOfStock,
	}
	for i := 1; i < len(order); i++ {
		if !order[i].WorseThan(order[i-1]) {
			t.Errorf("%s должен быть тяжелее %s", order[i], order[i-1])
		}
	}
	if replenishment.StatusOK.WorseThan(replenishment.StatusOK) {
		t.Error("статус не тяжелее самого себя")
	}
	if !replenishment.StatusCritical.IsRed() || replenishment.StatusOrderToday.IsRed() {
		t.Error("красными считаются только critical и out_of_stock")
	}
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}
