package stock_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/testsupport"
)

// newService поднимает изолированную базу и сервис склада на застывших часах.
func newService(t *testing.T) (*stock.Service, *testsupport.Fixture) {
	t.Helper()
	env := testsupport.Shared(t)
	f := env.NewTenant(t, "Кофейня «Тест»")
	return stock.NewService(env.App, env.Clock, nil), f
}

// receipt — короткая запись прихода: он нужен почти в каждом тесте.
func receipt(t *testing.T, s *stock.Service, f *testsupport.Fixture, itemID uuid.UUID, amount string) stock.RecordResult {
	t.Helper()
	res, err := s.Record(context.Background(), f.Tenant, stock.RecordRequest{
		ItemID:         itemID,
		Type:           stock.TypeReceipt,
		Qty:            qty.MustParse(amount),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Owner,
	})
	if err != nil {
		t.Fatalf("приход %s: %v", amount, err)
	}
	return res
}

func TestRecord_ЗнакСтавитСервер(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Молоко 3,2%", "l")
	ctx := context.Background()

	// Количество всегда приходит положительным (§11).
	receipt(t, s, f, item, "24")

	res, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1.5"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Movement.Qty.String() != "-1.500" {
		t.Errorf("расход записан как %s, want -1.500", res.Movement.Qty)
	}
	if res.Balance.OnHand.String() != "22.500" {
		t.Errorf("остаток = %s, want 22.500", res.Balance.OnHand)
	}
}

// TestFR8_ЗапретОтрицательногоОстатка: расход сверх остатка отклоняется,
// а в ошибке есть текущий остаток — интерфейс показывает его пользователю.
func TestFR8_ЗапретОтрицательногоОстатка(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Молоко 3,2%", "l")
	ctx := context.Background()

	receipt(t, s, f, item, "0.8")

	_, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeWriteoff,
		Qty:            qty.MustParse("1.5"),
		Reason:         string(stock.ReasonSpoiled),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	})
	if !errors.Is(err, stock.ErrInsufficientStock) {
		t.Fatalf("ожидалась ErrInsufficientStock, got %v", err)
	}

	var insufficient *stock.InsufficientStockError
	if !errors.As(err, &insufficient) {
		t.Fatalf("ошибка не несёт остаток: %v", err)
	}
	if insufficient.OnHand.String() != "0.800" {
		t.Errorf("в ошибке остаток %s, want 0.800", insufficient.OnHand)
	}

	// Неудачная попытка не должна оставить следов.
	if got := f.OnHand(t, item); got.String() != "0.800" {
		t.Errorf("остаток после отказа = %s, want 0.800", got)
	}
	if got := f.SumMovements(t, item); got.String() != "0.800" {
		t.Errorf("журнал после отказа = %s, want 0.800", got)
	}
}

// TestFR10_Идемпотентность: повтор формы с плохой связи не создаёт второе
// движение.
func TestFR10_Идемпотентность(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Сливки 33%", "l")
	ctx := context.Background()

	receipt(t, s, f, item, "10")

	key := uuid.NewString()
	req := stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("2"),
		IdempotencyKey: key,
		CreatedBy:      &f.Staff,
	}

	first, err := s.Record(ctx, f.Tenant, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate {
		t.Error("первый запрос не должен считаться повтором")
	}

	second, err := s.Record(ctx, f.Tenant, req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate {
		t.Error("повтор с тем же ключом должен помечаться как дубль")
	}
	if second.Movement.ID != first.Movement.ID {
		t.Errorf("вернулось другое движение: %s вместо %s", second.Movement.ID, first.Movement.ID)
	}
	if got := f.OnHand(t, item); got.String() != "8.000" {
		t.Errorf("остаток = %s, want 8.000: расход списался дважды", got)
	}
}

// TestFR10_ИдемпотентностьПриГонке: два одинаковых запроса одновременно.
// Второй должен проиграть на уникальном индексе, а не создать движение.
func TestFR10_ИдемпотентностьПриГонке(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Зерно Бразилия", "kg")
	ctx := context.Background()

	receipt(t, s, f, item, "10")

	key := uuid.NewString()
	req := stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1"),
		IdempotencyKey: key,
		CreatedBy:      &f.Staff,
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Record(ctx, f.Tenant, req)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil && !errors.Is(err, stock.ErrDuplicateKey) {
			t.Fatalf("запрос %d: неожиданная ошибка %v", i, err)
		}
	}
	if got := f.OnHand(t, item); got.String() != "9.000" {
		t.Errorf("остаток = %s, want 9.000: движение записалось дважды", got)
	}
}

