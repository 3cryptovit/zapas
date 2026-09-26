package notify

import (
	"testing"

	"github.com/google/uuid"
)

// TestСсылкиПодПутём — Zapas живёт на vitalness.ru/zapas. Ссылка из
// сводки в Telegram должна вести в кабинет под этим путём, а не в корень
// домена, где портфолио ответит 404.
func TestСсылкиПодПутём(t *testing.T) {
	s := &Service{baseURL: "https://vitalness.ru/zapas"}
	id := uuid.MustParse("01a0dd53-4f12-72f2-9332-32cc3f56d368")

	tests := map[string]struct{ got, want string }{
		"дашборд": {s.dashboardLink(), "https://vitalness.ru/zapas/app/"},
		"позиция": {s.itemLink(id), "https://vitalness.ru/zapas/app/items/" + id.String()},
		"заказ":   {s.orderLink(id), "https://vitalness.ru/zapas/app/orders/" + id.String()},
	}
	for name, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: %q, want %q", name, tt.got, tt.want)
		}
	}
}
