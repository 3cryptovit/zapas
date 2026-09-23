package testsupport

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// pgTime упаковывает время суток для колонки time.
func pgTime(t time.Time) pgtype.Time {
	micros := int64(t.Hour())*int64(time.Hour/time.Microsecond) +
		int64(t.Minute())*int64(time.Minute/time.Microsecond)
	return pgtype.Time{Microseconds: micros, Valid: true}
}
