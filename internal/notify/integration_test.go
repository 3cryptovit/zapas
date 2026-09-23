package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/tenant"
	"github.com/vostapenko/zapas/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.Run(m)) }

func silent() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// nextChatID выдаёт уникальный идентификатор чата на каждый вызов.
var chatIDCounter atomic.Int64

func nextChatID() int64 { return 100_000 + chatIDCounter.Add(1) }

// fakeSender запоминает отправленное и умеет падать по требованию.
type fakeSender struct {
	mu   sync.Mutex
	sent []string
	// fail возвращается вместо успеха, пока не обнулён.
	fail error
	// calls считает попытки, включая неудачные.
	calls int
}

func (f *fakeSender) Send(_ context.Context, recipient string, p notify.Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, recipient+": "+p.Title)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeSender) attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type env struct {
	notify     *notify.Service
	dispatcher *notify.Dispatcher
	sender     *fakeSender
	fixture    *testsupport.Fixture
}

func setup(t *testing.T) env {
	t.Helper()
	e := testsupport.Shared(t)
	f := e.NewTenant(t, "Кофейня «Уведомления»")

	// Привязываем Telegram владельцу: иначе слать некуда.
	// chat_id глобально уникален (один чат — один пользователь), поэтому
	// у каждого теста он свой.
	f.LinkTelegram(t, f.Owner, nextChatID())

	sender := &fakeSender{}
	return env{
		notify: notify.NewService(e.App, e.Maint, e.Clock, "https://example.test"),
		dispatcher: notify.NewDispatcher(e.Maint, map[notify.Channel]notify.Sender{
			notify.ChannelTelegram: sender,
		}, silent()),
		sender:  sender,
		fixture: f,
	}
}

func redAlert(f *testsupport.Fixture, itemID uuid.UUID) replenishment.RedAlert {
	return replenishment.RedAlert{
		ItemID:         itemID,
		ItemName:       "Молоко 3,2%",
		BaseUnit:       "l",
		Status:         replenishment.StatusOutOfStock,
		PreviousStatus: replenishment.StatusOK,
		OnHand:         qty.Zero(),
		SupplierName:   "Молочная ферма",
	}
}

// emit — короткая запись уведомления в собственной транзакции.
func emit(t *testing.T, e env, fn func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error) {
	t.Helper()
	err := e.fixture.Env.App.InTenantTx(context.Background(), e.fixture.Tenant.ID.String(),
		func(ctx context.Context, tx postgres.Tx) error {
			return fn(ctx, tx, e.fixture.Tenant)
		})
	if err != nil {
		t.Fatalf("запись уведомления: %v", err)
	}
}

