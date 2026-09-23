package forecast_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// start — понедельник, чтобы дни недели в тестах читались глазами.
var start = clock.MustParseDay("2026-06-01")

func obs(day clock.Day, value string) forecast.Observation {
	return forecast.Observation{Day: day, Qty: qty.MustParse(value), HasData: true}
}

// series строит ряд из n дней подряд, вызывая value для каждого дня.
func series(n int, value func(i int, day clock.Day) forecast.Observation) []forecast.Observation {
	out := make([]forecast.Observation, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, value(i, start.AddDays(i)))
	}
	return out
}

func TestCompute_M0_МалоДанных(t *testing.T) {
	in := series(6, func(_ int, d clock.Day) forecast.Observation { return obs(d, "10") })
	got := forecast.Compute(in, start.AddDays(6), forecast.HorizonDays)

	if got.Model != forecast.ModelM0 {
		t.Fatalf("модель = %s, want M0", got.Model)
	}
	if len(got.Points) != forecast.HorizonDays {
		t.Fatalf("точек = %d, want %d", len(got.Points), forecast.HorizonDays)
	}
	if !got.Total().IsZero() {
		t.Errorf("прогноз M0 должен быть нулевым, got %s", got.Total())
	}
	if got.Metrics.Accuracy() != 0 {
		t.Errorf("точность M0 = %v, want 0", got.Metrics.Accuracy())
	}
}

func TestCompute_M1_СреднееПоДнюНедели(t *testing.T) {
	// Ровный будний спрос 10, выходные 20. Ряда на 21 день хватает для M1,
	// но не хватает для M2 (нужно 28).
	in := series(21, func(_ int, d clock.Day) forecast.Observation {
		if d.Weekday() >= 6 {
			return obs(d, "20")
		}
		return obs(d, "10")
	})
	asOf := start.AddDays(21)
	got := forecast.Compute(in, asOf, 7)

	if got.Model != forecast.ModelM1 {
		t.Fatalf("модель = %s, want M1", got.Model)
	}
	for _, p := range got.Points {
		want := "10.000"
		if p.Day.Weekday() >= 6 {
			want = "20.000"
		}
		if p.Qty.String() != want {
			t.Errorf("%s (день недели %d): прогноз %s, want %s",
				p.Day, p.Day.Weekday(), p.Qty, want)
		}
	}
	// Ряд идеально регулярный — бэктест не должен показывать ошибку.
	if got.Metrics.WAPE > 1e-9 {
		t.Errorf("WAPE = %v, want ~0", got.Metrics.WAPE)
	}
}

func TestCleanSeries_ДеньБезДанныхНеНоль(t *testing.T) {
	// Если бы пропуск считался нулём, среднее по вторникам просело бы вдвое.
	in := series(28, func(i int, d clock.Day) forecast.Observation {
		o := obs(d, "10")
		if d.Weekday() == 2 && i > 7 {
			o.HasData = false
			o.Qty = qty.Zero()
		}
		return o
	})
	got := forecast.Compute(in, start.AddDays(28), 7)

	for _, p := range got.Points {
		if p.Qty.LessThan(qty.MustParse("9.5")) {
			t.Fatalf("%s: прогноз %s — пропуск посчитали нулём", p.Day, p.Qty)
		}
	}
}

func TestCleanSeries_ДниДефицитаИсключаются(t *testing.T) {
	// Три дня подряд склад пустовал: учтённый расход 1 при реальном спросе 10.
	in := series(28, func(i int, d clock.Day) forecast.Observation {
		if i >= 25 {
			return forecast.Observation{Day: d, Qty: qty.MustParse("1"), HasData: true, Stockout: true}
		}
		return obs(d, "10")
	})
	got := forecast.Compute(in, start.AddDays(28), 7)

	for _, p := range got.Points {
		if p.Qty.LessThan(qty.MustParse("9")) {
			t.Fatalf("%s: прогноз %s — модель училась на днях дефицита", p.Day, p.Qty)
		}
	}
}

func TestCleanSeries_ВыбросОбрезается(t *testing.T) {
	// Ровный спрос 10 и один день ×20 (мероприятие рядом).
	in := series(56, func(i int, d clock.Day) forecast.Observation {
		if i == 40 {
			return obs(d, "200")
		}
		return obs(d, "10")
	})
	got := forecast.Compute(in, start.AddDays(56), 7)

	for _, p := range got.Points {
		if p.Qty.GreaterThan(qty.MustParse("12")) {
			t.Fatalf("%s: прогноз %s — разовый всплеск раздул заказ", p.Day, p.Qty)
		}
	}
}

