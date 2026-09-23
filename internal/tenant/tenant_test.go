package tenant_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/tenant"
)

func msk(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("нет базы часовых поясов: %v", err)
	}
	return loc
}

func TestTenant_Today_ВПоясеТенанта(t *testing.T) {
	tn := tenant.Tenant{Location: msk(t)}

	// 21:30 UTC — в Москве уже следующий день.
	base := clock.NewFixed(time.Date(2026, 9, 22, 21, 30, 0, 0, time.UTC))
	if got := tn.Today(base).String(); got != "2026-09-23" {
		t.Errorf("Today = %s, want 2026-09-23", got)
	}

	// Тот же момент в UTC-тенанте — ещё вчера.
	utcTenant := tenant.Tenant{Location: time.UTC}
	if got := utcTenant.Today(base).String(); got != "2026-09-22" {
		t.Errorf("Today в UTC = %s, want 2026-09-22", got)
	}
}

func TestTenant_Clock_ПесочницаЖивётСоСмещением(t *testing.T) {
	base := clock.NewFixed(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))

	real := tenant.Tenant{Location: time.UTC}
	if !real.Now(base).Equal(base.Now()) {
		t.Error("у рабочего тенанта часы должны совпадать с настоящими")
	}

	sandbox := tenant.Tenant{Location: time.UTC, IsSandbox: true, ClockOffset: 7 * 24 * time.Hour}
	if got := sandbox.Today(base).String(); got != "2026-09-30" {
		t.Errorf("Today песочницы = %s, want 2026-09-30", got)
	}
}

func TestTenant_IsQuietHours(t *testing.T) {
	loc := msk(t)
	tn := tenant.Tenant{Location: loc, Settings: tenant.DefaultSettings()} // 22:00–08:00

	tests := []struct {
		name  string
		hour  int
		quiet bool
	}{
		{"глубокая ночь", 3, true},
		{"ровно начало окна", 22, true},
		{"перед полуночью", 23, true},
		{"ровно конец окна", 8, false},
		{"рабочий день", 14, false},
		{"за минуту до окна", 21, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 9, 23, tc.hour, 0, 0, 0, loc)
			if got := tn.IsQuietHours(at); got != tc.quiet {
				t.Errorf("IsQuietHours(%02d:00) = %v, want %v", tc.hour, got, tc.quiet)
			}
		})
	}
}

func TestTenant_IsQuietHours_ОкноВнутриСуток(t *testing.T) {
	loc := msk(t)
	tn := tenant.Tenant{Location: loc, Settings: tenant.Settings{
		QuietFrom: clock.TimeOfDay{Hour: 13},
		QuietTo:   clock.TimeOfDay{Hour: 15},
	}}

	if !tn.IsQuietHours(time.Date(2026, 9, 23, 14, 0, 0, 0, loc)) {
		t.Error("14:00 должно попадать в окно 13:00–15:00")
	}
	if tn.IsQuietHours(time.Date(2026, 9, 23, 3, 0, 0, 0, loc)) {
		t.Error("03:00 не должно попадать в окно 13:00–15:00")
	}
}

func TestTenant_IsQuietHours_ОкноВыключено(t *testing.T) {
	tn := tenant.Tenant{Location: time.UTC} // QuietFrom == QuietTo == 00:00
	if tn.IsQuietHours(time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)) {
		t.Error("при совпадающих границах тихих часов быть не должно")
	}
}

func TestParseSettings(t *testing.T) {
	// Пустой объект даёт значения по умолчанию из ТЗ.
	got := tenant.ParseSettings(nil)
	if got.DigestAt.String() != "09:00" {
		t.Errorf("сводка по умолчанию = %s, want 09:00", got.DigestAt)
	}
	if got.QuietFrom.String() != "22:00" || got.QuietTo.String() != "08:00" {
		t.Errorf("тихие часы по умолчанию = %s–%s, want 22:00–08:00", got.QuietFrom, got.QuietTo)
	}

	got = tenant.ParseSettings([]byte(`{"digest_at":{"Hour":7,"Minute":30},"email_enabled":false}`))
	if got.DigestAt.String() != "07:30" {
		t.Errorf("сводка = %s, want 07:30", got.DigestAt)
	}
	if got.EmailEnabled {
		t.Error("email должен быть выключен")
	}
	// Не заданные поля остаются значениями по умолчанию.
	if got.QuietFrom.String() != "22:00" {
		t.Errorf("тихие часы = %s, want 22:00", got.QuietFrom)
	}

	// Испорченный jsonb не должен ронять тенанта.
	if got := tenant.ParseSettings([]byte(`{{{`)); got.DigestAt.String() != "09:00" {
		t.Errorf("мусор должен давать настройки по умолчанию, got %s", got.DigestAt)
	}
}

func TestLoadLocation(t *testing.T) {
	if got := tenant.LoadLocation("Europe/Moscow"); got.String() != "Europe/Moscow" {
		t.Errorf("LoadLocation = %v", got)
	}
	// Опечатка в настройках не должна ронять тенанта целиком.
	if got := tenant.LoadLocation("Мордор/Барад-Дур"); got != time.UTC {
		t.Errorf("неизвестный пояс = %v, want UTC", got)
	}
	if got := tenant.LoadLocation(""); got != time.UTC {
		t.Errorf("пустой пояс = %v, want UTC", got)
	}
}

func TestContext(t *testing.T) {
	if _, err := tenant.FromContext(context.Background()); !errors.Is(err, tenant.ErrNoTenant) {
		t.Errorf("пустой контекст должен давать ErrNoTenant, got %v", err)
	}

	want := tenant.Tenant{Name: "Кофейня «Демо»", Location: time.UTC}
	ctx := tenant.WithTenant(context.Background(), want)

	got, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if tenant.MustFromContext(ctx).Name != want.Name {
		t.Error("MustFromContext вернул не тот тенант")
	}
}

func TestMustFromContext_ПаникаБезТенанта(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustFromContext должен паниковать без тенанта")
		}
	}()
	tenant.MustFromContext(context.Background())
}
