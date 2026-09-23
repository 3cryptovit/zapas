package replenishment_test

import (
	"testing"
	"time"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/replenishment"
)

// msk — пояс тенанта по умолчанию. «День» считается в нём, а не в UTC.
func msk(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("нет базы часовых поясов: %v", err)
	}
	return loc
}

// dairy — поставщик из примера ТЗ (§5.1): возит по пн и чт, срок 1 день,
// отсечка 16:00.
var dairy = replenishment.Supplier{
	LeadTimeDays:     1,
	DeliveryWeekdays: []int{1, 4},
	Cutoff:           clock.MustParseTimeOfDay("16:00"),
}

func at(t *testing.T, loc *time.Location, s string) time.Time {
	t.Helper()
	moment, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return moment
}

// TestComputeWindow_ПримерИзТЗ дословно воспроизводит пример раздела 5.1.
func TestComputeWindow_ПримерИзТЗ(t *testing.T) {
	loc := msk(t)

	// 2026-09-22 — вторник, 2026-09-23 — среда.
	tests := []struct {
		name    string
		now     string
		wantD1  string
		wantD2  string
		canWait bool
	}{
		{
			name:    "вторник до отсечки: d1 = d2 = чт, можно ждать",
			now:     "2026-09-22 10:00",
			wantD1:  "2026-09-24",
			wantD2:  "2026-09-24",
			canWait: true,
		},
		{
			name:    "среда до отсечки: d1 = чт, d2 = пн — проверяем запас",
			now:     "2026-09-23 10:00",
			wantD1:  "2026-09-24",
			wantD2:  "2026-09-28",
			canWait: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := replenishment.ComputeWindow(at(t, loc, tc.now), loc, dairy)

			if got := w.D1.String(); got != tc.wantD1 {
				t.Errorf("d1 = %s, want %s", got, tc.wantD1)
			}
			if got := w.D2.String(); got != tc.wantD2 {
				t.Errorf("d2 = %s, want %s", got, tc.wantD2)
			}
			if w.CanWait() != tc.canWait {
				t.Errorf("CanWait = %v, want %v", w.CanWait(), tc.canWait)
			}
		})
	}
}

func TestComputeWindow_ПослеОтсечкиСчитаемОтЗавтра(t *testing.T) {
	loc := msk(t)

	// Среда 16:30 — отсечка уже прошла, заказ уйдёт завтра (в четверг),
	// поэтому ближайшая поставка не четверг, а понедельник.
	w := replenishment.ComputeWindow(at(t, loc, "2026-09-23 16:30"), loc, dairy)

	if !w.AfterCutoff {
		t.Fatal("отсечка 16:00 в 16:30 должна считаться пройденной")
	}
	if got := w.OrderDay.String(); got != "2026-09-24" {
		t.Errorf("день заказа = %s, want 2026-09-24", got)
	}
	if got := w.D1.String(); got != "2026-09-28" {
		t.Errorf("d1 = %s, want 2026-09-28 (пн)", got)
	}
	if got := w.OrderBy.Format("2006-01-02 15:04"); got != "2026-09-24 16:00" {
		t.Errorf("дедлайн = %s, want 2026-09-24 16:00", got)
	}
}

func TestComputeWindow_ГраницаОтсечки(t *testing.T) {
	loc := msk(t)

	// Ровно 16:00 — уже поздно: «до 16:00» значит строго раньше.
	w := replenishment.ComputeWindow(at(t, loc, "2026-09-23 16:00"), loc, dairy)
	if !w.AfterCutoff {
		t.Error("ровно в отсечку заказ уже не принимается")
	}

	// За минуту до — ещё успеваем.
	w = replenishment.ComputeWindow(at(t, loc, "2026-09-23 15:59"), loc, dairy)
	if w.AfterCutoff {
		t.Error("за минуту до отсечки заказ ещё принимается")
	}
}

func TestComputeWindow_ВозитКаждыйДень(t *testing.T) {
	loc := msk(t)
	daily := replenishment.Supplier{
		LeadTimeDays:     1,
		DeliveryWeekdays: []int{1, 2, 3, 4, 5, 6, 7},
		Cutoff:           clock.MustParseTimeOfDay("20:00"),
	}

	w := replenishment.ComputeWindow(at(t, loc, "2026-09-23 10:00"), loc, daily)
	if got := w.D1.String(); got != "2026-09-24" {
		t.Errorf("d1 = %s, want 2026-09-24", got)
	}
	if got := w.D2.String(); got != "2026-09-25" {
		t.Errorf("d2 = %s, want 2026-09-25", got)
	}
	if w.CanWait() {
		t.Error("при ежедневной доставке отсрочка на день сдвигает поставку")
	}
}

func TestComputeWindow_ДлинныйСрокПоставки(t *testing.T) {
	loc := msk(t)
	// Упак-Сервис из §7.2: срок 3 дня, возит только по средам, отсечка 12:00.
	packaging := replenishment.Supplier{
		LeadTimeDays:     3,
		DeliveryWeekdays: []int{3},
		Cutoff:           clock.MustParseTimeOfDay("12:00"),
	}

	// Среда 10:00: заказ сегодня → не раньше субботы → ближайшая среда 30-го.
	w := replenishment.ComputeWindow(at(t, loc, "2026-09-23 10:00"), loc, packaging)
	if got := w.D1.String(); got != "2026-09-30" {
		t.Errorf("d1 = %s, want 2026-09-30", got)
	}
	// Заказ завтра → не раньше воскресенья → та же среда. Ждать можно.
	if got := w.D2.String(); got != "2026-09-30" {
		t.Errorf("d2 = %s, want 2026-09-30", got)
	}
	if !w.CanWait() {
		t.Error("если обе даты совпали, напоминания сегодня быть не должно")
	}
}

func TestComputeWindow_ПереходЧерезПолночьВПоясеТенанта(t *testing.T) {
	loc := msk(t)

	// 21:30 UTC — это уже 00:30 следующего дня в Москве.
	now := time.Date(2026, 9, 22, 21, 30, 0, 0, time.UTC)
	w := replenishment.ComputeWindow(now, loc, dairy)

	if got := w.OrderDay.String(); got != "2026-09-23" {
		t.Errorf("день заказа = %s, want 2026-09-23: день считается в поясе тенанта", got)
	}
}
