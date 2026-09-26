package config_test

import (
	"testing"

	"github.com/vostapenko/zapas/internal/platform/config"
)

// TestLoad_БазовыйАдресБезСлешаНаКонце — к адресу дописывают "/app/...".
// Со слешем в окружении ссылки из уведомлений становились "zapas//app/…",
// и роутер кабинета их не узнавал.
func TestLoad_БазовыйАдресБезСлешаНаКонце(t *testing.T) {
	tests := []struct{ env, want string }{
		{"https://vitalness.ru/zapas/", "https://vitalness.ru/zapas"},
		{"https://vitalness.ru/zapas", "https://vitalness.ru/zapas"},
		{"https://vitalness.ru/", "https://vitalness.ru"},
	}
	for _, tt := range tests {
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("APP_BASE_URL", tt.env)

		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.App.BaseURL != tt.want {
			t.Errorf("APP_BASE_URL=%q → %q, want %q", tt.env, cfg.App.BaseURL, tt.want)
		}
	}
}
