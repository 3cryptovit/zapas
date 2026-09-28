package sandbox_test

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/sandbox"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// guided заводит пустое пошаговое демо.
func (e env) guided(t *testing.T) (sandbox.CreatedGuided, tenant.Tenant, *sandbox.Guide) {
	t.Helper()
	g, err := e.sandbox.CreateGuided(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return g, e.tenantOf(t, g.Created), sandbox.NewGuide(e.sandbox, e.simulator)
}

func stepOf(t *testing.T, s sandbox.GuideState, id sandbox.StepID) sandbox.Step {
	t.Helper()
	for _, step := range s.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("шага %s нет в состоянии", id)
	return sandbox.Step{}
}

func (e env) fill(t *testing.T, g *sandbox.Guide, tn tenant.Tenant, id sandbox.StepID) sandbox.FillResult {
	t.Helper()
	res, err := g.Fill(context.Background(), tn, id)
	if err != nil {
		t.Fatalf("заполнить %s: %v", id, err)
	}
	if !stepOf(t, res.GuideState, id).Done {
		t.Fatalf("после наполнения шаг %s не пройден: %+v", id, res.GuideState)
	}
	return res
}

func (e env) reconcile(t *testing.T, tn tenant.Tenant, when string) {
	t.Helper()
	drifts, err := stock.NewService(e.testEnv.App, e.testEnv.Clock, nil).Reconcile(context.Background(), tn)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Errorf("%s: %d расхождений остатка с журналом: %v", when, len(drifts), drifts)
	}
}

var loginPattern = regexp.MustCompile(`^demo-[a-z2-9]{8}@sandbox\.local$`)

// TestПошаговое_ПустойСтарт — крайний случай «пусто»: демо без данных,
// текущий шаг — первый, остальные заблокированы.
func TestПошаговое_ПустойСтарт(t *testing.T) {
	e := setup(t)
	g, tn, guide := e.guided(t)

	if !loginPattern.MatchString(g.Email) {
		t.Errorf("логин %q не по шаблону", g.Email)
	}
	if len(g.Password) != 12 {
		t.Errorf("пароль длиной %d, want 12", len(g.Password))
	}

	state, err := guide.State(context.Background(), tn)
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != sandbox.StepSuppliers {
		t.Errorf("текущий шаг %q, want suppliers", state.Current)
	}
	for _, s := range state.Steps {
		if s.Done {
			t.Errorf("в пустом демо шаг %s уже пройден", s.ID)
		}
		if want := s.ID == sandbox.StepSuppliers; s.Ready != want {
			t.Errorf("шаг %s: ready=%v, want %v", s.ID, s.Ready, want)
		}
	}
}

