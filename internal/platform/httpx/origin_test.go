package httpx_test

import (
	"testing"

	"github.com/vostapenko/zapas/internal/platform/httpx"
)

// TestSameOrigin_АдресСПутём — приложение живёт на vitalness.ru/zapas,
// а браузер шлёт Origin без пути. Сравнение строк целиком давало 403 на
// вход, на каждое изменение и на создание демо.
func TestSameOrigin_АдресСПутём(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		base   string
		want   bool
	}{
		{"свой сайт, база с путём", "https://vitalness.ru", "https://vitalness.ru/zapas", true},
		{"свой сайт, база со слешем", "https://vitalness.ru", "https://vitalness.ru/zapas/", true},
		{"свой сайт, база без пути", "https://vitalness.ru", "https://vitalness.ru", true},
		{"регистр не важен", "https://Vitalness.RU", "https://vitalness.ru/zapas", true},
		{"локальная разработка", "http://localhost:5173", "http://localhost:5173/zapas", true},
		{"чужой сайт", "https://evil.example", "https://vitalness.ru/zapas", false},
		{"поддомен — чужой сайт", "https://www.vitalness.ru", "https://vitalness.ru/zapas", false},
		{"другая схема", "http://vitalness.ru", "https://vitalness.ru/zapas", false},
		{"другой порт", "https://vitalness.ru:8443", "https://vitalness.ru/zapas", false},
		{"Origin с путём не бывает своим", "https://vitalness.ru/zapas", "https://vitalness.ru/zapas", false},
		{"null из песочницы браузера", "null", "https://vitalness.ru/zapas", false},
		{"база без схемы", "https://vitalness.ru", "vitalness.ru/zapas", false},
		{"база-мусор", "https://vitalness.ru", "://", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpx.SameOrigin(tt.origin, tt.base); got != tt.want {
				t.Errorf("SameOrigin(%q, %q) = %v, want %v", tt.origin, tt.base, got, tt.want)
			}
		})
	}
}

func TestBasePath(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"https://vitalness.ru/zapas", "/zapas"},
		{"https://vitalness.ru/zapas/", "/zapas"},
		{"https://vitalness.ru", ""},
		{"https://vitalness.ru/", ""},
		{"http://localhost:5173/zapas", "/zapas"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := httpx.BasePath(tt.base); got != tt.want {
			t.Errorf("BasePath(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}
