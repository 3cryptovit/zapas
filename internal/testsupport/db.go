// Package testsupport поднимает изолированную базу для интеграционных тестов.
//
// Тесты идут на настоящем PostgreSQL, а не на моках: половина гарантий системы
// живёт в самой БД — RLS, CHECK на неотрицательный остаток, блокировка строки
// остатка, отозванные права на UPDATE журнала. На заглушках это не проверить.
//
// Адрес кластера берётся из TEST_DATABASE_ADMIN_URL; по умолчанию —
// тот, что поднимает deploy/docker-compose.yml.
package testsupport

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/migrations"
)

const defaultAdminURL = "postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable"

// Env — окружение теста: две роли подключения и застывшие часы.
type Env struct {
	// App — роль приложения: RLS действует, права на UPDATE журнала отозваны.
	App *postgres.DB
	// Maint — обслуживающая роль (BYPASSRLS): вход, сессии, ночные задачи.
	Maint *postgres.DB
	// Clock — застывшие часы: тесты не зависят от того, который сейчас час.
	Clock *clock.Fixed

	adminURL string
	dbName   string
	skip     string
}

var shared *Env

// Run поднимает базу один раз на пакет, прогоняет тесты и убирает за собой.
//
// Пакету с интеграционными тестами достаточно одной строки:
//
//	func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }
//
// Изоляцию внутри пакета дают тенанты, а не отдельные базы: миграции на каждый
// тест стоят полсекунды и ничего не проверяют сверх уже проверенного.
func Run(m *testing.M) int {
	env, err := newEnv()
	if err != nil {
		// Без базы интеграционные тесты пропускаются, а не падают: `go test ./...`
		// должен работать и на машине без поднятого окружения.
		shared = &Env{skip: err.Error()}
		return m.Run()
	}
	shared = env
	code := m.Run()
	env.drop()
	return code
}

// Shared отдаёт общее окружение пакета. Если базы нет, тест пропускается.
func Shared(t *testing.T) *Env {
	t.Helper()
	if shared == nil {
		t.Fatal("testsupport: не вызван testsupport.Run из TestMain")
	}
	if shared.skip != "" {
		t.Skipf("нет доступа к PostgreSQL (%s). Подними окружение: make up", shared.skip)
	}
	return shared
}

// NewEnv создаёт отдельную базу под конкретный тест. Нужен там, где тест
// меняет саму схему или права; в обычном случае хватает Shared.
func NewEnv(t *testing.T) *Env {
	t.Helper()
	env, err := newEnv()
	if err != nil {
		t.Skipf("нет доступа к PostgreSQL (%v). Подними окружение: make up", err)
	}
	t.Cleanup(env.drop)
	return env
}

func newEnv() (*Env, error) {
	adminURL := os.Getenv("TEST_DATABASE_ADMIN_URL")
	if adminURL == "" {
		adminURL = defaultAdminURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = admin.Close(ctx) }()

	// Роли обычно заводит init-скрипт кластера, но в CI postgres поднимается
	// как service и до скриптов не доходит. Создаём их идемпотентно.
	if err := ensureRoles(ctx, admin); err != nil {
		return nil, err
	}

	dbName := "zapas_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{dbName}.Sanitize())); err != nil {
		return nil, fmt.Errorf("создание тестовой базы: %w", err)
	}

	env := &Env{
		Clock:    clock.NewFixed(time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)),
		adminURL: adminURL,
		dbName:   dbName,
	}

	// Повторяем раскладку прав из deploy/postgres/initdb/10-roles.sh:
	// владелец схемы отдельно, роль приложения без BYPASSRLS.
	setup, err := pgx.Connect(ctx, replaceDB(adminURL, dbName))
	if err != nil {
		env.drop()
		return nil, fmt.Errorf("подключение к тестовой базе: %w", err)
	}
	for _, stmt := range []string{
		"ALTER SCHEMA public OWNER TO zapas_owner",
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
		"GRANT USAGE ON SCHEMA public TO zapas_app, zapas_maint",
	} {
		if _, err := setup.Exec(ctx, stmt); err != nil {
			_ = setup.Close(ctx)
			env.drop()
			return nil, fmt.Errorf("настройка прав (%s): %w", stmt, err)
		}
	}
	_ = setup.Close(ctx)

	// Миграции в тестах молчат: их вывод забивает результаты.
	goose.SetLogger(goose.NopLogger())
	if err := postgres.Migrate(ctx, withCredentials(adminURL, "zapas_owner", "zapas_owner", dbName), migrations.FS); err != nil {
		env.drop()
		return nil, fmt.Errorf("миграции: %w", err)
	}

	if env.App, err = postgres.Connect(ctx, withCredentials(adminURL, "zapas_app", "zapas_app", dbName), 16); err != nil {
		env.drop()
		return nil, fmt.Errorf("пул приложения: %w", err)
	}
	if env.Maint, err = postgres.Connect(ctx, withCredentials(adminURL, "zapas_maint", "zapas_maint", dbName), 4); err != nil {
		env.App.Close()
		env.drop()
		return nil, fmt.Errorf("обслуживающий пул: %w", err)
	}
	return env, nil
}

func (e *Env) drop() {
	if e == nil || e.dbName == "" {
		return
	}
	if e.App != nil {
		e.App.Close()
	}
	if e.Maint != nil {
		e.Maint.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, e.adminURL)
	if err != nil {
		return
	}
	defer func() { _ = admin.Close(ctx) }()
	_, _ = admin.Exec(ctx,
		fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{e.dbName}.Sanitize()))
}

func replaceDB(dsn, dbName string) string {
	base, _, _ := strings.Cut(dsn, "?")
	i := strings.LastIndex(base, "/")
	return base[:i+1] + dbName + "?sslmode=disable"
}

func withCredentials(dsn, user, password, dbName string) string {
	base, _, _ := strings.Cut(dsn, "?")
	rest := base[strings.Index(base, "://")+3:]
	hostPart := rest
	if at := strings.LastIndex(rest, "@"); at != -1 {
		hostPart = rest[at+1:]
	}
	if slash := strings.Index(hostPart, "/"); slash != -1 {
		hostPart = hostPart[:slash]
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", user, password, hostPart, dbName)
}

// ensureRoles создаёт роли кластера, если их ещё нет. Повторяет
// deploy/postgres/initdb/10-roles.sh: приложение без BYPASSRLS, обслуживающая
// роль с ним.
func ensureRoles(ctx context.Context, admin *pgx.Conn) error {
	roles := []struct {
		name    string
		options string
	}{
		{"zapas_owner", "LOGIN PASSWORD 'zapas_owner' NOBYPASSRLS"},
		{"zapas_app", "LOGIN PASSWORD 'zapas_app' NOBYPASSRLS"},
		{"zapas_maint", "LOGIN PASSWORD 'zapas_maint' BYPASSRLS"},
	}
	for _, r := range roles {
		stmt := fmt.Sprintf(`DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
        CREATE ROLE %s %s;
    END IF;
END$$;`, r.name, r.name, r.options)
		if _, err := admin.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("создание роли %s: %w", r.name, err)
		}
	}
	return nil
}
