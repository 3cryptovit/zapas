package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// setupShop заводит поставщика, позицию и условия закупки через API —
// так же, как это делает владелец в настройках.
func setupShop(t *testing.T, c *client) (itemID, supplierID string) {
	t.Helper()

	supplier := decode[map[string]any](t, c.post("/api/v1/suppliers", map[string]any{
		"name":              "Молочная ферма",
		"contact":           "@dairy",
		"lead_time_days":    1,
		"delivery_weekdays": []int{1, 4},
		"order_cutoff":      "16:00",
	}))
	supplierID = supplier["id"].(string)

	item := decode[map[string]any](t, c.post("/api/v1/items", map[string]any{
		"name":                "Молоко 3,2%",
		"base_unit":           "l",
		"default_supplier_id": supplierID,
		"manual_min_qty":      "20",
	}))
	itemID = item["id"].(string)

	resp := c.do(http.MethodPut, "/api/v1/suppliers/"+supplierID+"/items/"+itemID, map[string]any{
		"purchase_unit": "кор.",
		"unit_factor":   "12",
		"pack_multiple": "12",
	}, nil)
	requireStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	return itemID, supplierID
}

func TestДашборд_СчётчикиИТаблица(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	itemID, _ := setupShop(t, c)

	// Приход 24 л — остатка больше ручного минимума.
	resp := c.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": itemID, "qty": "24",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	view := decode[map[string]any](t, c.get("/api/v1/dashboard"))

	// Виртуальная дата тенанта — по ней интерфейс считает «через 2 дня».
	if view["today"] != "2026-09-23" {
		t.Errorf("today = %v, want 2026-09-23", view["today"])
	}

	counters := view["counters"].(map[string]any)
	if counters["total"].(float64) != 1 {
		t.Errorf("всего позиций = %v, want 1", counters["total"])
	}

	items := view["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("строк в таблице %d, want 1", len(items))
	}
	row := items[0].(map[string]any)
	if row["on_hand"] != "24.000" {
		t.Errorf("остаток = %v, want 24.000", row["on_hand"])
	}
	if row["supplier_name"] != "Молочная ферма" {
		t.Errorf("поставщик = %v", row["supplier_name"])
	}
	// Прогноза ещё нет — модель M0, точность не показывается.
	if _, hasAccuracy := row["accuracy"]; hasAccuracy {
		t.Error("при модели M0 точность показывать нечего")
	}
}

func TestДашборд_ОстатокНижеМинимумаПопадаетВЗаказатьСегодня(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	itemID, supplierID := setupShop(t, c)

	// Остаток 5 л при ручном минимуме 20 л: модель M0 сравнивает с минимумом.
	resp := c.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": itemID, "qty": "5",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	created := decode[map[string]any](t, resp)
	if status := created["status"].(map[string]any); status["code"] != "order_today" {
		t.Errorf("статус в ответе = %v, want order_today", status["code"])
	}

	view := decode[map[string]any](t, c.get("/api/v1/dashboard"))
	counters := view["counters"].(map[string]any)
	if counters["order_today"].(float64) != 1 {
		t.Errorf("счётчик «заказать сегодня» = %v, want 1", counters["order_today"])
	}

	suggestions := view["suggestions"].([]any)
	if len(suggestions) != 1 {
		t.Fatalf("блоков рекомендаций %d, want 1", len(suggestions))
	}
	block := suggestions[0].(map[string]any)
	if block["supplier_id"] != supplierID {
		t.Errorf("поставщик в рекомендации = %v", block["supplier_id"])
	}
	lines := block["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("строк рекомендации %d, want 1", len(lines))
	}
	line := lines[0].(map[string]any)
	// Нехватка 15 л округляется вверх до двух коробок по 12 л.
	if line["qty"] != "24.000" {
		t.Errorf("рекомендовано = %v, want 24.000", line["qty"])
	}
	if line["purchase_qty"] != "2.000" {
		t.Errorf("в единицах закупки = %v, want 2.000", line["purchase_qty"])
	}
}

func TestКарточкаПозиции(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	itemID, _ := setupShop(t, c)
	resp := c.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": itemID, "qty": "24",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	insights := decode[map[string]any](t, c.get("/api/v1/items/"+itemID+"/insights"))

	item := insights["item"].(map[string]any)
	if item["name"] != "Молоко 3,2%" {
		t.Errorf("позиция = %v", item["name"])
	}
	if item["service_level"].(float64) != 95 {
		t.Errorf("уровень сервиса = %v, want 95", item["service_level"])
	}

	status := insights["status"].(map[string]any)
	if status["on_hand"] != "24.000" {
		t.Errorf("остаток = %v, want 24.000", status["on_hand"])
	}
	// Блок «почему такой статус» должен приехать даже при модели M0.
	if _, ok := status["explanation"]; !ok {
		t.Error("нет блока «почему такой статус»")
	}

	accuracy := insights["accuracy"].(map[string]any)
	if accuracy["model"] != "M0" {
		t.Errorf("модель = %v, want M0", accuracy["model"])
	}

	// Списки всегда массивы, а не null: фронтенду не нужно их проверять.
	for _, key := range []string{"history", "forecast", "projection", "incoming"} {
		if _, ok := insights[key].([]any); !ok {
			t.Errorf("поле %q должно быть массивом, got %T", key, insights[key])
		}
	}
}

