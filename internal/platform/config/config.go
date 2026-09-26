// Package config читает настройки приложения из переменных окружения.
//
// Секретов в репозитории нет: локально их подставляет .env (см. .env.example),
// в проде — окружение контейнера (§12.2).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App     App
	DB      DB
	Redis   Redis
	Session Session
	Notify  Notify
	Sandbox Sandbox
	Observ  Observability
}

type App struct {
	Env  string // dev | prod
	Addr string
	// BaseURL — публичный адрес вместе с путём: https://vitalness.ru/zapas.
	BaseURL string
}

func (a App) IsProd() bool { return a.Env == "prod" }

type DB struct {
	// URL — роль приложения: без BYPASSRLS, не владелец таблиц. Весь
	// пользовательский трафик идёт только через неё.
	URL string
	// MigrateURL — роль владельца схемы, только для goose.
	MigrateURL string
	// MaintURL — обслуживающая роль (BYPASSRLS): вход по email, разбор сессии,
	// обход тенантов ночными задачами, удаление истёкших песочниц (ADR-005).
	MaintURL string
	MaxConns int32
}

type Redis struct {
	Addr string
}

type Session struct {
	TTL           time.Duration
	SecureCookies bool
}

type Notify struct {
	TelegramBotToken      string
	TelegramBotUsername   string
	TelegramWebhookSecret string
	SMTPAddr              string
	SMTPFrom              string
}

type Sandbox struct {
	TTL         time.Duration
	MaxActive   int
	PerIPHourly int
}

type Observability struct {
	SentryDSN   string
	LogLevel    string
	MetricsAddr string
}

// Load собирает конфиг и падает на старте, если обязательного значения нет:
// приложение, поднявшееся без DATABASE_URL, всё равно нерабочее.
func Load() (Config, error) {
	var missing []string
	required := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	cfg := Config{
		App: App{
			Env:  def(os.Getenv("APP_ENV"), "dev"),
			Addr: def(os.Getenv("APP_ADDR"), ":8080"),
			// Слеш на конце срезается: к адресу дописывают "/app/...", и
			// "zapas//app" роутер кабинета уже не узнаёт.
			BaseURL: strings.TrimRight(def(os.Getenv("APP_BASE_URL"), "http://localhost:5173/zapas"), "/"),
		},
		DB: DB{
			URL:      required("DATABASE_URL"),
			MaxConns: int32(intDef(os.Getenv("DB_MAX_CONNS"), 10)),
		},
		Redis: Redis{
			Addr: def(os.Getenv("REDIS_ADDR"), "localhost:6379"),
		},
		Session: Session{
			TTL:           durDef(os.Getenv("SESSION_TTL"), 14*24*time.Hour),
			SecureCookies: boolDef(os.Getenv("SECURE_COOKIES"), false),
		},
		Notify: Notify{
			TelegramBotToken:      os.Getenv("TELEGRAM_BOT_TOKEN"),
			TelegramBotUsername:   os.Getenv("TELEGRAM_BOT_USERNAME"),
			TelegramWebhookSecret: os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
			SMTPAddr:              def(os.Getenv("SMTP_ADDR"), "localhost:1025"),
			SMTPFrom:              def(os.Getenv("SMTP_FROM"), "zapas@example.com"),
		},
		Sandbox: Sandbox{
			TTL:         durDef(os.Getenv("SANDBOX_TTL"), 24*time.Hour),
			MaxActive:   intDef(os.Getenv("SANDBOX_MAX_ACTIVE"), 300),
			PerIPHourly: intDef(os.Getenv("SANDBOX_PER_IP_HOURLY"), 5),
		},
		Observ: Observability{
			SentryDSN:   os.Getenv("SENTRY_DSN"),
			LogLevel:    def(os.Getenv("LOG_LEVEL"), "info"),
			MetricsAddr: def(os.Getenv("METRICS_ADDR"), ":9090"),
		},
	}
	// Миграции идут от роли-владельца, обслуживание — от роли с BYPASSRLS.
	// Если отдельные DSN не заданы (локальная отладка), падаем на основной.
	cfg.DB.MigrateURL = def(os.Getenv("MIGRATE_DATABASE_URL"), cfg.DB.URL)
	cfg.DB.MaintURL = def(os.Getenv("MAINT_DATABASE_URL"), cfg.DB.URL)

	if cfg.App.IsProd() && !cfg.Session.SecureCookies {
		return Config{}, fmt.Errorf("config: SECURE_COOKIES=false в проде запрещён")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("config: не заданы переменные: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func def(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func intDef(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func durDef(v string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func boolDef(v string, fallback bool) bool {
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
