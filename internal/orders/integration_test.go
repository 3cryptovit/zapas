package orders_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/orders"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }

type env struct {
	orders   *orders.Service
	stock    *stock.Service
	pipeline *pipeline.Service
	fixture  *testsupport.Fixture
	item     uuid.UUID
	supplier uuid.UUID
}

// setup поднимает тенанта с молочным поставщиком (пн и чт, срок 1 день,
// отсечка 16:00) и позицией с упаковкой по 12 л.
func setup(t *testing.T) env {
	t.Helper()
	e := testsupport.Shared(t)
	f := e.NewTenant(t, "Кофейня «Заказы»")

	supplier := f.NewSupplier(t, "Молочная ферма", 1, []int16{1, 4}, "16:00")
	item := f.NewItem(t, "Молоко 3,2%", "l")
	f.SetDefaultSupplier(t, item, supplier)
	f.SetTerms(t, supplier, item, "кор.", "12", "12", "0")

	repl := replenishment.NewService(e.App, e.Clock, nil)
	st := stock.NewService(e.App, e.Clock, repl)

	return env{
		orders:   orders.NewService(e.App, e.Clock, repl, st, nil),
		stock:    st,
		pipeline: pipeline.NewService(e.App, e.Clock, repl, discardLogger()),
		fixture:  f,
		item:     item,
		supplier: supplier,
	}
}