// TestКонкурентность_ОстатокНеУходитВМинус — обязательный тест из §14.1:
// 50 горутин одновременно списывают одну позицию.
func TestКонкурентность_ОстатокНеУходитВМинус(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Стаканы 350 мл", "pcs")
	ctx := context.Background()

	// Остатка хватает ровно на 30 списаний из 50.
	receipt(t, s, f, item, "30")

	const goroutines = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, rejected := 0, 0

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
				ItemID:         item,
				Type:           stock.TypeUsage,
				Qty:            qty.MustParse("1"),
				IdempotencyKey: uuid.NewString(),
				CreatedBy:      &f.Staff,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, stock.ErrInsufficientStock):
				rejected++
			default:
				t.Errorf("неожиданная ошибка: %v", err)
			}
		}()
	}
	wg.Wait()

	if succeeded != 30 {
		t.Errorf("прошло списаний: %d, want 30", succeeded)
	}
	if rejected != goroutines-30 {
		t.Errorf("отклонено: %d, want %d", rejected, goroutines-30)
	}

	onHand := f.OnHand(t, item)
	if onHand.String() != "0.000" {
		t.Errorf("остаток = %s, want 0.000", onHand)
	}
	if onHand.IsNegative() {
		t.Fatal("остаток ушёл в минус")
	}
	// Главная гарантия ADR-002: остаток равен сумме движений.
	if sum := f.SumMovements(t, item); !sum.Equal(onHand) {
		t.Errorf("остаток %s разошёлся с журналом %s", onHand, sum)
	}
}

// TestFR7_Сторно: исходное движение не меняется, сторно возможно один раз.
func TestFR7_Сторно(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Сироп карамель", "l")
	ctx := context.Background()

	receipt(t, s, f, item, "10")

	wrong, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeWriteoff,
		Qty:            qty.MustParse("3"),
		Reason:         string(stock.ReasonBroken),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.OnHand(t, item); got.String() != "7.000" {
		t.Fatalf("остаток после списания = %s, want 7.000", got)
	}

	reversal, err := s.Reverse(ctx, f.Tenant, wrong.Movement.ID, f.Owner, true)
	if err != nil {
		t.Fatal(err)
	}
	if reversal.Movement.Type != stock.TypeReversal {
		t.Errorf("тип сторно = %s, want reversal", reversal.Movement.Type)
	}
	if reversal.Movement.Qty.String() != "3.000" {
		t.Errorf("сторно списания = %s, want 3.000", reversal.Movement.Qty)
	}
	if got := f.OnHand(t, item); got.String() != "10.000" {
		t.Errorf("остаток после сторно = %s, want 10.000", got)
	}

	// Второй раз сторнировать нельзя (FR-7).
	if _, err := s.Reverse(ctx, f.Tenant, wrong.Movement.ID, f.Owner, true); !errors.Is(err, stock.ErrAlreadyReversed) {
		t.Errorf("повторное сторно: %v, want ErrAlreadyReversed", err)
	}
	// Сторно сторна тоже нельзя.
	if _, err := s.Reverse(ctx, f.Tenant, reversal.Movement.ID, f.Owner, true); !errors.Is(err, stock.ErrCannotReverse) {
		t.Errorf("сторно сторна: %v, want ErrCannotReverse", err)
	}
}