// TestПошаговое_ВесьПуть — все шаги шаблоном по порядку. Главный инвариант
// (ADR-002) держится на каждом этапе.
func TestПошаговое_ВесьПуть(t *testing.T) {
	e := setup(t)
	_, tn, guide := e.guided(t)

	if got := e.fill(t, guide, tn, sandbox.StepSuppliers).Filled; got != 4 {
		t.Errorf("поставщиков заведено %d, want 4", got)
	}
	if got := e.fill(t, guide, tn, sandbox.StepItems).Filled; got < 30 {
		t.Errorf("позиций заведено %d, ждали весь каталог", got)
	}
	e.fill(t, guide, tn, sandbox.StepHistory)
	e.reconcile(t, tn, "после истории")

	if got := e.fill(t, guide, tn, sandbox.StepCount).Filled; got == 0 {
		t.Error("пересчёт без единого расхождения: шаблон должен показать недостачу")
	}
	e.reconcile(t, tn, "после пересчёта")

	e.fill(t, guide, tn, sandbox.StepForecast)
	if got := e.fill(t, guide, tn, sandbox.StepOrder).Filled; got == 0 {
		t.Error("ни одного заказа: после истории без заказов в последние дни рекомендации обязаны быть")
	}
	res := e.fill(t, guide, tn, sandbox.StepReceive)
	e.reconcile(t, tn, "после приёмки")

	if res.Current != sandbox.StepTime {
		t.Errorf("после шаблонных шагов текущий %q, want time", res.Current)
	}

	// Шаг «Время» делается кнопкой промотки — и засчитывается по данным.
	if _, err := e.simulator.Advance(context.Background(), tn, 1); err != nil {
		t.Fatal(err)
	}
	state, err := guide.State(context.Background(), e.tenantOf(t, sandbox.Created{TenantID: tn.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if !stepOf(t, state, sandbox.StepTime).Done {
		t.Error("после промотки шаг «Время» не засчитан")
	}
}

// TestПошаговое_ПорядокШагов — шаг не по порядку и шаг без шаблона.
func TestПошаговое_ПорядокШагов(t *testing.T) {
	e := setup(t)
	_, tn, guide := e.guided(t)
	ctx := context.Background()

	_, err := guide.Fill(ctx, tn, sandbox.StepItems)
	var order *sandbox.StepOrderError
	if !errors.As(err, &order) || order.Need != sandbox.StepSuppliers {
		t.Errorf("позиции без поставщиков: %v, want StepOrderError{suppliers}", err)
	}
	if _, err := guide.Fill(ctx, tn, sandbox.StepTime); !errors.Is(err, sandbox.ErrNotFillable) {
		t.Errorf("шаг «Время»: %v, want ErrNotFillable", err)
	}
	if _, err := guide.Fill(ctx, tn, "nope"); !errors.Is(err, sandbox.ErrUnknownStep) {
		t.Errorf("неизвестный шаг: %v, want ErrUnknownStep", err)
	}
}

// TestПошаговое_ПовторИРучнойПоставщик — повтор ничего не дублирует, а
// поставщик, заведённый руками, шаблон узнаёт без учёта регистра.
func TestПошаговое_ПовторИРучнойПоставщик(t *testing.T) {
	e := setup(t)
	_, tn, guide := e.guided(t)

	e.createSupplier(t, tn, "молочная ферма")

	if got := e.fill(t, guide, tn, sandbox.StepSuppliers).Filled; got != 3 {
		t.Errorf("заведено %d поставщиков, want 3: «Молочная ферма» уже есть", got)
	}
	res := e.fill(t, guide, tn, sandbox.StepSuppliers)
	if res.Filled != 0 {
		t.Errorf("повтор завёл ещё %d", res.Filled)
	}
	if got := stepOf(t, res.GuideState, sandbox.StepSuppliers).Count; got != 4 {
		t.Errorf("поставщиков %d, want 4", got)
	}

	e.fill(t, guide, tn, sandbox.StepItems)
	if again := e.fill(t, guide, tn, sandbox.StepItems); again.Filled != 0 {
		t.Errorf("повтор позиций завёл ещё %d", again.Filled)
	}
}

// TestПошаговое_РучноеДвижениеДоИстории — посетитель записал приход руками,
// потом заполнил историю. История дописывает остаток, а не выставляет его.
func TestПошаговое_РучноеДвижениеДоИстории(t *testing.T) {
	e := setup(t)
	_, tn, guide := e.guided(t)
	ctx := context.Background()

	e.fill(t, guide, tn, sandbox.StepSuppliers)
	e.fill(t, guide, tn, sandbox.StepItems)

	itemID := e.itemID(t, tn, "Молоко 3,2%")
	owner := e.ownerOf(t, tn)
	stocks := stock.NewService(e.testEnv.App, e.testEnv.Clock, nil)
	if _, err := stocks.Record(ctx, tn, stock.RecordRequest{
		ItemID:         itemID,
		Type:           stock.TypeReceipt,
		Qty:            qty.FromInt(10),
		OccurredAt:     tn.Now(e.testEnv.Clock),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &owner,
	}); err != nil {
		t.Fatal(err)
	}

	e.fill(t, guide, tn, sandbox.StepHistory)
	e.reconcile(t, tn, "ручной приход, затем история")

	if again := e.fill(t, guide, tn, sandbox.StepHistory); again.Filled != 0 {
		t.Errorf("повтор истории переписал %d позиций", again.Filled)
	}
	e.reconcile(t, tn, "после повтора истории")
}

// TestПошаговое_ИсторияБезШаблонныхПозиций — у позиций посетителя истории
// шаблон не выдумывает.
func TestПошаговое_ИсторияБезШаблонныхПозиций(t *testing.T) {
	e := setup(t)
	_, tn, guide := e.guided(t)

	supplier := e.createSupplier(t, tn, "Свой поставщик")
	e.createItem(t, tn, "Своя позиция", supplier)

	if _, err := guide.Fill(context.Background(), tn, sandbox.StepHistory); !errors.Is(err, sandbox.ErrNoTemplateItems) {
		t.Errorf("история без шаблонных позиций: %v, want ErrNoTemplateItems", err)
	}
}

// TestПошаговое_Одновременно — два «заполнить» из двух вкладок. Дублей нет,
// второй получает «уже заполняется» или видит готовое.
func TestПошаговое_Одновременно(t *testing.T) {
	e := setup(t)
	_, tn, guide := e.guided(t)

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = guide.Fill(context.Background(), tn, sandbox.StepSuppliers)
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, sandbox.ErrFillBusy):
		default:
			t.Errorf("неожиданная ошибка: %v", err)
		}
	}
	if ok == 0 {
		t.Error("ни одно наполнение не прошло")
	}

	state, err := guide.State(context.Background(), tn)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepOf(t, state, sandbox.StepSuppliers).Count; got != 4 {
		t.Errorf("поставщиков %d после одновременных наполнений, want 4", got)
	}
}

