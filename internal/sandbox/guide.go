package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/orders"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/tenant"
)

// StepID — шаг пошагового демо. Порядок — как у настоящего внедрения:
// сначала справочники, потом история, учёт, прогноз и работа с заказами.
type StepID string

const (
	StepSuppliers     StepID = "suppliers"
	StepItems         StepID = "items"
	StepHistory       StepID = "history"
	StepCount         StepID = "count"
	StepForecast      StepID = "forecast"
	StepOrder         StepID = "order"
	StepReceive       StepID = "receive"
	StepTime          StepID = "time"
	StepNotifications StepID = "notifications"
)

// stepTitles — названия для сообщений об ошибках; интерфейс держит свои.
var stepTitles = map[StepID]string{
	StepSuppliers:     "Поставщики",
	StepItems:         "Позиции",
	StepHistory:       "История расхода",
	StepCount:         "Пересчёт",
	StepForecast:      "Прогноз",
	StepOrder:         "Заказ",
	StepReceive:       "Приёмка",
	StepTime:          "Время",
	StepNotifications: "Уведомления",
}

// MinHistoryDays — сколько разных дней с расходом нужно, чтобы шаг истории
// считался пройденным. Руками больше недели не внести (FR-9), поэтому
// порог выше: история в жизни приходит импортом из прежнего учёта.
const MinHistoryDays = 14

// Ошибки пошагового демо.
var (
	ErrUnknownStep      = errors.New("sandbox: неизвестный шаг")
	ErrNotFillable      = errors.New("sandbox: этот шаг делается только руками")
	ErrFillBusy         = errors.New("sandbox: шаг уже заполняется")
	ErrNoTemplateItems  = errors.New("sandbox: нет позиций из шаблона")
	ErrNothingToOrder   = errors.New("sandbox: заказывать нечего")
	ErrNothingToReceive = errors.New("sandbox: нет отправленных заказов")
)

// StepOrderError — шаг заполняют раньше предыдущего.
type StepOrderError struct {
	Need StepID
}

func (e *StepOrderError) Error() string {
	return fmt.Sprintf("sandbox: сначала шаг «%s»", stepTitles[e.Need])
}

// Step — состояние одного шага.
type Step struct {
	ID StepID `json:"id"`
	// Done — в данных есть след шага; руками он сделан или шаблоном — всё равно.
	Done bool `json:"done"`
	// Count — этот след: поставщиков, позиций, дней истории и т. п.
	Count int `json:"count"`
	// Fillable — у шага есть наполнение тестовыми данными.
	Fillable bool `json:"fillable"`
	// Ready — предыдущие шаги пройдены, наполнять можно.
	Ready bool `json:"ready"`
}

// GuideState — все шаги по порядку.
type GuideState struct {
	Steps []Step `json:"steps"`
	// Current — первый непройденный шаг; пусто, когда пройдено всё.
	Current StepID `json:"current,omitempty"`
}

func (g GuideState) step(id StepID) (Step, bool) {
	for _, s := range g.Steps {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}

// FillResult — состояние после наполнения.
type FillResult struct {
	GuideState
	// Filled — сколько записей добавил шаблон; при повторе ноль.
	Filled int `json:"filled"`
}

// Guide ведёт посетителя по шагам пошагового демо (§7).
type Guide struct {
	svc *Service
	sim *Simulator
}

func NewGuide(svc *Service, sim *Simulator) *Guide {
	return &Guide{svc: svc, sim: sim}
}

// State считает, какие шаги уже пройдены.
func (g *Guide) State(ctx context.Context, tn tenant.Tenant) (GuideState, error) {
	if !tn.IsSandbox {
		return GuideState{}, ErrNotSandbox
	}

	var row sqlc.GuideStateRow
	err := g.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		var err error
		row, err = sqlc.New(tx).GuideState(ctx, tn.ID)
		return err
	})
	if err != nil {
		return GuideState{}, fmt.Errorf("sandbox: состояние шагов: %w", err)
	}
	return buildState(row), nil
}

func buildState(r sqlc.GuideStateRow) GuideState {
	steps := []Step{
		{ID: StepSuppliers, Count: int(r.Suppliers), Done: r.Suppliers > 0, Fillable: true},
		{ID: StepItems, Count: int(r.Items), Done: r.Items > 0, Fillable: true},
		{ID: StepHistory, Count: int(r.UsageDays), Done: r.UsageDays >= MinHistoryDays, Fillable: true},
		{ID: StepCount, Count: int(r.PostedCounts), Done: r.PostedCounts > 0, Fillable: true},
		{ID: StepForecast, Count: int(r.Forecasts), Done: r.Forecasts > 0, Fillable: true},
		{ID: StepOrder, Count: int(r.SentOrders), Done: r.SentOrders > 0, Fillable: true},
		{ID: StepReceive, Count: int(r.ReceivedOrders), Done: r.ReceivedOrders > 0, Fillable: true},
		{ID: StepTime, Count: int(r.VirtualDays), Done: r.VirtualDays > 0},
		{ID: StepNotifications, Count: int(r.ReadNotifications), Done: r.ReadNotifications > 0},
	}

	out := GuideState{Steps: steps}
	ready := true
	for i := range out.Steps {
		out.Steps[i].Ready = ready
		if !out.Steps[i].Done {
			if out.Current == "" {
				out.Current = out.Steps[i].ID
			}
			ready = false
		}
	}
	return out
}

