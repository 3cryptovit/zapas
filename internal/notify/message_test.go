package notify_test

import (
	"strings"
	"testing"

	googleuuid "github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

func ptr(q qty.Qty) *qty.Qty { return &q }

// TestRenderDigest_ПримерИзТЗ воспроизводит сводку из §5.5 дословно.
func TestRenderDigest_ПримерИзТЗ(t *testing.T) {
	got := notify.RenderDigest(notify.DigestData{
		TenantName: "Кофейня «Демо»",
		Day:        clock.MustParseDay("2026-09-23"), // среда
		Suppliers: []notify.DigestSupplier{
			{
				Name:       "Молочная ферма",
				Cutoff:     clock.MustParseTimeOfDay("16:00"),
				DeliveryAt: clock.MustParseDay("2026-09-24"), // чт
				Lines: []notify.DigestLine{
					{
						Name: "Молоко 3,2%", Qty: qty.MustParse("24"), Unit: "l",
						PurchaseQty: ptr(qty.MustParse("2")), PurchaseUnit: "кор.",
						StockoutDate: clock.MustParseDay("2026-09-25"), // пт
					},
					{
						Name: "Сливки 33%", Qty: qty.MustParse("6"), Unit: "l",
						PurchaseQty: ptr(qty.MustParse("1")), PurchaseUnit: "кор.",
					},
				},
			},
			{
				Name:       "Кофе-Импорт",
				Cutoff:     clock.MustParseTimeOfDay("18:00"),
				DeliveryAt: clock.MustParseDay("2026-09-25"), // пт
				Lines: []notify.DigestLine{
					{
						Name: "Зерно Бразилия", Qty: qty.MustParse("2"), Unit: "kg",
						PurchaseQty: ptr(qty.MustParse("2")), PurchaseUnit: "× 1 кг",
					},
				},
			},
		},
		Critical: []notify.DigestCritical{
			{
				Name:         "Стаканы 350 мл",
				StockoutDate: clock.MustParseDay("2026-09-24"), // завтра
				NextDelivery: clock.MustParseDay("2026-09-25"), // пт
			},
		},
		DashboardURL: "https://example.test/app",
	})

	want := strings.Join([]string{
		"Кофейня «Демо» · среда, 23 сентября",
		"",
		"Заказать сегодня (3)",
		"Молочная ферма — до 16:00, поставка в чт",
		"  • Молоко 3,2% — 2 кор. (24 л), сейчас хватит до пт",
		"  • Сливки 33% — 1 кор. (6 л)",
		"Кофе-Импорт — до 18:00, поставка в пт",
		"  • Зерно Бразилия — 2 × 1 кг (2 кг)",
		"",
		"Критично (1)",
		"  • Стаканы 350 мл — хватит только до завтра, ближайшая поставка в пт",
		"",
		"Открыть дашборд →",
		"",
	}, "\n")

	if got.Body != want {
		t.Errorf("текст сводки разошёлся с примером ТЗ.\nПолучено:\n%s\nОжидалось:\n%s", got.Body, want)
	}
	if got.Title != "Сводка на 23 сен" {
		t.Errorf("заголовок = %q", got.Title)
	}
	if len(got.Items) != 3 {
		t.Errorf("позиций в payload = %d, want 3", len(got.Items))
	}
}

func TestDigest_Пустая(t *testing.T) {
	d := notify.DigestData{TenantName: "Кофейня", Day: clock.MustParseDay("2026-09-23")}
	if !d.Empty() {
		t.Error("сводка без заказов и без красных позиций должна считаться пустой")
	}
	if d.OrderCount() != 0 {
		t.Errorf("OrderCount = %d, want 0", d.OrderCount())
	}
}

func TestRenderCritical(t *testing.T) {
	tests := []struct {
		name string
		data notify.CriticalData
		want string
	}{
		{
			name: "закончилось совсем",
			data: notify.CriticalData{
				ItemName: "Молоко 3,2%", OnHand: qty.Zero(), Unit: "l",
				Today:        clock.MustParseDay("2026-09-23"),
				NextDelivery: clock.MustParseDay("2026-09-24"),
				SupplierName: "Молочная ферма",
			},
			want: "Молоко 3,2% — закончилось. Ближайшая поставка в чт от «Молочная ферма» — нужна срочная закупка.",
		},
		{
			name: "хватит только до завтра",
			data: notify.CriticalData{
				ItemName: "Стаканы 350 мл", OnHand: qty.MustParse("40"), Unit: "pcs",
				Today:        clock.MustParseDay("2026-09-23"),
				StockoutDate: clock.MustParseDay("2026-09-24"),
				NextDelivery: clock.MustParseDay("2026-09-25"),
				SupplierName: "Упак-Сервис",
			},
			want: "Стаканы 350 мл — хватит только до завтра (сейчас 40 шт). Ближайшая поставка в пт от «Упак-Сервис» — нужна срочная закупка.",
		},
		{
			name: "хватит только на сегодня",
			data: notify.CriticalData{
				ItemName: "Сироп", OnHand: qty.MustParse("0.5"), Unit: "l",
				Today:        clock.MustParseDay("2026-09-23"),
				StockoutDate: clock.MustParseDay("2026-09-23"),
			},
			want: "Сироп — хватит только на сегодня (сейчас 0,5 л)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := notify.RenderCritical(tc.data)
			if got.Body != tc.want {
				t.Errorf("текст = %q,\nwant %q", got.Body, tc.want)
			}
			if !strings.HasPrefix(got.Title, "Срочно: ") {
				t.Errorf("заголовок = %q", got.Title)
			}
		})
	}
}