func TestАлертПопадаетВЛентуИОчередь(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	item := f.NewItem(t, "Молоко 3,2%", "l")
	emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
		return e.notify.ItemWentRed(ctx, tx, tn, redAlert(f, item))
	})

	feed, err := e.notify.List(ctx, f.Tenant, f.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 {
		t.Fatalf("в ленте %d уведомлений, want 1", len(feed.Items))
	}
	if feed.Items[0].Type != notify.TypeCritical {
		t.Errorf("тип = %s, want critical", feed.Items[0].Type)
	}
	if feed.Unread != 1 {
		t.Errorf("непрочитанных = %d, want 1", feed.Unread)
	}
	if feed.Items[0].Payload.Body == "" {
		t.Error("текст уведомления пустой")
	}

	// Воркер забирает и отправляет.
	result, err := e.dispatcher.Dispatch(ctx, f.Env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != 1 {
		t.Errorf("отправлено %d, want 1 (claimed %d, retried %d, failed %d)",
			result.Sent, result.Claimed, result.Retried, result.Failed)
	}
	if e.sender.count() != 1 {
		t.Errorf("канал получил %d сообщений, want 1", e.sender.count())
	}

	// Повторный проход не должен отправить то же самое ещё раз.
	again, err := e.dispatcher.Dispatch(ctx, f.Env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if again.Claimed != 0 {
		t.Errorf("второй проход забрал %d сообщений, want 0", again.Claimed)
	}
}

// TestДедупликация: один алерт на позицию в день (§5.5).
func TestДедупликация(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	item := f.NewItem(t, "Молоко 3,2%", "l")
	for i := 0; i < 3; i++ {
		emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
			return e.notify.ItemWentRed(ctx, tx, tn, redAlert(f, item))
		})
	}

	feed, err := e.notify.List(ctx, f.Tenant, f.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 {
		t.Errorf("в ленте %d уведомлений, want 1: дедупликация не сработала", len(feed.Items))
	}

	result, err := e.dispatcher.Dispatch(ctx, f.Env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != 1 {
		t.Errorf("отправлено %d, want 1", result.Sent)
	}
}

// TestРетрай: временная ошибка откладывает отправку, а не теряет её.
func TestРетрай(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture
	now := f.Env.Clock.Now()

	item := f.NewItem(t, "Сливки 33%", "l")
	emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
		return e.notify.ItemWentRed(ctx, tx, tn, redAlert(f, item))
	})

	// Канал недоступен.
	e.sender.fail = errors.New("сеть недоступна")

	first, err := e.dispatcher.Dispatch(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Retried != 1 {
		t.Fatalf("отложено %d, want 1", first.Retried)
	}

	// Сразу повторять нельзя: сообщение отложено на минуту.
	immediate, err := e.dispatcher.Dispatch(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if immediate.Claimed != 0 {
		t.Errorf("отложенное сообщение забрали сразу: claimed %d", immediate.Claimed)
	}

	// Через две минуты канал починился.
	e.sender.fail = nil
	later, err := e.dispatcher.Dispatch(ctx, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if later.Sent != 1 {
		t.Errorf("после восстановления отправлено %d, want 1", later.Sent)
	}
	if e.sender.count() != 1 {
		t.Errorf("доставлено %d сообщений, want 1", e.sender.count())
	}
}

// TestПостояннаяОшибкаНеРетраится: заблокированный бот — не повод повторять.
func TestПостояннаяОшибкаНеРетраится(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	item := f.NewItem(t, "Сахар", "kg")
	emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
		return e.notify.ItemWentRed(ctx, tx, tn, redAlert(f, item))
	})

	e.sender.fail = notify.ErrPermanent

	result, err := e.dispatcher.Dispatch(ctx, f.Env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("провалено %d, want 1", result.Failed)
	}

	// Больше не пытаемся.
	attemptsBefore := e.sender.attempts()
	if _, err := e.dispatcher.Dispatch(ctx, f.Env.Clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if e.sender.attempts() != attemptsBefore {
		t.Error("проваленное сообщение попробовали отправить снова")
	}
}

// TestПесочницаНеШлётНаружу: внешние каналы демо отключены (§7.5).
func TestПесочницаНеШлётНаружу(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	sandbox := f.Tenant
	sandbox.IsSandbox = true

	item := f.NewItem(t, "Стаканы", "pcs")
	err := f.Env.App.InTenantTx(ctx, sandbox.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return e.notify.ItemWentRed(ctx, tx, sandbox, redAlert(f, item))
	})
	if err != nil {
		t.Fatal(err)
	}

	// В ленте уведомление есть.
	feed, err := e.notify.List(ctx, sandbox, f.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 1 {
		t.Fatalf("в ленте демо %d уведомлений, want 1", len(feed.Items))
	}

	// А в очередь на отправку — нет.
	result, err := e.dispatcher.Dispatch(ctx, f.Env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 0 {
		t.Errorf("песочница поставила %d сообщений в очередь, want 0", result.Claimed)
	}
}

// TestТихиеЧасы: срочный алерт ночью откладывается до утра (§5.5).
func TestТихиеЧасы(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	// Три часа ночи по времени тенанта.
	night := time.Date(2026, 9, 23, 3, 0, 0, 0, f.Tenant.Loc())
	f.Env.Clock.Set(night)
	defer f.Env.Clock.Set(time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC))

	item := f.NewItem(t, "Молоко ночное", "l")
	emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
		return e.notify.ItemWentRed(ctx, tx, tn, redAlert(f, item))
	})

	// Ночью не забираем.
	result, err := e.dispatcher.Dispatch(ctx, night)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 0 {
		t.Errorf("ночью забрали %d сообщений, want 0", result.Claimed)
	}

	// Утром — забираем.
	morning := time.Date(2026, 9, 23, 8, 1, 0, 0, f.Tenant.Loc())
	result, err = e.dispatcher.Dispatch(ctx, morning)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sent != 1 {
		t.Errorf("утром отправлено %d, want 1", result.Sent)
	}
}