// Fill заполняет шаг данными шаблона.
//
// Повтор ничего не дублирует: справочники ищутся по имени, история
// дописывается только позициям без неё, заказы и приёмка — по статусу.
// Одновременные наполнения одного демо отсекает блокировка.
func (g *Guide) Fill(ctx context.Context, tn tenant.Tenant, id StepID) (FillResult, error) {
	if !tn.IsSandbox {
		return FillResult{}, ErrNotSandbox
	}
	if _, ok := stepTitles[id]; !ok {
		return FillResult{}, ErrUnknownStep
	}

	unlock, err := g.lock(ctx, tn.ID)
	if err != nil {
		return FillResult{}, err
	}
	defer unlock()

	state, err := g.State(ctx, tn)
	if err != nil {
		return FillResult{}, err
	}
	step, _ := state.step(id)
	if !step.Fillable {
		return FillResult{}, ErrNotFillable
	}
	if !step.Ready {
		return FillResult{}, &StepOrderError{Need: state.Current}
	}

	owner, err := g.sim.owner(ctx, tn)
	if err != nil {
		return FillResult{}, err
	}

	var filled int
	switch id {
	case StepSuppliers:
		filled, err = g.fillSuppliers(ctx, tn)
	case StepItems:
		filled, err = g.fillItems(ctx, tn)
	case StepHistory:
		filled, err = g.fillHistory(ctx, tn, owner)
	case StepCount:
		filled, err = g.fillCount(ctx, tn, step, owner)
	case StepForecast:
		filled, err = g.fillForecast(ctx, tn)
	case StepOrder:
		filled, err = g.fillOrder(ctx, tn, step, owner)
	case StepReceive:
		filled, err = g.fillReceive(ctx, tn, step, owner)
	}
	if err != nil {
		return FillResult{}, err
	}

	after, err := g.State(ctx, tn)
	if err != nil {
		return FillResult{}, err
	}
	return FillResult{GuideState: after, Filled: filled}, nil
}

// lock берёт сессионную блокировку на тенанта на отдельном соединении.
// Наполнение ходит в базу несколькими транзакциями через сервисы, и
// блокировка транзакции его бы не накрыла.
func (g *Guide) lock(ctx context.Context, tenantID uuid.UUID) (func(), error) {
	conn, err := g.svc.maint.Pool().Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("sandbox: соединение для блокировки: %w", err)
	}
	q := sqlc.New(conn)

	ok, err := q.TryGuideLock(ctx, tenantID.String())
	if err != nil {
		conn.Release()
		return nil, fmt.Errorf("sandbox: блокировка: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, ErrFillBusy
	}

	return func() {
		// Отменённый запрос не должен оставить блокировку висеть на
		// соединении, которое вернётся в пул.
		_ = q.GuideUnlock(context.WithoutCancel(ctx), tenantID.String())
		conn.Release()
	}, nil
}

func (g *Guide) fillSuppliers(ctx context.Context, tn tenant.Tenant) (int, error) {
	var created int
	err := g.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		var err error
		_, created, err = g.svc.ensureSuppliers(ctx, sqlc.New(tx), tn)
		return err
	})
	return created, err
}

// fillItems заводит позиции шаблона. Поставщиков шаблона, которых нет,
// тоже заводит: посетитель мог создать своих, а позициям нужны эти.
func (g *Guide) fillItems(ctx context.Context, tn tenant.Tenant) (int, error) {
	var created int
	err := g.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		warehouse, err := q.GetDefaultWarehouse(ctx, tn.ID)
		if err != nil {
			return fmt.Errorf("sandbox: склад: %w", err)
		}
		supplierIDs, _, err := g.svc.ensureSuppliers(ctx, q, tn)
		if err != nil {
			return err
		}
		items, err := g.svc.ensureItems(ctx, q, tn, warehouse.ID, supplierIDs)
		if err != nil {
			return err
		}
		created = len(items)
		return nil
	})
	return created, err
}

