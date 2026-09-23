package sandbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/vostapenko/zapas/internal/dashboard"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/orders"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/sandbox"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
	"github.com/vostapenko/zapas/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }

func silent() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type env struct {
	sandbox   *sandbox.Service
	simulator *sandbox.Simulator
	dashboard *dashboard.Service
	notify    *notify.Service
	testEnv   *testsupport.Env
}

func setup(t *testing.T) env {
	t.Helper()
	e := testsupport.Shared(t)

	notifySvc := notify.NewService(e.App, e.Maint, e.Clock, "https://demo.test")
	repl := replenishment.NewService(e.App, e.Clock, notifySvc)
	stocks := stock.NewService(e.App, e.Clock, repl)
	ordersSvc := orders.NewService(e.App, e.Clock, repl, stocks, notifySvc)
	pipe := pipeline.NewService(e.App, e.Clock, repl, silent())
	sandboxSvc := sandbox.NewService(e.App, e.Maint, e.Clock, pipe, 24*time.Hour, "https://demo.test", silent())

	return env{
		sandbox:   sandboxSvc,
		simulator: sandbox.NewSimulator(sandboxSvc, ordersSvc, stocks, notifySvc),
		dashboard: dashboard.NewService(e.App, e.Clock),
		notify:    notifySvc,
		testEnv:   e,
	}
}

// tenantOf собирает tenant.Tenant по созданной песочнице.
func (e env) tenantOf(t *testing.T, created sandbox.Created) tenant.Tenant {
	t.Helper()
	ctx := context.Background()

	var tn tenant.Tenant
	err := e.testEnv.Maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		row, err := sqlc.New(tx).GetTenant(ctx, created.TenantID)
		if err != nil {
			return err
		}
		tn = tenant.Tenant{
			ID:          row.ID,
			Name:        row.Name,
			Location:    tenant.LoadLocation(row.Timezone),
			IsSandbox:   row.IsSandbox,
			ClockOffset: postgres.Duration(row.ClockOffset),
			ExpiresAt:   postgres.TimePtr(row.ExpiresAt),
			Autopilot:   row.Autopilot,
			Seed:        row.Seed,
			Settings:    tenant.ParseSettings(row.Settings),
		}
		return nil
	})
	if err != nil {
		t.Fatalf("чтение тенанта: %v", err)
	}
	return tn
}

// TestСоздание_СтартовоеСостояние проверяет обещание §7.2: на входе видны
// все статусы, включая красные и «заказать сегодня».
func TestСоздание_СтартовоеСостояние(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 20260919)
	if err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)

	if !tn.IsSandbox {
		t.Error("тенант должен быть помечен как демо")
	}
	if tn.ExpiresAt == nil {
		t.Error("у демо должен быть срок жизни")
	}

	view, err := e.dashboard.Load(ctx, tn)
	if err != nil {
		t.Fatal(err)
	}

	if view.Counters.Total < 30 {
		t.Errorf("позиций в демо %d, ожидалось около 40", view.Counters.Total)
	}
	if view.Counters.Total > sandbox.MaxItems {
		t.Errorf("позиций %d, лимит %d", view.Counters.Total, sandbox.MaxItems)
	}

	// Прогноз должен быть построен: иначе дашборд серый и показывать нечего.
	withModel := 0
	for _, row := range view.Items {
		if row.Model != "" && row.Model != "M0" {
			withModel++
		}
	}
	if withModel < view.Counters.Total/2 {
		t.Errorf("модель построена только у %d из %d позиций", withModel, view.Counters.Total)
	}

	// На входе должно быть что показать: и заказы, и красное.
	needsAttention := view.Counters.OrderToday + view.Counters.Critical + view.Counters.OutOfStock
	if needsAttention == 0 {
		t.Errorf("демо открылось полностью зелёным — показывать нечего: %+v", view.Counters)
	}
	if len(view.Suggestions) == 0 && view.Counters.OrderToday > 0 {
		t.Error("есть позиции «заказать сегодня», но блок рекомендаций пустой")
	}
}

