// Package tenant хранит сведения о тенанте и переносит их через контекст.
//
// tenant_id берётся только отсюда — из сессии, никогда из тела запроса
// или URL (§12.1, п. 1).
package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
)

// ErrNoTenant возвращается, когда в контексте нет тенанта. Это всегда баг
// проводки, а не пользовательская ошибка.
var ErrNoTenant = errors.New("tenant: в контексте нет тенанта")

// Tenant — организация со своими настройками и часовым поясом.
type Tenant struct {
	ID       uuid.UUID
	Name     string
	Location *time.Location
	// IsSandbox: демо-тенант. Внешние каналы уведомлений ему отключены,
	// а время идёт со смещением (§7).
	IsSandbox bool
	// ClockOffset — смещение виртуальных часов песочницы (ADR-006).
	ClockOffset time.Duration
	ExpiresAt   *time.Time
	// Autopilot — симулятор песочницы сам оформляет заказы по рекомендациям (§7.3).
	Autopilot bool
	// Seed — зерно генератора демо-данных. Одинаковый seed даёт одинаковые
	// данные, на этом держатся тесты и проверка точности прогноза (§7.2).
	Seed     int64
	Settings Settings
}

// Settings — то, что владелец меняет в настройках (§6, экран «Настройки»).
type Settings struct {
	// DigestAt — время ежедневной сводки, по умолчанию 09:00.
	DigestAt clock.TimeOfDay `json:"digest_at"`
	// QuietFrom и QuietTo — тихие часы: срочные алерты в этом окне
	// откладываются до утра (§5.5).
	QuietFrom clock.TimeOfDay `json:"quiet_from"`
	QuietTo   clock.TimeOfDay `json:"quiet_to"`
	// Каналы доставки. Лента в интерфейсе включена всегда.
	TelegramEnabled bool `json:"telegram_enabled"`
	EmailEnabled    bool `json:"email_enabled"`
}

// DefaultSettings — значения по умолчанию из ТЗ.
func DefaultSettings() Settings {
	return Settings{
		DigestAt:        clock.TimeOfDay{Hour: 9},
		QuietFrom:       clock.TimeOfDay{Hour: 22},
		QuietTo:         clock.TimeOfDay{Hour: 8},
		TelegramEnabled: true,
		EmailEnabled:    true,
	}
}

// ParseSettings разбирает jsonb из tenants.settings. Пустой или испорченный
// объект даёт значения по умолчанию: тенант без настроек всё равно должен
// работать.
func ParseSettings(raw []byte) Settings {
	s := DefaultSettings()
	if len(raw) == 0 {
		return s
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return DefaultSettings()
	}
	return s
}

// Clock отдаёт часы тенанта: настоящие для рабочего тенанта и смещённые
// для песочницы.
func (t Tenant) Clock(base clock.Clock) clock.Clock {
	if t.ClockOffset == 0 {
		return base
	}
	return clock.NewOffset(base, t.ClockOffset)
}

// Now — текущий момент по часам тенанта.
func (t Tenant) Now(base clock.Clock) time.Time { return t.Clock(base).Now() }

// Today — сегодняшний день в часовом поясе тенанта. Именно он считается
// «днём» в прогнозе, дневном расходе и окнах поставки (§12).
func (t Tenant) Today(base clock.Clock) clock.Day {
	return clock.DayIn(t.Now(base), t.Loc())
}

// Loc — часовой пояс тенанта; UTC, если пояс почему-то не загрузился.
func (t Tenant) Loc() *time.Location {
	if t.Location == nil {
		return time.UTC
	}
	return t.Location
}

// IsQuietHours сообщает, что момент попадает в тихие часы и срочный алерт
// нужно отложить до утра (§5.5).
func (t Tenant) IsQuietHours(at time.Time) bool {
	s := t.Settings
	if s.QuietFrom == s.QuietTo {
		return false
	}
	local := at.In(t.Loc())
	minutes := local.Hour()*60 + local.Minute()
	from := s.QuietFrom.Hour*60 + s.QuietFrom.Minute
	to := s.QuietTo.Hour*60 + s.QuietTo.Minute

	if from < to {
		return minutes >= from && minutes < to
	}
	// Окно через полночь: 22:00–08:00.
	return minutes >= from || minutes < to
}

// LoadLocation загружает пояс по имени; неизвестное имя даёт UTC, чтобы
// опечатка в настройках не роняла тенанта целиком.
func LoadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

type ctxKey struct{}

// WithTenant кладёт тенанта в контекст.
func WithTenant(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromContext достаёт тенанта из контекста.
func FromContext(ctx context.Context) (Tenant, error) {
	t, ok := ctx.Value(ctxKey{}).(Tenant)
	if !ok {
		return Tenant{}, ErrNoTenant
	}
	return t, nil
}

// MustFromContext достаёт тенанта и паникует, если его нет. Годится только
// в обработчиках за middleware аутентификации, где отсутствие тенанта —
// это ошибка проводки маршрутов.
func MustFromContext(ctx context.Context) Tenant {
	t, err := FromContext(ctx)
	if err != nil {
		panic(err)
	}
	return t
}
