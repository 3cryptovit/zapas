package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vostapenko/zapas/internal/platform/config"
)

type guidedCreated struct {
	TenantID string `json:"tenant_id"`
	Login    *struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	} `json:"login"`
}

type guideState struct {
	Current string `json:"current"`
	Filled  int    `json:"filled"`
	Steps   []struct {
		ID    string `json:"id"`
		Done  bool   `json:"done"`
		Count int    `json:"count"`
		Ready bool   `json:"ready"`
	} `json:"steps"`
}

type problem struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// TestДемо_ПошаговоеВходПоВыданномуПаролю — логин и пароль, показанные
// посетителю, действительно пускают в его демо из другого браузера.
func TestДемо_ПошаговоеВходПоВыданномуПаролю(t *testing.T) {
	c, _ := newClient(t)

	resp := c.post("/api/v1/sandbox", map[string]any{"mode": "guided"})
	requireStatus(t, resp, http.StatusCreated)
	created := decode[guidedCreated](t, resp)
	if created.Login == nil || created.Login.Email == "" || created.Login.Password == "" {
		t.Fatalf("в ответе нет логина: %+v", created)
	}

	other := c.peer()
	login := other.post("/api/v1/auth/login", map[string]string{
		"email": created.Login.Email, "password": created.Login.Password,
	})
	requireStatus(t, login, http.StatusOK)
	login.Body.Close()

	me := decode[map[string]any](t, other.get("/api/v1/me"))
	if tenant, _ := me["tenant"].(map[string]any); tenant["id"] != created.TenantID {
		t.Errorf("вошли не в своё демо: %v, want %s", me["tenant"], created.TenantID)
	}

	state := decode[guideState](t, other.get("/api/v1/sandbox/guide"))
	if state.Current != "suppliers" || len(state.Steps) != 9 {
		t.Errorf("стартовое состояние: current=%q, шагов %d", state.Current, len(state.Steps))
	}

	// Повторный клик: то же демо, пароль второй раз не отдаётся.
	again := c.post("/api/v1/sandbox", map[string]any{"mode": "guided"})
	requireStatus(t, again, http.StatusOK)
	if reused := decode[guidedCreated](t, again); reused.Login != nil || reused.TenantID != created.TenantID {
		t.Errorf("повторный клик: %+v", reused)
	}
}

// TestДемо_ШагиЧерезHTTP — порядок шагов, неизвестный шаг и наполнение.
func TestДемо_ШагиЧерезHTTP(t *testing.T) {
	c, _ := newClient(t)
	requireStatus(t, c.post("/api/v1/sandbox", map[string]any{"mode": "guided"}), http.StatusCreated)

	resp := c.post("/api/v1/sandbox/guide/items/fill", nil)
	requireStatus(t, resp, http.StatusConflict)
	p := decode[problem](t, resp)
	if !strings.HasSuffix(p.Type, "step-order") || !strings.Contains(p.Detail, "Поставщики") {
		t.Errorf("не по порядку: %+v", p)
	}

	resp = c.post("/api/v1/sandbox/guide/nope/fill", nil)
	requireStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	resp = c.post("/api/v1/sandbox/guide/time/fill", nil)
	requireStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	resp = c.post("/api/v1/sandbox/guide/suppliers/fill", nil)
	requireStatus(t, resp, http.StatusOK)
	state := decode[guideState](t, resp)
	if state.Filled != 4 || state.Current != "items" {
		t.Errorf("после поставщиков: filled=%d current=%q", state.Filled, state.Current)
	}
}

// TestДемо_РежимПроверяется — опечатка в режиме не должна молча давать
// готовое демо.
func TestДемо_РежимПроверяется(t *testing.T) {
	c, _ := newClient(t)
	resp := c.post("/api/v1/sandbox", map[string]any{"mode": "guidde"})
	requireStatus(t, resp, http.StatusUnprocessableEntity)
	resp.Body.Close()
}

// TestДемо_АварийноВыключено — рубильник из раннбука. Раньше рецепт был
// SANDBOX_MAX_ACTIVE=0, но ноль означал «по умолчанию», и демо не
// выключалось.
func TestДемо_АварийноВыключено(t *testing.T) {
	c, _ := newClientWith(t, func(cfg *config.Config) { cfg.Sandbox.Disabled = true })

	resp := c.post("/api/v1/sandbox", map[string]any{})
	requireStatus(t, resp, http.StatusServiceUnavailable)
	if p := decode[problem](t, resp); !strings.HasSuffix(p.Type, "sandbox-disabled") {
		t.Errorf("тип ответа %q", p.Type)
	}
}