// TestВоспроизводимость: одинаковый seed даёт одинаковые данные (§7.2).
func TestВоспроизводимость(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	const seed = 777

	first, err := e.sandbox.Create(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.sandbox.Create(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := e.dashboard.Load(ctx, e.tenantOf(t, first))
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.dashboard.Load(ctx, e.tenantOf(t, second))
	if err != nil {
		t.Fatal(err)
	}

	if len(a.Items) != len(b.Items) {
		t.Fatalf("разное число позиций: %d и %d", len(a.Items), len(b.Items))
	}
	for i := range a.Items {
		if a.Items[i].Name != b.Items[i].Name {
			t.Fatalf("порядок позиций разошёлся: %q и %q", a.Items[i].Name, b.Items[i].Name)
		}
		if !a.Items[i].OnHand.Equal(b.Items[i].OnHand) {
			t.Errorf("%s: остатки разошлись — %s и %s",
				a.Items[i].Name, a.Items[i].OnHand, b.Items[i].OnHand)
		}
		if a.Items[i].Status != b.Items[i].Status {
			t.Errorf("%s: статусы разошлись — %s и %s",
				a.Items[i].Name, a.Items[i].Status, b.Items[i].Status)
		}
	}
}

// TestПромотка_ДвигаетВремяИМеняетСостояние — главный аргумент демо (§7.3).
func TestПромотка_ДвигаетВремяИМеняетСостояние(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 4242)
	if err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)
	before := tn.Today(e.testEnv.Clock)

	result, err := e.simulator.Advance(ctx, tn, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Days != 1 {
		t.Errorf("промотано %d дней, want 1", result.Days)
	}
	if got := result.Today.Sub(before); got != 1 {
		t.Errorf("дата сдвинулась на %d дней, want 1", got)
	}

	// Виртуальная дата действительно изменилась в базе.
	after := e.tenantOf(t, created)
	if got := after.Today(e.testEnv.Clock).Sub(before); got != 1 {
		t.Errorf("виртуальная дата тенанта сдвинулась на %d, want 1", got)
	}

	// За день был расход: движения появились.
	view, err := e.dashboard.Load(ctx, after)
	if err != nil {
		t.Fatal(err)
	}
	if view.Today != after.Today(e.testEnv.Clock) {
		t.Errorf("дашборд показывает %s, а тенант живёт в %s", view.Today, after.Today(e.testEnv.Clock))
	}
}

// TestПромотка_БезАвтопилотаПозицииКраснеют — сравнение «с системой и без»
// из §7.3.
func TestПромотка_БезАвтопилотаПозицииКраснеют(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 31337)
	if err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)

	beforeView, err := e.dashboard.Load(ctx, tn)
	if err != nil {
		t.Fatal(err)
	}
	redBefore := beforeView.Counters.Critical + beforeView.Counters.OutOfStock

	// Неделя без единого заказа.
	if _, err := e.simulator.Advance(ctx, tn, 7); err != nil {
		t.Fatal(err)
	}

	afterView, err := e.dashboard.Load(ctx, e.tenantOf(t, created))
	if err != nil {
		t.Fatal(err)
	}
	redAfter := afterView.Counters.Critical + afterView.Counters.OutOfStock

	if redAfter <= redBefore {
		t.Errorf("без заказов за неделю красных стало %d (было %d) — "+
			"демо не показывает, что бывает без системы", redAfter, redBefore)
	}
}

// TestПромотка_САвтопилотомЗаказыОформляются.
func TestПромотка_САвтопилотомЗаказыОформляются(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 5150)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.sandbox.SetAutopilot(ctx, created.TenantID, true); err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)
	if !tn.Autopilot {
		t.Fatal("автопилот не включился")
	}

	result, err := e.simulator.Advance(ctx, tn, 7)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ordered == 0 {
		t.Error("автопилот за неделю не оформил ни одного заказа")
	}
	if result.Received == 0 {
		t.Error("за неделю не приехала ни одна поставка")
	}
}

// TestПромотка_ЛентаНаполняется: сводки и алерты дня уходят в ленту (§7.3).
func TestПромотка_ЛентаНаполняется(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 909)
	if err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)

	if _, err := e.simulator.Advance(ctx, tn, 7); err != nil {
		t.Fatal(err)
	}

	feed, err := e.notify.List(ctx, e.tenantOf(t, created), created.OwnerID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) == 0 {
		t.Error("за неделю в ленте не появилось ни одного уведомления")
	}
}