// TestПошаговое_ГотовоеДемо — у готового демо шаги справочников, истории и
// прогноза пройдены по данным; пересчёта генератор не делает.
func TestПошаговое_ГотовоеДемо(t *testing.T) {
	e := setup(t)
	created, err := e.sandbox.Create(context.Background(), 777)
	if err != nil {
		t.Fatal(err)
	}
	state, err := sandbox.NewGuide(e.sandbox, e.simulator).State(context.Background(), e.tenantOf(t, created))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []sandbox.StepID{sandbox.StepSuppliers, sandbox.StepItems, sandbox.StepHistory, sandbox.StepForecast} {
		if !stepOf(t, state, id).Done {
			t.Errorf("в готовом демо шаг %s не пройден", id)
		}
	}
	if state.Current != sandbox.StepCount {
		t.Errorf("текущий шаг готового демо %q, want count", state.Current)
	}
}

// --- помощники ---

func (e env) createSupplier(t *testing.T, tn tenant.Tenant, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	cutoff, _ := clock.ParseTimeOfDay("16:00")
	err := e.testEnv.App.InTenantTx(context.Background(), tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		_, err := sqlc.New(tx).CreateSupplier(ctx, sqlc.CreateSupplierParams{
			ID: id, TenantID: tn.ID, Name: name, LeadTimeDays: 1,
			DeliveryWeekdays: []int16{1, 4}, OrderCutoff: postgres.TimeOfDay(cutoff),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e env) createItem(t *testing.T, tn tenant.Tenant, name string, supplier uuid.UUID) {
	t.Helper()
	err := e.testEnv.App.InTenantTx(context.Background(), tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		_, err := sqlc.New(tx).CreateItem(ctx, sqlc.CreateItemParams{
			ID: uuid.Must(uuid.NewV7()), TenantID: tn.ID, Name: name, BaseUnit: "pcs",
			DefaultSupplierID: uuid.NullUUID{UUID: supplier, Valid: true},
			ServiceLevel:      95, ManualMinQty: qty.Zero().Decimal(),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (e env) itemID(t *testing.T, tn tenant.Tenant, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := e.testEnv.App.InTenantTx(context.Background(), tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetItemByName(ctx, sqlc.GetItemByNameParams{TenantID: tn.ID, Lower: name})
		id = row.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e env) ownerOf(t *testing.T, tn tenant.Tenant) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := e.testEnv.App.InTenantTx(context.Background(), tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		users, err := sqlc.New(tx).ListRecipients(ctx, tn.ID)
		if err == nil && len(users) > 0 {
			id = users[0].ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