func TestОтметкаПрочитанным(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	for i, name := range []string{"Молоко", "Сливки", "Зерно"} {
		item := f.NewItem(t, name, "l")
		alert := redAlert(f, item)
		alert.ItemName = name
		_ = i
		emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
			return e.notify.ItemWentRed(ctx, tx, tn, alert)
		})
	}

	feed, err := e.notify.List(ctx, f.Tenant, f.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Unread != 3 {
		t.Fatalf("непрочитанных = %d, want 3", feed.Unread)
	}

	// Отмечаем одно.
	if _, err := e.notify.MarkRead(ctx, f.Tenant, f.Owner, []uuid.UUID{feed.Items[0].ID}); err != nil {
		t.Fatal(err)
	}
	feed, err = e.notify.List(ctx, f.Tenant, f.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Unread != 2 {
		t.Errorf("после отметки одного непрочитанных = %d, want 2", feed.Unread)
	}

	// Пустой список — «прочитать всё».
	if _, err := e.notify.MarkRead(ctx, f.Tenant, f.Owner, nil); err != nil {
		t.Fatal(err)
	}
	feed, err = e.notify.List(ctx, f.Tenant, f.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Unread != 0 {
		t.Errorf("после «прочитать всё» непрочитанных = %d, want 0", feed.Unread)
	}
}

func TestЛента_Пагинация(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := e.fixture

	for i := 0; i < 5; i++ {
		item := f.NewItem(t, "Позиция "+string(rune('А'+i)), "l")
		alert := redAlert(f, item)
		emit(t, e, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
			return e.notify.ItemWentRed(ctx, tx, tn, alert)
		})
	}

	seen := map[uuid.UUID]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		feed, err := e.notify.List(ctx, f.Tenant, f.Owner, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range feed.Items {
			if seen[n.ID] {
				t.Errorf("уведомление %s пришло дважды", n.ID)
			}
			seen[n.ID] = true
		}
		if feed.NextCursor == "" {
			break
		}
		cursor = feed.NextCursor
	}
	if len(seen) != 5 {
		t.Errorf("страницами собрано %d уведомлений, want 5", len(seen))
	}

	if _, err := e.notify.List(ctx, f.Tenant, f.Owner, "мусор", 10); !errors.Is(err, notify.ErrBadCursor) {
		t.Errorf("испорченный курсор: %v, want ErrBadCursor", err)
	}
}

func TestИзоляция_ЧужаяЛентаНеВидна(t *testing.T) {
	a := setup(t)
	b := setup(t)
	ctx := context.Background()

	item := a.fixture.NewItem(t, "Молоко A", "l")
	emit(t, a, func(ctx context.Context, tx postgres.Tx, tn tenant.Tenant) error {
		return a.notify.ItemWentRed(ctx, tx, tn, redAlert(a.fixture, item))
	})

	feed, err := b.notify.List(ctx, b.fixture.Tenant, b.fixture.Owner, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != 0 {
		t.Errorf("в чужой ленте видно %d уведомлений, want 0", len(feed.Items))
	}
}
