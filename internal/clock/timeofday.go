package clock

import (
	"fmt"
	"time"
)

// TimeOfDay — время суток без даты: время отсечки заказа у поставщика
// и время ежедневной сводки (§5.1, §5.5).
type TimeOfDay struct {
	Hour   int
	Minute int
}

// ParseTimeOfDay разбирает "16:00" и "16:00:00".
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	for _, layout := range []string{"15:04", "15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return TimeOfDay{Hour: t.Hour(), Minute: t.Minute()}, nil
		}
	}
	return TimeOfDay{}, fmt.Errorf("clock: не время суток: %q", s)
}

// MustParseTimeOfDay паникует на мусоре. Только для тестов и констант.
func MustParseTimeOfDay(s string) TimeOfDay {
	t, err := ParseTimeOfDay(s)
	if err != nil {
		panic(err)
	}
	return t
}

// String отдаёт "16:00".
func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }

// On возвращает момент этого времени суток в указанный день и пояс.
func (t TimeOfDay) On(day Day, loc *time.Location) time.Time {
	return time.Date(day.Year, day.Month, day.Date, t.Hour, t.Minute, 0, 0, loc)
}

// Before сообщает, что момент now раньше этого времени суток в свой день.
// Используется для проверки «успеваем ли до отсечки».
func (t TimeOfDay) Before(now time.Time, loc *time.Location) bool {
	return now.Before(t.On(DayIn(now, loc), loc))
}
