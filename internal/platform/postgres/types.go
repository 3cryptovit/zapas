package postgres

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Tx — транзакция. Псевдоним, чтобы модули не импортировали pgx напрямую.
type Tx = pgx.Tx

// Преобразования между типами pgx и доменными типами.
//
// Сложены в одном месте: иначе каждый модуль начинает по-своему решать,
// что делать с pgtype.Timestamptz{Valid: false}.

// Time упаковывает момент времени.
func Time(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// NullTime упаковывает необязательный момент времени.
func NullTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// TimeOrZero распаковывает момент; NULL даёт нулевое время.
func TimeOrZero(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// TimePtr распаковывает необязательный момент.
func TimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// Date упаковывает календарный день тенанта.
func Date(d clock.Day) pgtype.Date {
	return pgtype.Date{Time: d.Time(time.UTC), Valid: true}
}

// NullDate упаковывает необязательный день; нулевой день даёт NULL.
func NullDate(d clock.Day) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return Date(d)
}

// Day распаковывает день. Дата в БД хранится без зоны, поэтому читаем её
// как есть: пояс тенанта уже учтён при записи.
func Day(d pgtype.Date) clock.Day {
	if !d.Valid {
		return clock.Day{}
	}
	y, m, day := d.Time.Date()
	return clock.Day{Year: y, Month: m, Date: day}
}

// TimeOfDay упаковывает время суток (order_cutoff, время сводки).
func TimeOfDay(t clock.TimeOfDay) pgtype.Time {
	micros := int64(t.Hour)*int64(time.Hour/time.Microsecond) +
		int64(t.Minute)*int64(time.Minute/time.Microsecond)
	return pgtype.Time{Microseconds: micros, Valid: true}
}

// ClockTime распаковывает время суток.
func ClockTime(t pgtype.Time) clock.TimeOfDay {
	if !t.Valid {
		return clock.TimeOfDay{}
	}
	total := t.Microseconds / int64(time.Minute/time.Microsecond)
	return clock.TimeOfDay{Hour: int(total / 60), Minute: int(total % 60)}
}

// Interval упаковывает длительность (clock_offset песочницы).
func Interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: int64(d / time.Microsecond), Valid: true}
}

// Duration распаковывает интервал. Месяцы и дни считаются календарно,
// но clock_offset хранится в сутках и часах, поэтому приближение точное.
func Duration(i pgtype.Interval) time.Duration {
	if !i.Valid {
		return 0
	}
	return time.Duration(i.Microseconds)*time.Microsecond +
		time.Duration(i.Days)*24*time.Hour +
		time.Duration(i.Months)*30*24*time.Hour
}

// Qty оборачивает numeric из БД в доменное количество.
func Qty(d decimal.Decimal) qty.Qty { return qty.FromDecimal(d) }

// NullQty распаковывает необязательное количество.
func NullQty(d decimal.NullDecimal) (qty.Qty, bool) {
	if !d.Valid {
		return qty.Zero(), false
	}
	return qty.FromDecimal(d.Decimal), true
}

// QtyOrZero распаковывает необязательное количество, подставляя ноль.
func QtyOrZero(d decimal.NullDecimal) qty.Qty {
	v, _ := NullQty(d)
	return v
}

// DecimalOf разворачивает доменное количество обратно в numeric.
func DecimalOf(q qty.Qty) decimal.Decimal { return q.Decimal() }

// NullDecimalOf упаковывает необязательное количество.
func NullDecimalOf(q *qty.Qty) decimal.NullDecimal {
	if q == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: q.Decimal(), Valid: true}
}