func TestRenderCutoff(t *testing.T) {
	got := notify.RenderCutoff(notify.CutoffData{
		SupplierName: "Молочная ферма",
		Cutoff:       clock.MustParseTimeOfDay("16:00"),
		ItemCount:    2,
	})
	want := "«Молочная ферма» принимает заказы до 16:00. 2 позиции всё ещё не заказано."
	if got.Body != want {
		t.Errorf("текст = %q, want %q", got.Body, want)
	}
}

func TestRenderLate(t *testing.T) {
	got := notify.RenderLate(notify.LateData{
		SupplierName: "Упак-Сервис",
		ExpectedAt:   clock.MustParseDay("2026-09-24"),
		ItemCount:    1,
	})
	want := "Поставка от «Упак-Сервис» ожидалась 24 сен и не принята. 1 позиция ждут приёмки."
	if got.Body != want {
		t.Errorf("текст = %q, want %q", got.Body, want)
	}
}

func TestRenderMismatch(t *testing.T) {
	got := notify.RenderMismatch(notify.MismatchData{
		SupplierName: "Молочная ферма",
		Lines: []notify.MismatchLine{
			{Name: "Молоко 3,2%", Ordered: qty.MustParse("24"), Received: qty.MustParse("18"), Unit: "l"},
			{Name: "Сливки 33%", Ordered: qty.MustParse("6"), Received: qty.MustParse("6"), Unit: "l"},
			{Name: "Зерно", Ordered: qty.MustParse("2"), Received: qty.MustParse("3"), Unit: "kg"},
		},
	})

	// Совпавшая строка в текст не идёт: о ней сообщать нечего.
	if strings.Contains(got.Body, "Сливки") {
		t.Errorf("совпавшая строка попала в текст:\n%s", got.Body)
	}
	for _, want := range []string{
		"заказано 24 л, принято 18 л (-6)",
		"заказано 2 кг, принято 3 кг (+1)",
	} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("в тексте нет %q:\n%s", want, got.Body)
		}
	}
}

func TestDedupKeys_РазныеДляРазныхДней(t *testing.T) {
	item := googleuuid.Must(googleuuid.NewV7())
	d1 := clock.MustParseDay("2026-09-23")
	d2 := clock.MustParseDay("2026-09-24")

	if notify.CriticalKey(item, d1) == notify.CriticalKey(item, d2) {
		t.Error("ключ срочного алерта должен отличаться по дням")
	}
	// Повторный вызов через переменные, а не выражением с самим собой:
	// иначе staticcheck справедливо видит сравнение одинаковых частей.
	first := notify.CriticalKey(item, d1)
	second := notify.CriticalKey(item, d1)
	if first != second {
		t.Errorf("ключ должен быть стабильным: %q и %q", first, second)
	}
	if notify.DigestKey(d1) == notify.DigestKey(d2) {
		t.Error("ключ сводки должен отличаться по дням")
	}
}