func TestCompute_M2_ВыигрываетНаСезонности(t *testing.T) {
	// Явная недельная сезонность плюс сдвиг уровня в середине ряда: среднее по
	// дню недели за 4 недели отстаёт, сглаживание догоняет.
	in := series(84, func(i int, d clock.Day) forecast.Observation {
		base := 10.0
		if i > 42 {
			base = 20.0 // уровень вырос вдвое
		}
		if d.Weekday() >= 6 {
			base *= 1.3
		}
		return forecast.Observation{
			Day: d, HasData: true,
			Qty: qty.FromInt(1).MulFloat(base),
		}
	})
	got := forecast.Compute(in, start.AddDays(84), forecast.HorizonDays)

	if got.Model != forecast.ModelM2 {
		t.Fatalf("модель = %s, want M2 (WAPE=%.4f)", got.Model, got.Metrics.WAPE)
	}
	if got.Metrics.Alpha <= 0 {
		t.Errorf("α не подобран: %v", got.Metrics.Alpha)
	}
}

func TestCompute_РавныйWAPE_ВыбираетM1(t *testing.T) {
	// Идеально ровный ряд: обе модели попадают точно, побеждает более простая.
	in := series(84, func(_ int, d clock.Day) forecast.Observation { return obs(d, "10") })
	got := forecast.Compute(in, start.AddDays(84), 7)

	if got.Model != forecast.ModelM1 {
		t.Fatalf("модель = %s, want M1 при равном WAPE", got.Model)
	}
}

func TestMetrics_BiasПоказываетЗавышение(t *testing.T) {
	// Ряд с резким падением в конце: модель какое-то время помнит старый
	// уровень и завышает — Bias должен быть положительным.
	in := series(84, func(i int, d clock.Day) forecast.Observation {
		if i >= 70 {
			return obs(d, "5")
		}
		return obs(d, "20")
	})
	got := forecast.Compute(in, start.AddDays(84), 7)

	if got.Metrics.Bias <= 0 {
		t.Errorf("Bias = %.4f, ожидалось завышение (> 0)", got.Metrics.Bias)
	}
}

func TestAccuracy_НеНижеНуля(t *testing.T) {
	m := forecast.Metrics{WAPE: 2.5}
	if m.Accuracy() != 0 {
		t.Errorf("Accuracy = %v, want 0", m.Accuracy())
	}
	m = forecast.Metrics{WAPE: 0.14}
	if math.Abs(m.Accuracy()-0.86) > 1e-9 {
		t.Errorf("Accuracy = %v, want 0.86", m.Accuracy())
	}
}

// TestWAPE_ЦельТЗ — критерий приёмки §4.3: на сгенерированных данных для
// позиций с ежедневным расходом WAPE ≤ 20%. Seed фиксирован, тест
// воспроизводим (§7.2).
func TestWAPE_ЦельТЗ(t *testing.T) {
	const seed = 20260919

	items := []struct {
		name    string
		base    float64
		noise   float64
		weekend float64
	}{
		{"молоко: ровный спрос", 24, 0.15, 1.3},
		{"зерно: спрос поменьше", 6, 0.20, 1.3},
		{"стаканы: крупные числа", 180, 0.18, 1.35},
		{"сиропы: слабая сезонность", 3, 0.25, 1.1},
	}

	for i, item := range items {
		t.Run(item.name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, uint64(i)))
			in := generate(rng, 90, item.base, item.noise, item.weekend)

			got := forecast.Compute(in, start.AddDays(90), forecast.HorizonDays)

			if got.Model == forecast.ModelM0 {
				t.Fatalf("на 90 днях истории модель не должна быть M0")
			}
			if got.Metrics.WAPE > 0.20 {
				t.Errorf("WAPE = %.1f%% (модель %s), цель ТЗ ≤ 20%%",
					got.Metrics.WAPE*100, got.Model)
			}
			if got.Metrics.Sigma <= 0 {
				t.Errorf("σ = %v, ожидалось положительное: оно идёт в страховой запас",
					got.Metrics.Sigma)
			}
		})
	}
}

// generate повторяет модель генератора песочницы (§7.2): базовый спрос ×
// индекс дня недели × медленный тренд × шум, со всплеском раз в 3–4 недели.
func generate(rng *rand.Rand, days int, base, noise, weekend float64) []forecast.Observation {
	out := make([]forecast.Observation, 0, days)
	for i := 0; i < days; i++ {
		d := start.AddDays(i)
		v := base
		if d.Weekday() >= 6 {
			v *= weekend
		}
		v *= 1 + 0.10*float64(i)/float64(days) // медленный тренд +10% за период
		v *= 1 + noise*(rng.Float64()*2-1)
		if i%24 == 23 {
			v *= 2 // мероприятие рядом
		}
		if v < 0 {
			v = 0
		}
		out = append(out, forecast.Observation{
			Day: d, HasData: true, Qty: qty.FromInt(1).MulFloat(v),
		})
	}
	return out
}