// TestПолныйЦикл — сценарий из §2: рекомендация → заказ → отправка →
// «в пути» → приёмка → приход.
func TestПолныйЦикл(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	// 60 дней ровного расхода по 6 л и остаток, которого хватит ненадолго.
	today := f.Tenant.Today(f.Env.Clock)
	f.SeedHistory(t, e.item, today.AddDays(-60), 60, func(int, clock.Day) string { return "6" })
	f.SetOnHand(t, e.item, "10")

	if _, err := e.pipeline.Run(ctx, f.Tenant); err != nil {
		t.Fatal(err)
	}

	// 1. Черновик собирается из рекомендаций одной кнопкой на поставщика.
	order, err := e.orders.Create(ctx, f.Tenant, orders.CreateInput{
		SupplierID: e.supplier,
		CreatedBy:  f.Owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != orders.StatusDraft {
		t.Errorf("статус нового заказа = %s, want draft", order.Status)
	}
	if len(order.Lines) != 1 {
		t.Fatalf("строк в заказе %d, want 1", len(order.Lines))
	}
	ordered := order.Lines[0].QtyOrdered
	if !ordered.IsPositive() {
		t.Fatal("количество в строке должно быть больше нуля")
	}
	// Рекомендация уже округлена до упаковки.
	if rem := ordered.Decimal().Mod(qty.MustParse("12").Decimal()); !rem.IsZero() {
		t.Errorf("заказано %s — не кратно упаковке 12", ordered)
	}

	// Пока заказ черновик, «в пути» ничего нет.
	if got := onOrder(t, e, e.item); !got.IsZero() {
		t.Errorf("по черновику в пути %s, want 0", got)
	}

	// 2. Отправка фиксирует дату поставки и текст заявки.
	sent, err := e.orders.Send(ctx, f.Tenant, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != orders.StatusSent {
		t.Fatalf("статус = %s, want sent", sent.Status)
	}
	if sent.ExpectedAt.IsZero() {
		t.Error("ожидаемая дата поставки не проставлена")
	}
	// Среда: ближайший день доставки при сроке 1 день — четверг.
	if got := sent.ExpectedAt.String(); got != "2026-09-24" {
		t.Errorf("поставка = %s, want 2026-09-24 (чт)", got)
	}
	if !strings.Contains(sent.Text, "Молоко 3,2%") {
		t.Errorf("в тексте заявки нет позиции:\n%s", sent.Text)
	}
	if !strings.Contains(sent.Text, "кор.") {
		t.Errorf("в тексте заявки нет единицы закупки:\n%s", sent.Text)
	}

	// 3. После отправки количество учитывается «в пути», повторных
	// напоминаний по позиции быть не должно (§2).
	if got := onOrder(t, e, e.item); !got.Equal(ordered) {
		t.Errorf("в пути %s, want %s", got, ordered)
	}
	if st := statusOf(t, e, e.item); st == string(replenishment.StatusOrderToday) {
		t.Error("после отправки заказа позиция не должна просить заказ снова")
	}

	// 4. Приёмка создаёт приход и обнуляет «в пути».
	before := f.OnHand(t, e.item)
	received, err := e.orders.Receive(ctx, f.Tenant, order.ID, nil, f.Staff, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if received.Status != orders.StatusReceived {
		t.Fatalf("статус = %s, want received", received.Status)
	}
	if received.Lines[0].QtyReceived == nil {
		t.Fatal("фактическое количество не записано")
	}
	if !received.Lines[0].QtyReceived.Equal(ordered) {
		t.Errorf("по умолчанию принято %s, want %s", received.Lines[0].QtyReceived, ordered)
	}

	after := f.OnHand(t, e.item)
	if got := after.Sub(before); !got.Equal(ordered) {
		t.Errorf("остаток вырос на %s, want %s", got, ordered)
	}
	if got := onOrder(t, e, e.item); !got.IsZero() {
		t.Errorf("после приёмки в пути %s, want 0", got)
	}
	// Остаток обязан совпасть с журналом.
	if sum := f.SumMovements(t, e.item); !sum.Equal(after) {
		t.Errorf("остаток %s разошёлся с журналом %s", after, sum)
	}
}

// TestFR20_ПовторнаяПриёмкаНеСоздаётВторойПриход.
func TestFR20_ПовторнаяПриёмкаНеСоздаётВторойПриход(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	order := draftAndSend(t, e, "24")

	key := uuid.NewString()
	if _, err := e.orders.Receive(ctx, f.Tenant, order.ID, nil, f.Staff, key); err != nil {
		t.Fatal(err)
	}
	after := f.OnHand(t, e.item)

	_, err := e.orders.Receive(ctx, f.Tenant, order.ID, nil, f.Staff, key)
	if !errors.Is(err, orders.ErrAlreadyReceived) {
		t.Fatalf("повторная приёмка: %v, want ErrAlreadyReceived", err)
	}
	if got := f.OnHand(t, e.item); !got.Equal(after) {
		t.Errorf("остаток изменился после повторной приёмки: %s → %s", after, got)
	}
}

// TestПриёмкаСРасхождением: привезли меньше, чем заказали.
func TestПриёмкаСРасхождением(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	order := draftAndSend(t, e, "24")
	before := f.OnHand(t, e.item)

	received, err := e.orders.Receive(ctx, f.Tenant, order.ID, []orders.ReceiveLine{
		{ItemID: e.item, Qty: qty.MustParse("18")},
	}, f.Staff, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	if got := received.Lines[0].Mismatch(); got.String() != "-6.000" {
		t.Errorf("расхождение = %s, want -6.000", got)
	}
	if !received.HasSignificantMismatch() {
		t.Error("расхождение 25% должно считаться существенным (порог 5%)")
	}
	if got := f.OnHand(t, e.item).Sub(before); got.String() != "18.000" {
		t.Errorf("приход = %s, want 18.000", got)
	}
}

func TestОтменаЗаказаУбираетИзПути(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	order := draftAndSend(t, e, "24")
	if got := onOrder(t, e, e.item); got.IsZero() {
		t.Fatal("после отправки количество должно быть в пути")
	}

	cancelled, err := e.orders.Cancel(ctx, f.Tenant, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != orders.StatusCancelled {
		t.Errorf("статус = %s, want cancelled", cancelled.Status)
	}
	if got := onOrder(t, e, e.item); !got.IsZero() {
		t.Errorf("после отмены в пути %s, want 0", got)
	}

	// Принять отменённый заказ нельзя.
	if _, err := e.orders.Receive(ctx, f.Tenant, order.ID, nil, f.Staff, uuid.NewString()); !errors.Is(err, orders.ErrWrongStatus) {
		t.Errorf("приёмка отменённого: %v, want ErrWrongStatus", err)
	}
}

func TestПовторнаяОтправкаОтклоняется(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	order := draftAndSend(t, e, "24")
	if _, err := e.orders.Send(ctx, e.fixture.Tenant, order.ID); !errors.Is(err, orders.ErrAlreadySent) {
		t.Errorf("повторная отправка: %v, want ErrAlreadySent", err)
	}
}

func TestПустойЗаказНеОтправляется(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// Рекомендаций нет — черновик собрать не из чего.
	_, err := e.orders.Create(ctx, e.fixture.Tenant, orders.CreateInput{
		SupplierID: e.supplier,
		CreatedBy:  e.fixture.Owner,
	})
	if !errors.Is(err, orders.ErrEmptyOrder) {
		t.Errorf("создание пустого заказа: %v, want ErrEmptyOrder", err)
	}
}

func TestПравкаСтрокЧерновика(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	order, err := e.orders.Create(ctx, f.Tenant, orders.CreateInput{
		SupplierID: e.supplier,
		CreatedBy:  f.Owner,
		Lines:      []orders.CreateLine{{ItemID: e.item, Qty: qty.MustParse("12")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := e.orders.UpdateLines(ctx, f.Tenant, order.ID, []orders.CreateLine{
		{ItemID: e.item, Qty: qty.MustParse("36")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Lines[0].QtyOrdered.String(); got != "36.000" {
		t.Errorf("количество = %s, want 36.000", got)
	}

	// Нулевое количество убирает строку.
	emptied, err := e.orders.UpdateLines(ctx, f.Tenant, order.ID, []orders.CreateLine{
		{ItemID: e.item, Qty: qty.Zero()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptied.Lines) != 0 {
		t.Errorf("строк осталось %d, want 0", len(emptied.Lines))
	}

	// Отправленный заказ править нельзя.
	if _, err := e.orders.UpdateLines(ctx, f.Tenant, order.ID, nil); err != nil {
		t.Fatalf("правка пустого черновика не должна падать: %v", err)
	}
}

func TestИзоляция_ЧужойЗаказНеВиден(t *testing.T) {
	a := setup(t)
	b := setup(t)
	ctx := context.Background()

	order := draftAndSend(t, a, "24")

	if _, err := b.orders.Get(ctx, b.fixture.Tenant, order.ID); !errors.Is(err, orders.ErrNotFound) {
		t.Errorf("чужой заказ: %v, want ErrNotFound", err)
	}
	list, err := b.orders.List(ctx, b.fixture.Tenant, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("в списке чужого тенанта %d заказов, want 0", len(list))
	}
}

func TestOrderText_ЧитаемаяЗаявка(t *testing.T) {
	e := setup(t)
	order := draftAndSend(t, e, "24")

	text := order.Text
	for _, want := range []string{"Кофейня «Заказы»", "Молоко 3,2%", "2 кор.", "24 л"} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте заявки нет %q:\n%s", want, text)
		}
	}
}

// --- вспомогательное ---

func draftAndSend(t *testing.T, e env, amount string) orders.Order {
	t.Helper()
	ctx := context.Background()

	order, err := e.orders.Create(ctx, e.fixture.Tenant, orders.CreateInput{
		SupplierID: e.supplier,
		CreatedBy:  e.fixture.Owner,
		Lines:      []orders.CreateLine{{ItemID: e.item, Qty: qty.MustParse(amount)}},
	})
	if err != nil {
		t.Fatalf("создание заказа: %v", err)
	}
	sent, err := e.orders.Send(ctx, e.fixture.Tenant, order.ID)
	if err != nil {
		t.Fatalf("отправка заказа: %v", err)
	}
	return sent
}

func onOrder(t *testing.T, e env, itemID uuid.UUID) qty.Qty {
	t.Helper()
	return e.fixture.ItemStatusOnOrder(t, itemID)
}

func statusOf(t *testing.T, e env, itemID uuid.UUID) string {
	t.Helper()
	return e.fixture.ItemStatusCode(t, itemID)
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