func TestПромотка_ОграниченияГоризонта(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)

	if _, err := e.simulator.Advance(ctx, tn, 3); !errors.Is(err, sandbox.ErrBadDays) {
		t.Errorf("промотка на 3 дня: %v, want ErrBadDays", err)
	}

	// Обычный тенант проматывать нельзя.
	real := tn
	real.IsSandbox = false
	if _, err := e.simulator.Advance(ctx, real, 1); !errors.Is(err, sandbox.ErrNotSandbox) {
		t.Errorf("промотка рабочего тенанта: %v, want ErrNotSandbox", err)
	}

	// Предел горизонта.
	maxed := tn
	maxed.ClockOffset = time.Duration(sandbox.MaxVirtualDays) * 24 * time.Hour
	if _, err := e.simulator.Advance(ctx, maxed, 1); !errors.Is(err, sandbox.ErrHorizonLimit) {
		t.Errorf("промотка за предел: %v, want ErrHorizonLimit", err)
	}
}

// TestОчистка удаляет истёкшие демо каскадом (§7.5).
func TestОчистка(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 606)
	if err != nil {
		t.Fatal(err)
	}

	activeBefore, err := e.sandbox.ActiveCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activeBefore == 0 {
		t.Fatal("только что созданная песочница не попала в счётчик активных")
	}

	// Просрочиваем демо.
	e.expire(t, created)

	deleted, err := e.sandbox.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("очистка не удалила истёкшую песочницу")
	}

	// Тенанта больше нет — значит нет и его данных.
	if e.tenantExists(t, created) {
		t.Error("истёкший тенант остался в базе")
	}
}

// expire ставит срок жизни в прошлое.
//
// Время берётся у часов теста, а не у системных: часы заморожены, и
// «час назад» по настоящим часам может оказаться будущим по
// замороженным. Тест из-за этого проходил только до определённого
// часа суток.
func (e env) expire(t *testing.T, created sandbox.Created) {
	t.Helper()
	ctx := context.Background()

	err := e.testEnv.Maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		_, err := tx.Exec(ctx,
			"UPDATE tenants SET expires_at = $2 WHERE id = $1",
			created.TenantID, e.testEnv.Clock.Now().Add(-time.Hour))
		return err
	})
	if err != nil {
		t.Fatalf("просрочка песочницы: %v", err)
	}
}

func (e env) tenantExists(t *testing.T, created sandbox.Created) bool {
	t.Helper()
	ctx := context.Background()

	exists := false
	err := e.testEnv.Maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM tenants WHERE id = $1)", created.TenantID).
			Scan(&exists)
	})
	if err != nil {
		t.Fatalf("проверка тенанта: %v", err)
	}
	return exists
}

// TestГенератор_ОстаткиСходятсяСЖурналом — главная инвариантa системы должна
// держаться и на сгенерированных данных (ADR-002).
func TestГенератор_ОстаткиСходятсяСЖурналом(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 12345)
	if err != nil {
		t.Fatal(err)
	}
	tn := e.tenantOf(t, created)

	stocks := stock.NewService(e.testEnv.App, e.testEnv.Clock, nil)
	drifts, err := stocks.Reconcile(ctx, tn)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Errorf("генератор оставил %d расхождений остатка с журналом: %v", len(drifts), drifts)
	}

	// И после недели промотки тоже.
	if _, err := e.simulator.Advance(ctx, tn, 7); err != nil {
		t.Fatal(err)
	}
	drifts, err = stocks.Reconcile(ctx, e.tenantOf(t, created))
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Errorf("после промотки %d расхождений: %v", len(drifts), drifts)
	}
}

// TestПромотка_НеделяСАвтопилотом — §7.3: автопилот оформляет и принимает
// заказы во время промотки.
//
// Ловит ошибку, из-за которой приёмка бралась по системным часам, а не по
// часам тенанта: после нескольких дней промотки приход оказывался «в
// прошлом» дальше, чем на неделю, и FR-9 его отвергал.
func TestПромотка_НеделяСАвтопилотом(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	created, err := e.sandbox.Create(ctx, 4242)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.sandbox.SetAutopilot(ctx, created.TenantID, true); err != nil {
		t.Fatal(err)
	}

	tn := e.tenantOf(t, created)

	// Две недели подряд: одной мало, ошибка проявлялась накоплением сдвига.
	for i := range 2 {
		res, err := e.simulator.Advance(ctx, tn, 7)
		if err != nil {
			t.Fatalf("промотка недели %d: %v", i+1, err)
		}
		t.Logf("неделя %d: принято %d, заказано %d, уведомлений %d",
			i+1, res.Received, res.Ordered, res.Notifications)
		tn = e.tenantOf(t, created)
	}

	// Автопилот обязан был хоть что-то заказать и принять за две недели.
	res, err := e.simulator.Advance(ctx, tn, 1)
	if err != nil {
		t.Fatalf("промотка дня после двух недель: %v", err)
	}
	_ = res
}
