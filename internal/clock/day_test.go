package clock_test

import (
	"testing"
	"time"

	"github.com/vostapenko/zapas/internal/clock"
)

func TestDayIn_КрайСуток(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("нет базы часовых поясов: %v", err)
	}

	tests := []struct {
		name string
		utc  string
		want string
	}{
		{"полночь по Москве — уже новый день", "2026-09-22T21:00:00Z", "2026-09-23"},
		{"за минуту до полуночи — ещё старый день", "2026-09-22T20:59:00Z", "2026-09-22"},
		{"полдень", "2026-09-23T09:00:00Z", "2026-09-23"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			moment, err := time.Parse(time.RFC3339, tc.utc)
			if err != nil {
				t.Fatal(err)
			}
			if got := clock.DayIn(moment, msk).String(); got != tc.want {
				t.Errorf("DayIn(%s) = %s, want %s", tc.utc, got, tc.want)
			}
		})
	}
}

func TestDay_Weekday_ISO(t *testing.T) {
	tests := []struct {
		day  string
		want int
	}{
		{"2026-09-21", 1}, // понедельник
		{"2026-09-24", 4}, // четверг
		{"2026-09-27", 7}, // воскресенье — 7, а не 0
	}
	for _, tc := range tests {
		if got := clock.MustParseDay(tc.day).Weekday(); got != tc.want {
			t.Errorf("Weekday(%s) = %d, want %d", tc.day, got, tc.want)
		}
	}
}

func TestDay_AddDaysSub_ЧерезПереводЧасов(t *testing.T) {
	// Дни считаются календарно, поэтому DST не должен влиять на арифметику.
	from := clock.MustParseDay("2026-03-28")
	got := from.AddDays(3)
	if got.String() != "2026-03-31" {
		t.Fatalf("AddDays = %s, want 2026-03-31", got)
	}
	if n := got.Sub(from); n != 3 {
		t.Fatalf("Sub = %d, want 3", n)
	}
}

func TestDaysBetween(t *testing.T) {
	got := clock.DaysBetween(clock.MustParseDay("2026-09-21"), clock.MustParseDay("2026-09-24"))
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if got[0].String() != "2026-09-21" || got[3].String() != "2026-09-24" {
		t.Fatalf("границы неверны: %v", got)
	}
	if rev := clock.DaysBetween(clock.MustParseDay("2026-09-24"), clock.MustParseDay("2026-09-21")); rev != nil {
		t.Fatalf("обратный интервал должен быть пустым, got %v", rev)
	}
}

func TestOffset_ВиртуальноеВремя(t *testing.T) {
	base := clock.NewFixed(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	virtual := clock.NewOffset(base, 7*24*time.Hour)

	if got := virtual.Now().Format(time.RFC3339); got != "2026-09-30T12:00:00Z" {
		t.Fatalf("Now = %s, want 2026-09-30T12:00:00Z", got)
	}
	// Сдвиг базовых часов виден через смещённые: песочница не «замораживается».
	base.Advance(time.Hour)
	if got := virtual.Now().Format(time.RFC3339); got != "2026-09-30T13:00:00Z" {
		t.Fatalf("Now после Advance = %s, want 2026-09-30T13:00:00Z", got)
	}
}