// fillHistory дописывает 90 дней истории позициям шаблона, у которых её
// ещё нет. Позициям посетителя историю не выдумываем: у них честно «нет
// прогноза», как и при промотке времени.
func (g *Guide) fillHistory(ctx context.Context, tn tenant.Tenant, owner uuid.UUID) (int, error) {
	var written int
	err := g.svc.db.InTenantTx(ctx, tn.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		warehouse, err := q.GetDefaultWarehouse(ctx, tn.ID)
		if err != nil {
			return fmt.Errorf("sandbox: склад: %w", err)
		}
		rows, err := q.ListItems(ctx, sqlc.ListItemsParams{TenantID: tn.ID})
		if err != nil {
			return fmt.Errorf("sandbox: позиции: %w", err)
		}

		// Движение старше недели руками не внести (FR-9): если оно есть,
		// историю позиции уже писал генератор.
		before := tn.Now(g.svc.clock).Add(-8 * 24 * time.Hour)
		withHistory, err := q.ItemsWithMovementsBefore(ctx, sqlc.ItemsWithMovementsBeforeParams{
			TenantID: tn.ID, Before: postgres.Time(before),
		})
		if err != nil {
			return fmt.Errorf("sandbox: позиции с историей: %w", err)
		}
		skip := make(map[uuid.UUID]bool, len(withHistory))
		for _, id := range withHistory {
			skip[id] = true
		}

		// Порядок каталога, а не алфавитный: поток генератора у позиции
		// привязан к её месту, и история совпадает с готовым демо.
		byName := make(map[string]uuid.UUID, len(rows))
		for _, row := range rows {
			byName[row.Name] = row.ID
		}
		templated := 0
		var items []createdItem
		for _, spec := range catalog {
			id, ok := byName[spec.Name]
			if !ok {
				continue
			}
			templated++
			if !skip[id] {
				items = append(items, createdItem{ID: id, Spec: spec})
			}
		}
		if templated == 0 {
			return ErrNoTemplateItems
		}

		written = len(items)
		return g.svc.generateHistory(ctx, q, tn, warehouse.ID, owner, items, tn.Seed)
	})
	return written, err
}

// fillCount проводит пересчёт с расхождениями, как у живого склада: у
// трети позиций факт меньше учёта — пролили, недовесили, не записали.
func (g *Guide) fillCount(ctx context.Context, tn tenant.Tenant, step Step, owner uuid.UUID) (int, error) {
	if step.Done {
		return 0, nil
	}

	count, err := g.sim.stock.CreateCount(ctx, tn, stock.ScopeAll, "Пересчёт из шаблона демо", nil, owner)
	if err != nil {
		return 0, err
	}

	rng := newRNG(tn.Seed, 7_000_000)
	lines := make(map[uuid.UUID]qty.Qty, len(count.Lines))
	differs := 0
	for _, l := range count.Lines {
		counted := l.ExpectedQty
		if rng.Float64() < 0.33 && l.ExpectedQty.IsPositive() {
			counted = shortfall(l, rng.Float64())
			differs++
		}
		lines[l.ItemID] = counted
	}

	if err := g.sim.stock.SetCountLines(ctx, tn, count.ID, lines); err != nil {
		return 0, err
	}
	// force: подтверждать проведение здесь некому, а расход между открытием
	// документа и проведением шаблону не мешает.
	if _, err := g.sim.stock.PostCount(ctx, tn, count.ID, owner, true); err != nil {
		return 0, err
	}
	return differs, nil
}

// shortfall — факт меньше учёта. Штуки — на целое число, от 1 до 3: у
// стаканов не бывает дробной недостачи. Вес и объём — на 1–5%.
func shortfall(l stock.CountLine, r float64) qty.Qty {
	if l.BaseUnit == "pcs" {
		return l.ExpectedQty.Sub(qty.FromInt(1 + int64(r*3))).ClampZero()
	}
	return l.ExpectedQty.MulFloat(0.99 - r*0.04)
}

func (g *Guide) fillForecast(ctx context.Context, tn tenant.Tenant) (int, error) {
	result, err := g.svc.pipeline.Run(ctx, tn)
	if err != nil {
		return 0, fmt.Errorf("sandbox: прогноз: %w", err)
	}
	return result.WithModel, nil
}

// fillOrder оформляет и отправляет заказы по рекомендациям — то же, что
// автопилот за один день.
func (g *Guide) fillOrder(ctx context.Context, tn tenant.Tenant, step Step, owner uuid.UUID) (int, error) {
	placed, err := g.sim.autoOrder(ctx, tn, owner)
	if err != nil {
		return 0, err
	}
	if placed == 0 && !step.Done {
		return 0, ErrNothingToOrder
	}
	return placed, nil
}

// fillReceive принимает все отправленные заказы целиком: в демо поставщик
// привозит, не дожидаясь даты поставки.
func (g *Guide) fillReceive(ctx context.Context, tn tenant.Tenant, step Step, owner uuid.UUID) (int, error) {
	status := orders.StatusSent
	sent, err := g.sim.orders.List(ctx, tn, &status, 200)
	if err != nil {
		return 0, err
	}
	if len(sent) == 0 {
		if step.Done {
			return 0, nil
		}
		return 0, ErrNothingToReceive
	}

	for _, o := range sent {
		if _, err := g.sim.orders.Receive(ctx, tn, o.ID, nil, owner, "guide:"+o.ID.String()); err != nil {
			return 0, fmt.Errorf("sandbox: приёмка: %w", err)
		}
	}
	return len(sent), nil
}
