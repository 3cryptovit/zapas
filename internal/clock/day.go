package clock

import "time"

// Day — календарный день в часовом поясе тенанта, без времени и зоны.
//
// «День» в системе — не UTC-сутки: дневной расход, прогноз и дни доставки
// считаются в поясе тенанта (§12, «Часовые пояса»). Отдельный тип не даёт
// перепутать день с моментом времени.
type Day struct {
	Year  int
	Month time.Month
	Date  int
}

// DayIn возвращает календарный день момента t в поясе loc.
func DayIn(t time.Time, loc *time.Location) Day {
	y, m, d := t.In(loc).Date()
	return Day{Year: y, Month: m, Date: d}
}

// MustParseDay разбирает YYYY-MM-DD и паникует на мусоре. Только для тестов и констант.
func MustParseDay(s string) Day {
	d, err := ParseDay(s)
	if err != nil {
		panic(err)
	}
	return d
}

// ParseDay разбирает день в формате YYYY-MM-DD.
func ParseDay(s string) (Day, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Day{}, err
	}
	return Day{Year: t.Year(), Month: t.Month(), Date: t.Day()}, nil
}

// String отдаёт YYYY-MM-DD — формат дней в API (§11).
func (d Day) String() string { return d.Time(time.UTC).Format("2006-01-02") }

// Time — полночь этого дня в поясе loc.
func (d Day) Time(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Date, 0, 0, 0, 0, loc)
}

// AddDays сдвигает день на n суток вперёд (или назад при отрицательном n).
func (d Day) AddDays(n int) Day {
	t := d.Time(time.UTC).AddDate(0, 0, n)
	return Day{Year: t.Year(), Month: t.Month(), Date: t.Day()}
}

// Weekday — день недели. Понедельник = 1, воскресенье = 7 (ISO-8601),
// как в suppliers.delivery_weekdays.
func (d Day) Weekday() int {
	wd := int(d.Time(time.UTC).Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

// Before сообщает, что d строго раньше other.
func (d Day) Before(other Day) bool { return d.Time(time.UTC).Before(other.Time(time.UTC)) }

// After сообщает, что d строго позже other.
func (d Day) After(other Day) bool { return other.Before(d) }

// Equal сравнивает дни.
func (d Day) Equal(other Day) bool { return d == other }

// Sub возвращает число суток между d и other (d - other).
func (d Day) Sub(other Day) int {
	return int(d.Time(time.UTC).Sub(other.Time(time.UTC)).Hours() / 24)
}

// IsZero сообщает, что день не задан.
func (d Day) IsZero() bool { return d == Day{} }

// MarshalJSON сериализует день как "YYYY-MM-DD".
func (d Day) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.String() + `"`), nil
}

// UnmarshalJSON разбирает "YYYY-MM-DD" и null.
func (d *Day) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		*d = Day{}
		return nil
	}
	parsed, err := ParseDay(trimQuotes(s))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// DaysBetween перечисляет дни включительно от from до to.
func DaysBetween(from, to Day) []Day {
	if to.Before(from) {
		return nil
	}
	out := make([]Day, 0, to.Sub(from)+1)
	for d := from; !d.After(to); d = d.AddDays(1) {
		out = append(out, d)
	}
	return out
}