func TestFR7_СотрудникСторнируетТолькоСвоё(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Молоко овсяное", "l")
	ctx := context.Background()

	receipt(t, s, f, item, "10") // создано владельцем

	ownerMovement, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Owner,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Сотрудник не может сторнировать чужое.
	if _, err := s.Reverse(ctx, f.Tenant, ownerMovement.Movement.ID, f.Staff, false); !errors.Is(err, stock.ErrCannotReverse) {
		t.Errorf("сотрудник сторнировал чужое движение: %v", err)
	}
	// Владелец может.
	if _, err := s.Reverse(ctx, f.Tenant, ownerMovement.Movement.ID, f.Owner, true); err != nil {
		t.Errorf("владелец не смог сторнировать: %v", err)
	}
}

// TestFR9_ВремяСобытия: назад не дальше семи дней, вперёд нельзя.
func TestFR9_ВремяСобытия(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Сахар", "kg")
	ctx := context.Background()
	receipt(t, s, f, item, "100")

	now := f.Tenant.Now(f.Env.Clock)

	tests := []struct {
		name       string
		occurredAt time.Time
		wantErr    error
	}{
		{"вчера — можно", now.AddDate(0, 0, -1), nil},
		{"шесть дней назад — можно", now.AddDate(0, 0, -6), nil},
		{"восемь дней назад — нельзя", now.AddDate(0, 0, -8), stock.ErrOccurredTooOld},
		{"завтра — нельзя", now.AddDate(0, 0, 1), stock.ErrOccurredInFuture},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
				ItemID:         item,
				Type:           stock.TypeUsage,
				Qty:            qty.MustParse("1"),
				OccurredAt:     tc.occurredAt,
				IdempotencyKey: uuid.NewString(),
				CreatedBy:      &f.Staff,
			})
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("неожиданная ошибка: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestСписаниеБезПричиныОтклоняется(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Круассан", "pcs")
	receipt(t, s, f, item, "10")

	_, err := s.Record(context.Background(), f.Tenant, stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeWriteoff,
		Qty:            qty.MustParse("1"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	})
	if err == nil {
		t.Fatal("списание без причины должно отклоняться")
	}
}