func TestКарточкаЧужойПозицииДаёт404(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	resp := c.get("/api/v1/items/" + uuid.NewString() + "/insights")
	requireStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

// TestЗаказПоHTTP — путь владельца из §2: оформить, скопировать заявку, принять.
func TestЗаказПоHTTP(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	itemID, supplierID := setupShop(t, c)
	resp := c.postIdem("/api/v1/movements", map[string]any{
		"type": "receipt", "item_id": itemID, "qty": "5",
	}, uuid.NewString())
	requireStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Черновик собирается из рекомендаций одной кнопкой на поставщика.
	order := decode[map[string]any](t, c.postIdem("/api/v1/orders", map[string]any{
		"supplier_id": supplierID,
	}, uuid.NewString()))
	orderID := order["id"].(string)
	if order["status"] != "draft" {
		t.Fatalf("статус = %v, want draft", order["status"])
	}

	// Отправка возвращает текст заявки для копирования (FR-19).
	sent := decode[map[string]any](t, c.postIdem("/api/v1/orders/"+orderID+"/send", nil, uuid.NewString()))
	if sent["status"] != "sent" {
		t.Fatalf("статус = %v, want sent", sent["status"])
	}
	if sent["expected_at"] != "2026-09-24" {
		t.Errorf("ожидаемая поставка = %v, want 2026-09-24", sent["expected_at"])
	}
	if text, _ := sent["text"].(string); text == "" {
		t.Error("текст заявки пустой")
	}

	// После отправки количество «в пути», повторных напоминаний нет.
	view := decode[map[string]any](t, c.get("/api/v1/dashboard"))
	row := view["items"].([]any)[0].(map[string]any)
	if row["on_order"] == "0.000" {
		t.Errorf("в пути = %v, ожидалось положительное количество", row["on_order"])
	}
	if counters := view["counters"].(map[string]any); counters["order_today"].(float64) != 0 {
		t.Errorf("после отправки заказа «заказать сегодня» = %v, want 0", counters["order_today"])
	}

	// Приёмку делает сотрудник — он встречает поставку.
	staff := c.peer()
	staff.login(f, f.StaffEmail)

	received := decode[map[string]any](t, staff.postIdem(
		"/api/v1/orders/"+orderID+"/receive", nil, uuid.NewString()))
	if received["status"] != "received" {
		t.Fatalf("статус = %v, want received", received["status"])
	}

	after := decode[map[string]any](t, c.get("/api/v1/dashboard"))
	afterRow := after["items"].([]any)[0].(map[string]any)
	if afterRow["on_hand"] != "29.000" {
		t.Errorf("остаток после приёмки = %v, want 29.000 (5 + 24)", afterRow["on_hand"])
	}
	if afterRow["on_order"] != "0.000" {
		t.Errorf("после приёмки в пути = %v, want 0.000", afterRow["on_order"])
	}
}

func TestРоли_СотрудникНеОформляетЗаказы(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)
	_, supplierID := setupShop(t, c)

	staff := c.peer()
	staff.login(f, f.StaffEmail)

	resp := staff.postIdem("/api/v1/orders", map[string]any{"supplier_id": supplierID}, uuid.NewString())
	requireStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Читать список заказов сотрудник может: ему нужна приёмка.
	list := staff.get("/api/v1/orders")
	requireStatus(t, list, http.StatusOK)
	list.Body.Close()
}

