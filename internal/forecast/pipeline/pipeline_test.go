package pipeline_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }

func silent() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// setup поднимает тенанта с молочным поставщиком из §7.2:
// возит по пн и чт, срок 1 день, отсечка 16:00.
func setup(t *testing.T) (*pipeline.Service, *replenishment.Service, *testsupport.Fixture, uuid.UUID) {
	t.Helper()
	env := testsupport.Shared(t)
	f := env.NewTenant(t, "Кофейня «Прогноз»")

	supplier := f.NewSupplier(t, "Молочная ферма", 1, []int16{1, 4}, "16:00")
	item := f.NewItem(t, "Молоко 3,2%", "l")
	f.SetDefaultSupplier(t, item, supplier)
	// Коробка 12 л, кратность 12.
	f.SetTerms(t, supplier, item, "кор.", "12", "12", "0")

	repl := replenishment.NewService(env.App, env.Clock, nil)
	pipe := pipeline.NewService(env.App, env.Clock, repl, silent())
	return pipe, repl, f, item
}

// TestPipeline_СтроитПрогнозИСтатус проходит всю цепочку:
// история расхода → ряд → модель → прогноз на 14 дней → статус позиции.
func TestPipeline_СтроитПрогнозИСтатус(t *testing.T) {
	pipe, _, f, item := setup(t)
	ctx := context.Background()

	// Часы теста стоят на 2026-09-23 (среда). Набиваем 60 дней ровного
	// расхода по 6 л, на выходных больше.
	today := f.Tenant.Today(f.Env.Clock)
	from := today.AddDays(-60)
	f.SeedHistory(t, item, from, 60, func(_ int, d clock.Day) string {
		if d.Weekday() >= 6 {
			return "8"
		}
		return "6"
	})

	result, err := pipe.Run(ctx, f.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if result.Items == 0 {
		t.Fatal("конвейер не обработал ни одной позиции")
	}
	if result.WithModel == 0 {
		t.Fatalf("на 60 днях истории модель обязана построиться (WAPE %.3f)", result.AvgWAPE)
	}
	if result.AvgWAPE > 0.20 {
		t.Errorf("WAPE = %.1f%%, цель ТЗ ≤ 20%%", result.AvgWAPE*100)
	}

	// Прогноз записан на 14 дней вперёд.
	points := readForecast(t, f, item, today)
	if len(points) != forecast.HorizonDays {
		t.Fatalf("точек прогноза %d, want %d", len(points), forecast.HorizonDays)
	}
	if points[0].Day != today {
		t.Errorf("прогноз начинается с %s, want %s", points[0].Day, today)
	}
	// Будний прогноз должен быть около 6 л.
	for _, p := range points {
		if p.Day.Weekday() >= 6 {
			continue
		}
		if p.Qty.LessThan(mustQty("4")) || p.Qty.GreaterThan(mustQty("8")) {
			t.Errorf("%s: прогноз %s, ожидалось около 6", p.Day, p.Qty)
		}
	}

	// Статус посчитан и лежит в read-модели дашборда.
	status := readStatus(t, f, item)
	if status.Status == "" {
		t.Fatal("статус не записан")
	}
	if status.Status == sqlc.ItemStatusCodeNoForecast {
		t.Error("при построенной модели статус не может быть no_forecast")
	}
}

// TestPipeline_Идемпотентность: повторный прогон за ту же дату перезаписывает
// результат, а не дублирует его (§4.4).
func TestPipeline_Идемпотентность(t *testing.T) {
	pipe, _, f, item := setup(t)
	ctx := context.Background()

	today := f.Tenant.Today(f.Env.Clock)
	f.SeedHistory(t, item, today.AddDays(-40), 40, func(int, clock.Day) string { return "5" })

	first, err := pipe.Run(ctx, f.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pipe.Run(ctx, f.Tenant)
	if err != nil {
		t.Fatal(err)
	}

	if first.Items != second.Items {
		t.Errorf("повторный прогон обработал другое число позиций: %d и %d", first.Items, second.Items)
	}
	if got := len(readForecast(t, f, item, today)); got != forecast.HorizonDays {
		t.Errorf("после двух прогонов точек прогноза %d, want %d", got, forecast.HorizonDays)
	}
}

// TestPipeline_ЗаказатьСегодня — связка прогноза с правилом заказа (§5.2).
func TestPipeline_ЗаказатьСегодня(t *testing.T) {
	pipe, _, f, item := setup(t)
	ctx := context.Background()

	today := f.Tenant.Today(f.Env.Clock)
	f.SeedHistory(t, item, today.AddDays(-60), 60, func(int, clock.Day) string { return "6" })

	// Остатка хватит на пару дней, а следующая поставка после четверга —
	// только в понедельник. Среда: d1 = чт, d2 = пн.
	f.SetOnHand(t, item, "10")

	if _, err := pipe.Run(ctx, f.Tenant); err != nil {
		t.Fatal(err)
	}

	status := readStatus(t, f, item)
	if status.Status != sqlc.ItemStatusCodeOrderToday && status.Status != sqlc.ItemStatusCodeCritical {
		t.Fatalf("статус = %s, ожидался order_today или critical", status.Status)
	}
	recommended := postgres.Qty(status.RecommendedQty)
	if !recommended.IsPositive() {
		t.Fatal("рекомендуемое количество должно быть больше нуля")
	}
	// Заказ округлён вверх до коробки 12 л.
	if rem := recommended.Decimal().Mod(mustQty("12").Decimal()); !rem.IsZero() {
		t.Errorf("заказ %s не кратен упаковке 12", recommended)
	}
	if !status.StockoutDate.Valid {
		t.Error("дата «хватит до» не посчитана")
	}
}

// TestPipeline_ЗапасаХватает — зелёный статус при большом остатке.
func TestPipeline_ЗапасаХватает(t *testing.T) {
	pipe, _, f, item := setup(t)
	ctx := context.Background()

	today := f.Tenant.Today(f.Env.Clock)
	f.SeedHistory(t, item, today.AddDays(-60), 60, func(int, clock.Day) string { return "6" })
	f.SetOnHand(t, item, "500")

	if _, err := pipe.Run(ctx, f.Tenant); err != nil {
		t.Fatal(err)
	}

	status := readStatus(t, f, item)
	if status.Status != sqlc.ItemStatusCodeOk {
		t.Errorf("статус = %s, want ok", status.Status)
	}
	if postgres.Qty(status.RecommendedQty).IsPositive() {
		t.Errorf("при достаточном запасе заказ не нужен, got %s", postgres.Qty(status.RecommendedQty))
	}
}

// TestPipeline_МалоДанных — модель M0 и серый статус (§4.2).
func TestPipeline_МалоДанных(t *testing.T) {
	pipe, _, f, item := setup(t)
	ctx := context.Background()

	today := f.Tenant.Today(f.Env.Clock)
	// Три дня истории — меньше минимальных семи.
	f.SeedHistory(t, item, today.AddDays(-3), 3, func(int, clock.Day) string { return "6" })

	result, err := pipe.Run(ctx, f.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if result.WithModel != 0 {
		t.Errorf("на трёх днях истории модели быть не должно, got %d", result.WithModel)
	}

	if points := readForecast(t, f, item, today); len(points) != 0 {
		t.Errorf("для M0 прогноз не записывается, got %d точек", len(points))
	}

	status := readStatus(t, f, item)
	if status.Status != sqlc.ItemStatusCodeNoForecast {
		t.Errorf("статус = %s, want no_forecast", status.Status)
	}
}

// TestPipeline_ДеньБезДанныхНеНоль: пропуск в истории не должен занижать прогноз.
func TestPipeline_ДеньБезДанныхНеНоль(t *testing.T) {
	pipe, _, f, item := setup(t)
	ctx := context.Background()

	today := f.Tenant.Today(f.Env.Clock)
	// По вторникам кофейня закрыта: движений нет вообще.
	f.SeedHistory(t, item, today.AddDays(-60), 60, func(_ int, d clock.Day) string {
		if d.Weekday() == 2 {
			return "0"
		}
		return "10"
	})

	if _, err := pipe.Run(ctx, f.Tenant); err != nil {
		t.Fatal(err)
	}

	// В тенанте есть другие позиции? Нет — значит вторники попадут в ряд
	// как дни без данных и будут исключены, а не посчитаны нулём.
	for _, p := range readForecast(t, f, item, today) {
		if p.Day.Weekday() != 2 {
			continue
		}
		if p.Qty.LessThan(mustQty("5")) {
			t.Errorf("прогноз на вторник %s = %s: пропуск посчитали нулём", p.Day, p.Qty)
		}
	}
}

// --- вспомогательное ---

func readForecast(t *testing.T, f *testsupport.Fixture, itemID uuid.UUID, from clock.Day) []forecast.Point {
	t.Helper()
	ctx := context.Background()

	var out []forecast.Point
	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListForecastForItem(ctx, sqlc.ListForecastForItemParams{
			TenantID: f.Tenant.ID, ItemID: itemID, Day: postgres.Date(from),
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, forecast.Point{Day: postgres.Day(r.Day), Qty: postgres.Qty(r.Qty)})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("чтение прогноза: %v", err)
	}
	return out
}

func readStatus(t *testing.T, f *testsupport.Fixture, itemID uuid.UUID) sqlc.ItemStatus {
	t.Helper()
	ctx := context.Background()

	var out sqlc.ItemStatus
	err := f.Env.App.InTenantTx(ctx, f.Tenant.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetItemStatus(ctx, sqlc.GetItemStatusParams{
			TenantID: f.Tenant.ID, ItemID: itemID,
		})
		if err != nil {
			return err
		}
		out = row
		return nil
	})
	if err != nil {
		t.Fatalf("чтение статуса: %v", err)
	}
	return out
}

func mustQty(s string) qty.Qty { return qty.MustParse(s) }
