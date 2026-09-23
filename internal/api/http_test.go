package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/api"
	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/config"
	"github.com/vostapenko/zapas/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }

// client — тонкая обёртка над httptest: держит cookie сессии и CSRF-токен,
// как это делает браузер.
type client struct {
	t      *testing.T
	server *httptest.Server
	jar    map[string]string
}

func newClient(t *testing.T) (*client, *testsupport.Fixture) {
	t.Helper()
	env := testsupport.Shared(t)
	f := env.NewTenant(t, "Кофейня «Демо»")

	app := api.New(api.Deps{
		Config: config.Config{
			App:     config.App{Env: "test", BaseURL: ""},
			Session: config.Session{TTL: testSessionTTL},
		},
		// Логи тестов не нужны: падения видно и так.
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:    env.App,
		Maint: env.Maint,
		Clock: env.Clock,
	})

	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	return &client{t: t, server: server, jar: map[string]string{}}, f
}

const testSessionTTL = 14 * 24 * 60 * 60 * 1e9 // 14 дней

func (c *client) do(method, path string, body any, headers map[string]string) *http.Response {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, c.server.URL+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range c.jar {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	// Браузер копирует CSRF-токен из cookie в заголовок (double-submit).
	if csrf, ok := c.jar[auth.CSRFCookie]; ok {
		req.Header.Set(auth.CSRFHeader, csrf)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.server.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.MaxAge < 0 {
			delete(c.jar, cookie.Name)
			continue
		}
		c.jar[cookie.Name] = cookie.Value
	}
	return resp
}

// peer заводит второго клиента к тому же приложению: нужен, когда
// в сценарии участвуют и владелец, и сотрудник.
func (c *client) peer() *client {
	return &client{t: c.t, server: c.server, jar: map[string]string{}}
}

func (c *client) post(path string, body any) *http.Response {
	return c.do(http.MethodPost, path, body, nil)
}

// postIdem добавляет обязательный для движений ключ идемпотентности.
func (c *client) postIdem(path string, body any, key string) *http.Response {
	return c.do(http.MethodPost, path, body, map[string]string{"Idempotency-Key": key})
}

// doRaw шлёт тело как есть: так загружается CSV.
func (c *client) doRaw(path, body string) *http.Response {
	c.t.Helper()

	req, err := http.NewRequest(http.MethodPost, c.server.URL+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/csv")
	for name, value := range c.jar {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	if csrf, ok := c.jar[auth.CSRFCookie]; ok {
		req.Header.Set(auth.CSRFHeader, csrf)
	}

	resp, err := c.server.Client().Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func (c *client) get(path string) *http.Response {
	return c.do(http.MethodGet, path, nil, nil)
}

func (c *client) login(f *testsupport.Fixture, email string) {
	c.t.Helper()
	resp := c.post("/api/v1/auth/login", map[string]string{
		"email":    email,
		"password": testsupport.Password,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("вход: %d %s", resp.StatusCode, readBody(c.t, resp))
	}
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()

	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return out
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func requireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("статус %d, want %d: %s", resp.StatusCode, want, readBody(t, resp))
	}
}

// --- тесты ---

func TestВходИВыход(t *testing.T) {
	c, f := newClient(t)

	// Без сессии защищённый ресурс отдаёт 401.
	resp := c.get("/api/v1/items")
	requireStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	c.login(f, f.OwnerEmail)

	me := decode[map[string]any](t, c.get("/api/v1/me"))
	user := me["user"].(map[string]any)
	if user["role"] != "owner" {
		t.Errorf("роль = %v, want owner", user["role"])
	}
	tenantInfo := me["tenant"].(map[string]any)
	if tenantInfo["name"] != "Кофейня «Демо»" {
		t.Errorf("тенант = %v", tenantInfo["name"])
	}
	// Виртуальная дата отдаётся всем: для рабочего тенанта она настоящая.
	if tenantInfo["today"] != "2026-09-23" {
		t.Errorf("сегодня = %v, want 2026-09-23", tenantInfo["today"])
	}

	resp = c.post("/api/v1/auth/logout", nil)
	requireStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// После выхода сессия не действует немедленно (ADR-007).
	resp = c.get("/api/v1/me")
	requireStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestВход_НеверныйПарольИНесуществующийEmail_ОдинаковыйОтвет(t *testing.T) {
	c, f := newClient(t)

	wrongPassword := c.post("/api/v1/auth/login", map[string]string{
		"email": f.OwnerEmail, "password": "неправильный-пароль",
	})
	unknownEmail := c.post("/api/v1/auth/login", map[string]string{
		"email": "нет-такого@example.test", "password": testsupport.Password,
	})

	if wrongPassword.StatusCode != unknownEmail.StatusCode {
		t.Errorf("статусы разошлись: %d и %d", wrongPassword.StatusCode, unknownEmail.StatusCode)
	}
	if wrongPassword.StatusCode != http.StatusUnauthorized {
		t.Errorf("статус = %d, want 401", wrongPassword.StatusCode)
	}
	// Тексты тоже одинаковые: по ним нельзя перебрать, кто зарегистрирован.
	if readBody(t, wrongPassword) != readBody(t, unknownEmail) {
		t.Error("ответы различаются — можно перебрать существующие адреса")
	}
}

func TestCSRF_БезЗаголовкаЗапросОтклоняется(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	// Запрос без X-CSRF-Token: так выглядит подделка со стороннего сайта.
	req, err := http.NewRequest(http.MethodPost, c.server.URL+"/api/v1/categories",
		bytes.NewReader([]byte(`{"name":"Молочка"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.jar[auth.SessionCookie]})

	resp, err := c.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	requireStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Чтение при этом работает: CSRF проверяется только на изменяющих запросах.
	read := c.get("/api/v1/categories")
	requireStatus(t, read, http.StatusOK)
	read.Body.Close()
}

func TestРоли_СотрудникНеМеняетКаталог(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.StaffEmail)

	resp := c.post("/api/v1/items", map[string]any{"name": "Молоко", "base_unit": "l"})
	requireStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Читать номенклатуру сотрудник может.
	list := c.get("/api/v1/items")
	requireStatus(t, list, http.StatusOK)
	list.Body.Close()
}

// TestСценарийСмены проходит путь из §2: приход, расход, попытка списать
// больше остатка, сторно, журнал.
func TestСценарийСмены(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	item := decode[map[string]any](t, c.post("/api/v1/items", map[string]any{
		"name": "Молоко 3,2%", "base_unit": "l",
	}))
	itemID := item["id"].(string)
	if item["unit_label"] != "л" {
		t.Errorf("единица = %v, want л", item["unit_label"])
	}

	// Приход 24 л.
	resp := c.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": itemID, "qty": "24",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	created := decode[map[string]any](t, resp)
	if balance := created["balance"].(map[string]any); balance["on_hand"] != "24.000" {
		t.Errorf("остаток = %v, want 24.000", balance["on_hand"])
	}

	// Расход 1,5 л: количество приходит положительным, знак ставит сервер.
	resp = c.postIdem("/api/v1/movements", map[string]any{
		"type": "usage", "item_id": itemID, "qty": "1.5",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	used := decode[map[string]any](t, resp)
	if movement := used["movement"].(map[string]any); movement["qty"] != "-1.500" {
		t.Errorf("расход = %v, want -1.500", movement["qty"])
	}
	usedID := used["movement"].(map[string]any)["id"].(string)

	// Списание больше остатка → 409 с текущим остатком (FR-8).
	resp = c.postIdem("/api/v1/movements", map[string]any{
		"type": "writeoff", "item_id": itemID, "qty": "100", "reason": "spoiled",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusConflict)
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	problem := decode[map[string]any](t, resp)
	if problem["type"] != "/errors/insufficient-stock" {
		t.Errorf("тип ошибки = %v", problem["type"])
	}
	if problem["on_hand"] != "22.500" {
		t.Errorf("в ошибке остаток = %v, want 22.500", problem["on_hand"])
	}

	// Сторно ошибочного расхода возвращает остаток.
	resp = c.postIdem("/api/v1/movements/"+usedID+"/reverse", nil, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	reversed := decode[map[string]any](t, resp)
	if balance := reversed["balance"].(map[string]any); balance["on_hand"] != "24.000" {
		t.Errorf("остаток после сторно = %v, want 24.000", balance["on_hand"])
	}

	// Журнал: приход, расход и сторно.
	journal := decode[map[string]any](t, c.get("/api/v1/movements?item_id="+itemID))
	items := journal["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("в журнале %d движений, want 3", len(items))
	}
	// Свежие сверху.
	if first := items[0].(map[string]any); first["type"] != "reversal" {
		t.Errorf("сверху журнала %v, want reversal", first["type"])
	}
}

func TestИдемпотентность_ПовторОтдаёт200(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	item := decode[map[string]any](t, c.post("/api/v1/items", map[string]any{
		"name": "Сливки 33%", "base_unit": "l",
	}))
	itemID := item["id"].(string)

	key := uuid.NewString()
	body := map[string]any{"type": "receipt", "item_id": itemID, "qty": "10"}

	first := c.postIdem("/api/v1/movements", body, key)
	requireStatus(t, first, http.StatusCreated)
	firstID := decode[map[string]any](t, first)["movement"].(map[string]any)["id"]

	// Повтор — 200, а не 201: ничего нового не создано.
	second := c.postIdem("/api/v1/movements", body, key)
	requireStatus(t, second, http.StatusOK)
	secondBody := decode[map[string]any](t, second)
	if secondBody["movement"].(map[string]any)["id"] != firstID {
		t.Error("повтор вернул другое движение")
	}
	if balance := secondBody["balance"].(map[string]any); balance["on_hand"] != "10.000" {
		t.Errorf("остаток = %v, want 10.000: приход записался дважды", balance["on_hand"])
	}
}

func TestДвижениеБезКлючаИдемпотентности(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	item := decode[map[string]any](t, c.post("/api/v1/items", map[string]any{
		"name": "Сахар", "base_unit": "kg",
	}))

	resp := c.post("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": item["id"], "qty": "5",
	})
	requireStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestВалидация_ОшибкиПоПолям(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	resp := c.post("/api/v1/items", map[string]any{"name": "", "base_unit": "тонна"})
	requireStatus(t, resp, http.StatusUnprocessableEntity)

	problem := decode[map[string]any](t, resp)
	errs, ok := problem["errors"].([]any)
	if !ok || len(errs) != 2 {
		t.Fatalf("ожидались ошибки по двум полям: %v", problem)
	}
	fields := map[string]bool{}
	for _, e := range errs {
		fields[e.(map[string]any)["field"].(string)] = true
	}
	if !fields["name"] || !fields["base_unit"] {
		t.Errorf("ошибки не по тем полям: %v", fields)
	}
}

// TestИзоляция_ЧужойРесурсДаёт404: существование объекта другого тенанта
// не раскрывается (§11).
func TestИзоляция_ЧужойРесурсДаёт404(t *testing.T) {
	env := testsupport.Shared(t)
	a := env.NewTenant(t, "Тенант A")
	b := env.NewTenant(t, "Тенант B")

	app := api.New(api.Deps{
		Config: config.Config{Session: config.Session{TTL: testSessionTTL}},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:     env.App,
		Maint:  env.Maint,
		Clock:  env.Clock,
	})
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	ca := &client{t: t, server: server, jar: map[string]string{}}
	cb := &client{t: t, server: server, jar: map[string]string{}}
	ca.login(a, a.OwnerEmail)
	cb.login(b, b.OwnerEmail)

	itemA := decode[map[string]any](t, ca.post("/api/v1/items", map[string]any{
		"name": "Молоко A", "base_unit": "l",
	}))
	idA := itemA["id"].(string)

	// Тенант B получает 404, а не 403: сам факт существования не раскрывается.
	resp := cb.get("/api/v1/items/" + idA)
	requireStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Движение по чужой позиции — тоже 404.
	resp = cb.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": idA, "qty": "1",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// В списке B чужих позиций нет.
	list := decode[map[string]any](t, cb.get("/api/v1/items"))
	if items := list["items"].([]any); len(items) != 0 {
		t.Errorf("тенанту B видно %d чужих позиций", len(items))
	}
}

// TestИнвентаризация проходит путь «конец смены» из §2.
func TestИнвентаризация(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	item := decode[map[string]any](t, c.post("/api/v1/items", map[string]any{
		"name": "Зерно Бразилия", "base_unit": "kg",
	}))
	itemID := item["id"].(string)

	resp := c.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": itemID, "qty": "10",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	count := decode[map[string]any](t, c.post("/api/v1/counts", map[string]any{
		"scope": "all", "note": "Конец смены",
	}))
	countID := count["id"].(string)

	// Намерили 9,4 кг вместо учётных 10.
	updated := decode[map[string]any](t, c.do(http.MethodPut,
		"/api/v1/counts/"+countID+"/lines", map[string]any{
			"lines": []map[string]any{{"item_id": itemID, "counted_qty": "9.4"}},
		}, nil))
	lines := updated["lines"].([]any)
	if diff := lines[0].(map[string]any)["diff"]; diff != "-0.600" {
		t.Errorf("расхождение = %v, want -0.600", diff)
	}

	posted := decode[map[string]any](t, c.postIdem(
		"/api/v1/counts/"+countID+"/post", nil, uuid.NewString()))
	if posted["status"] != "posted" {
		t.Errorf("статус = %v, want posted", posted["status"])
	}

	// Проведение создало корректировку, остаток стал равен факту.
	journal := decode[map[string]any](t, c.get("/api/v1/movements?item_id="+itemID+"&type=adjustment"))
	adjustments := journal["items"].([]any)
	if len(adjustments) != 1 {
		t.Fatalf("корректировок %d, want 1", len(adjustments))
	}
	if qty := adjustments[0].(map[string]any)["qty"]; qty != "-0.600" {
		t.Errorf("корректировка = %v, want -0.600", qty)
	}
}

func TestНеизвестныйМаршрутОтдаётProblemJSON(t *testing.T) {
	c, _ := newClient(t)

	resp := c.get("/api/v1/такого-нет")
	requireStatus(t, resp, http.StatusNotFound)
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	problem := decode[map[string]any](t, resp)
	for _, field := range []string{"type", "title", "status"} {
		if _, ok := problem[field]; !ok {
			t.Errorf("в ответе нет обязательного поля %q: %v", field, problem)
		}
	}
	if fmt.Sprint(problem["status"]) != "404" {
		t.Errorf("status в теле = %v, want 404", problem["status"])
	}
}