// TestИмпортCSV — FR-4: сначала предпросмотр с ошибками, потом импорт
// одной транзакцией.
func TestИмпортCSV(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	csv := strings.Join([]string{
		"Название;Категория;Единица;Поставщик;Остаток;Кратность",
		"Молоко 3,2%;Молочка;л;Молочная ферма;24;12",
		"Зерно Бразилия;Кофе;кг;Кофе-Импорт;8;1",
		"Стаканы 350 мл;Упаковка;шт;Упак-Сервис;400;50",
	}, "\n")

	// Предпросмотр ничего не меняет.
	preview := decode[map[string]any](t, c.doRaw(
		"/api/v1/imports/items?dry_run=true", csv))
	if preview["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", preview["dry_run"])
	}
	if preview["items"].(float64) != 3 {
		t.Errorf("позиций в предпросмотре = %v, want 3", preview["items"])
	}

	list := decode[map[string]any](t, c.get("/api/v1/items"))
	if items := list["items"].([]any); len(items) != 0 {
		t.Fatalf("предпросмотр создал %d позиций — он не должен ничего менять", len(items))
	}

	// Настоящий импорт.
	result := decode[map[string]any](t, c.doRaw("/api/v1/imports/items", csv))
	if result["items"].(float64) != 3 {
		t.Errorf("импортировано %v, want 3", result["items"])
	}
	if result["categories"].(float64) != 3 {
		t.Errorf("категорий заведено %v, want 3", result["categories"])
	}
	if result["suppliers"].(float64) != 3 {
		t.Errorf("поставщиков заведено %v, want 3", result["suppliers"])
	}

	// Позиции и начальные остатки на месте.
	view := decode[map[string]any](t, c.get("/api/v1/dashboard"))
	rows := view["items"].([]any)
	if len(rows) != 3 {
		t.Fatalf("на дашборде %d позиций, want 3", len(rows))
	}
	byName := map[string]map[string]any{}
	for _, r := range rows {
		row := r.(map[string]any)
		byName[row["name"].(string)] = row
	}
	if got := byName["Молоко 3,2%"]["on_hand"]; got != "24.000" {
		t.Errorf("остаток молока = %v, want 24.000", got)
	}
	if got := byName["Стаканы 350 мл"]["on_hand"]; got != "400.000" {
		t.Errorf("остаток стаканов = %v, want 400.000", got)
	}
}

// TestИмпортCSV_ОшибкиНеМеняютБазу: либо всё, либо ничего (FR-4).
func TestИмпортCSV_ОшибкиНеМеняютБазу(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.OwnerEmail)

	csv := strings.Join([]string{
		"Название,Единица,Остаток",
		"Молоко,л,24",
		"Зерно,тонна,8", // неизвестная единица
	}, "\n")

	result := decode[map[string]any](t, c.doRaw("/api/v1/imports/items", csv))

	errs, ok := result["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("ожидалась одна ошибка по строке: %v", result["errors"])
	}
	if result["dry_run"] != true {
		t.Error("при ошибках импорт должен остаться предпросмотром")
	}

	// В базе ничего не появилось, включая корректную первую строку.
	list := decode[map[string]any](t, c.get("/api/v1/items"))
	if items := list["items"].([]any); len(items) != 0 {
		t.Errorf("частично импортировано %d позиций — должно быть 0", len(items))
	}
}

func TestИмпортCSV_СотрудникуНедоступен(t *testing.T) {
	c, f := newClient(t)
	c.login(f, f.StaffEmail)

	resp := c.doRaw("/api/v1/imports/items", "Название,Единица\nМолоко,л")
	requireStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// TestДемо_ПовторныйКликВозвращаетТоЖеДемо — §7.5.
//
// Человек, уже сидящий в демо, нажимает кнопку второй раз: он хочет
// попасть обратно, а не завести ещё одно. Раньше создавалось второе, и
// на пятом нажатии посетитель упирался в лимит «5 в час с адреса» —
// в собственном браузере, с живой сессией на руках.
func TestДемо_ПовторныйКликВозвращаетТоЖеДемо(t *testing.T) {
	c, _ := newClient(t)

	first := c.post("/api/v1/sandbox", map[string]any{})
	requireStatus(t, first, http.StatusCreated)
	created := decode[map[string]any](t, first)

	second := c.post("/api/v1/sandbox", map[string]any{})
	// 200, а не 201: ничего не создано.
	requireStatus(t, second, http.StatusOK)
	again := decode[map[string]any](t, second)

	if created["tenant_id"] != again["tenant_id"] {
		t.Errorf("второй клик завёл новое демо: было %v, стало %v",
			created["tenant_id"], again["tenant_id"])
	}
	// Слеш на конце обязателен: без него nginx отдаёт лендинг вместо
	// кабинета, и переход выглядит так, будто кнопка не сработала.
	for _, r := range []map[string]any{created, again} {
		if r["redirect_to"] != "/app/" {
			t.Errorf("ведём не туда: %v, ожидалось /app/", r["redirect_to"])
		}
	}
}

// TestДемо_БезСессииСоздаётНовое — обратная сторона: посетитель без
// сессии должен получить своё демо, а не чужое.
func TestДемо_БезСессииСоздаётНовое(t *testing.T) {
	c, _ := newClient(t)

	first := decode[map[string]any](t, c.post("/api/v1/sandbox", map[string]any{}))

	// Отдельный клиент — отдельный браузер, cookie не разделяются.
	other := c.peer()
	resp := other.post("/api/v1/sandbox", map[string]any{})
	requireStatus(t, resp, http.StatusCreated)
	second := decode[map[string]any](t, resp)

	if first["tenant_id"] == second["tenant_id"] {
		t.Error("два разных посетителя попали в одно демо")
	}
}
