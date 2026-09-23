package clock_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vostapenko/zapas/internal/clock"
)

func TestSystem_ОтдаётUTC(t *testing.T) {
	now := clock.System{}.Now()
	if now.Location() != time.UTC {
		t.Errorf("зона = %v, want UTC: в БД всё хранится в UTC", now.Location())
	}
	if time.Since(now) > time.Minute {
		t.Errorf("системные часы отстают: %v", now)
	}
}

func TestFunc_АдаптируетФункцию(t *testing.T) {
	want := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	var c clock.Clock = clock.Func(func() time.Time { return want })
	if !c.Now().Equal(want) {
		t.Errorf("Now = %v, want %v", c.Now(), want)
	}
}

func TestFixed_SetИAdvance(t *testing.T) {
	c := clock.NewFixed(time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))
	c.Advance(36 * time.Hour)
	if got := c.Now().Format(time.RFC3339); got != "2026-09-24T12:00:00Z" {
		t.Errorf("после Advance = %s, want 2026-09-24T12:00:00Z", got)
	}
	c.Set(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if got := c.Now().Format("2006-01-02"); got != "2026-01-01" {
		t.Errorf("после Set = %s, want 2026-01-01", got)
	}
}

func TestParseDay_Ошибки(t *testing.T) {
	for _, in := range []string{"", "23.09.2026", "2026-13-01", "nonsense"} {
		if _, err := clock.ParseDay(in); err == nil {
			t.Errorf("ParseDay(%q) не вернул ошибку", in)
		}
	}
}

func TestDay_Сравнение(t *testing.T) {
	a := clock.MustParseDay("2026-09-23")
	b := clock.MustParseDay("2026-09-24")

	if !a.Before(b) || b.Before(a) {
		t.Error("Before работает неверно")
	}
	if !b.After(a) || a.After(b) {
		t.Error("After работает неверно")
	}
	if !a.Equal(clock.MustParseDay("2026-09-23")) {
		t.Error("Equal работает неверно")
	}
	if a.IsZero() {
		t.Error("заданный день не может быть нулевым")
	}
	if !(clock.Day{}).IsZero() {
		t.Error("нулевой день должен определяться как нулевой")
	}
	if got := a.Sub(b); got != -1 {
		t.Errorf("Sub назад = %d, want -1", got)
	}
}

func TestDay_JSON(t *testing.T) {
	type payload struct {
		Day clock.Day `json:"day"`
	}

	// Дни в API — строки YYYY-MM-DD в поясе тенанта (§11).
	out, err := json.Marshal(payload{Day: clock.MustParseDay("2026-09-23")})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"day":"2026-09-23"}` {
		t.Fatalf("Marshal = %s", out)
	}

	// Незаданный день — null, а не «нулевой год».
	out, err = json.Marshal(payload{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"day":null}` {
		t.Fatalf("Marshal пустого = %s, want null", out)
	}

	var in payload
	if err := json.Unmarshal([]byte(`{"day":"2026-09-30"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Day.String() != "2026-09-30" {
		t.Fatalf("Unmarshal = %s", in.Day)
	}
	if err := json.Unmarshal([]byte(`{"day":null}`), &in); err != nil {
		t.Fatal(err)
	}
	if !in.Day.IsZero() {
		t.Fatalf("null должен давать нулевой день, got %s", in.Day)
	}
	if err := json.Unmarshal([]byte(`{"day":"23.09.2026"}`), &in); err == nil {
		t.Fatal("мусор в дате должен давать ошибку")
	}
}

func TestDay_Time_ВПоясеТенанта(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("нет базы часовых поясов: %v", err)
	}
	got := clock.MustParseDay("2026-09-23").Time(loc)
	if got.Format(time.RFC3339) != "2026-09-23T00:00:00+03:00" {
		t.Errorf("полночь дня = %s", got.Format(time.RFC3339))
	}
}

func TestTimeOfDay(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("нет базы часовых поясов: %v", err)
	}

	cutoff := clock.MustParseTimeOfDay("16:00")
	if cutoff.String() != "16:00" {
		t.Errorf("String = %s, want 16:00", cutoff.String())
	}
	if got := clock.MustParseTimeOfDay("16:00:00").String(); got != "16:00" {
		t.Errorf("формат с секундами = %s, want 16:00", got)
	}
	for _, bad := range []string{"", "25:00", "полдень"} {
		if _, err := clock.ParseTimeOfDay(bad); err == nil {
			t.Errorf("ParseTimeOfDay(%q) не вернул ошибку", bad)
		}
	}

	day := clock.MustParseDay("2026-09-23")
	if got := cutoff.On(day, loc).Format(time.RFC3339); got != "2026-09-23T16:00:00+03:00" {
		t.Errorf("On = %s", got)
	}

	before := time.Date(2026, 9, 23, 15, 59, 0, 0, loc)
	after := time.Date(2026, 9, 23, 16, 0, 0, 0, loc)
	if !cutoff.Before(before, loc) {
		t.Error("15:59 должно быть раньше отсечки 16:00")
	}
	if cutoff.Before(after, loc) {
		t.Error("ровно 16:00 — отсечка уже прошла")
	}
}