// TestFR11_Журнал: фильтры и курсорная пагинация.
func TestFR11_Журнал(t *testing.T) {
	s, f := newService(t)
	milk := f.NewItem(t, "Молоко 3,2%", "l")
	beans := f.NewItem(t, "Зерно", "kg")
	ctx := context.Background()

	receipt(t, s, f, milk, "100")
	receipt(t, s, f, beans, "50")
	for i := 0; i < 5; i++ {
		if _, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
			ItemID:         milk,
			Type:           stock.TypeUsage,
			Qty:            qty.MustParse("1"),
			IdempotencyKey: uuid.NewString(),
			CreatedBy:      &f.Staff,
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListMovements(ctx, f.Tenant, stock.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 7 {
		t.Fatalf("всего движений %d, want 7", len(all.Items))
	}

	byItem, err := s.ListMovements(ctx, f.Tenant, stock.ListFilter{ItemID: &beans})
	if err != nil {
		t.Fatal(err)
	}
	if len(byItem.Items) != 1 {
		t.Errorf("по зерну %d движений, want 1", len(byItem.Items))
	}

	usage := stock.TypeUsage
	byType, err := s.ListMovements(ctx, f.Tenant, stock.ListFilter{Type: &usage})
	if err != nil {
		t.Fatal(err)
	}
	if len(byType.Items) != 5 {
		t.Errorf("расходов %d, want 5", len(byType.Items))
	}

	byAuthor, err := s.ListMovements(ctx, f.Tenant, stock.ListFilter{AuthorID: &f.Owner})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAuthor.Items) != 2 {
		t.Errorf("движений владельца %d, want 2 (два прихода)", len(byAuthor.Items))
	}

	// Пагинация: три страницы по три, без потерь и дублей.
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		got, err := s.ListMovements(ctx, f.Tenant, stock.ListFilter{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range got.Items {
			if seen[m.ID] {
				t.Errorf("движение %s пришло дважды", m.ID)
			}
			seen[m.ID] = true
		}
		if got.NextCursor == "" {
			break
		}
		cursor = got.NextCursor
	}
	if len(seen) != 7 {
		t.Errorf("страницами собрано %d движений, want 7", len(seen))
	}

	if _, err := s.ListMovements(ctx, f.Tenant, stock.ListFilter{Cursor: "не курсор"}); !errors.Is(err, stock.ErrBadCursor) {
		t.Errorf("испорченный курсор: %v, want ErrBadCursor", err)
	}
}

// TestFR12_FR13_Инвентаризация: проведение создаёт корректировку
// «факт − учёт», и остаток становится равен факту.
func TestFR12_FR13_Инвентаризация(t *testing.T) {
	s, f := newService(t)
	milk := f.NewItem(t, "Молоко 3,2%", "l")
	ctx := context.Background()

	receipt(t, s, f, milk, "24")

	count, err := s.CreateCount(ctx, f.Tenant, stock.ScopeAll, "Конец смены", nil, f.Staff)
	if err != nil {
		t.Fatal(err)
	}
	if count.Status != stock.CountDraft {
		t.Errorf("статус нового пересчёта = %s, want draft", count.Status)
	}
	if len(count.Lines) != 1 {
		t.Fatalf("строк в пересчёте %d, want 1", len(count.Lines))
	}
	if count.Lines[0].ExpectedQty.String() != "24.000" {
		t.Errorf("учёт в строке = %s, want 24.000", count.Lines[0].ExpectedQty)
	}

	// Фактически намерили 23,2 л: 0,8 л где-то потерялись.
	if err := s.SetCountLines(ctx, f.Tenant, count.ID, map[uuid.UUID]qty.Qty{
		milk: qty.MustParse("23.2"),
	}); err != nil {
		t.Fatal(err)
	}

	// До проведения расхождение видно, но остаток не тронут.
	draft, err := s.GetCount(ctx, f.Tenant, count.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Lines[0].Diff == nil || draft.Lines[0].Diff.String() != "-0.800" {
		t.Errorf("расхождение = %v, want -0.800", draft.Lines[0].Diff)
	}
	if got := f.OnHand(t, milk); got.String() != "24.000" {
		t.Errorf("до проведения остаток = %s, want 24.000", got)
	}

	posted, err := s.PostCount(ctx, f.Tenant, count.ID, f.Owner, false)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Status != stock.CountPosted {
		t.Errorf("статус после проведения = %s, want posted", posted.Status)
	}
	if got := f.OnHand(t, milk); got.String() != "23.200" {
		t.Errorf("после проведения остаток = %s, want 23.200", got)
	}
	if sum := f.SumMovements(t, milk); sum.String() != "23.200" {
		t.Errorf("журнал = %s, want 23.200", sum)
	}

	// Повторное проведение не должно создать вторую пачку корректировок.
	if _, err := s.PostCount(ctx, f.Tenant, count.ID, f.Owner, false); !errors.Is(err, stock.ErrCountPosted) {
		t.Errorf("повторное проведение: %v, want ErrCountPosted", err)
	}
	if got := f.OnHand(t, milk); got.String() != "23.200" {
		t.Errorf("остаток после повтора = %s, want 23.200", got)
	}
}

// TestFR13_УчётИзменилсяМеждуВводомИПроведением — предупреждение из ТЗ.
func TestFR13_УчётИзменилсяМеждуВводомИПроведением(t *testing.T) {
	s, f := newService(t)
	milk := f.NewItem(t, "Молоко 3,2%", "l")
	ctx := context.Background()

	receipt(t, s, f, milk, "24")

	count, err := s.CreateCount(ctx, f.Tenant, stock.ScopeAll, "", nil, f.Staff)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCountLines(ctx, f.Tenant, count.ID, map[uuid.UUID]qty.Qty{
		milk: qty.MustParse("23"),
	}); err != nil {
		t.Fatal(err)
	}

	// Пока пересчитывали, кто-то списал ещё литр.
	if _, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         milk,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = s.PostCount(ctx, f.Tenant, count.ID, f.Owner, false)
	if !errors.Is(err, stock.ErrCountChanged) {
		t.Fatalf("проведение: %v, want ErrCountChanged", err)
	}

	var changed *stock.CountChangedError
	if !errors.As(err, &changed) || len(changed.Items) != 1 {
		t.Fatalf("ошибка не перечислила изменившиеся позиции: %v", err)
	}
	if changed.Items[0].CurrentQty.String() != "23.000" {
		t.Errorf("текущий учёт = %s, want 23.000", changed.Items[0].CurrentQty)
	}

	// Отклонённое проведение ничего не поменяло.
	if got := f.OnHand(t, milk); got.String() != "23.000" {
		t.Errorf("остаток = %s, want 23.000", got)
	}

	// С явным подтверждением проводится.
	if _, err := s.PostCount(ctx, f.Tenant, count.ID, f.Owner, true); err != nil {
		t.Fatal(err)
	}
	if got := f.OnHand(t, milk); got.String() != "23.000" {
		t.Errorf("после подтверждения остаток = %s, want 23.000", got)
	}
}

// TestFR17_СверкаОстаткаСЖурналом.
func TestFR17_СверкаОстаткаСЖурналом(t *testing.T) {
	s, f := newService(t)
	milk := f.NewItem(t, "Молоко 3,2%", "l")
	beans := f.NewItem(t, "Зерно", "kg")
	ctx := context.Background()

	receipt(t, s, f, milk, "24")
	receipt(t, s, f, beans, "5")
	if _, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         milk,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1.5"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	}); err != nil {
		t.Fatal(err)
	}

	drifts, err := s.Reconcile(ctx, f.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Errorf("сверка нашла расхождения на чистых данных: %v", drifts)
	}
}

// TestИзоляция_ЧужойТенантНеВиденЧерезСервис — уровень приложения поверх RLS.
func TestИзоляция_ЧужойТенантНеВиденЧерезСервис(t *testing.T) {
	env := testsupport.Shared(t)
	a := env.NewTenant(t, "Тенант A")
	b := env.NewTenant(t, "Тенант B")
	s := stock.NewService(env.App, env.Clock, nil)
	ctx := context.Background()

	itemA := a.NewItem(t, "Молоко A", "l")
	itemB := b.NewItem(t, "Зерно B", "kg")

	receipt(t, s, a, itemA, "10")
	receipt(t, s, b, itemB, "20")

	// Позиция чужого тенанта не находится.
	_, err := s.Record(ctx, b.Tenant, stock.RecordRequest{
		ItemID:         itemA,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &b.Owner,
	})
	if !errors.Is(err, stock.ErrItemNotFound) {
		t.Errorf("движение по чужой позиции: %v, want ErrItemNotFound", err)
	}

	// В журнале тенанта B нет движений тенанта A.
	page, err := s.ListMovements(ctx, b.Tenant, stock.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("в журнале B %d движений, want 1", len(page.Items))
	}
	if page.Items[0].ItemID != itemB {
		t.Errorf("в журнал B попало чужое движение: %s", page.Items[0].ItemID)
	}

	// Остатки тоже не пересекаются.
	balances, err := s.ListBalances(ctx, b.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := balances[itemA]; leaked {
		t.Error("остаток чужой позиции виден тенанту B")
	}
}

func TestАрхивнаяПозицияНеПринимаетДвижений(t *testing.T) {
	s, f := newService(t)
	item := f.NewItem(t, "Старый сироп", "l")
	ctx := context.Background()

	receipt(t, s, f, item, "5")
	f.ArchiveItem(t, item)

	_, err := s.Record(ctx, f.Tenant, stock.RecordRequest{
		ItemID:         item,
		Type:           stock.TypeUsage,
		Qty:            qty.MustParse("1"),
		IdempotencyKey: uuid.NewString(),
		CreatedBy:      &f.Staff,
	})
	if !errors.Is(err, stock.ErrItemArchived) {
		t.Errorf("движение по архивной позиции: %v, want ErrItemArchived", err)
	}
}
