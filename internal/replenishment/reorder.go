package replenishment

import (
	"math"
	"time"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Status — статус позиции на дашборде (§5.3).
type Status string

const (
	StatusOutOfStock Status = "out_of_stock" // красный: на складе ноль
	StatusCritical   Status = "critical"     // красный: закончится раньше d1
	StatusOrderToday Status = "order_today"  // жёлтый: IP < S
	StatusOK         Status = "ok"           // зелёный
	StatusNoForecast Status = "no_forecast"  // серый: модель M0
)

// IsRed сообщает, что позиция требует немедленной реакции.
func (s Status) IsRed() bool { return s == StatusOutOfStock || s == StatusCritical }

// severity задаёт порядок «ухудшения»: срочный алерт повторяется только при
// переходе на более тяжёлый статус (§5.5).
func (s Status) severity() int {
	switch s {
	case StatusOutOfStock:
		return 4
	case StatusCritical:
		return 3
	case StatusOrderToday:
		return 2
	case StatusNoForecast:
		return 1
	default:
		return 0
	}
}

// WorseThan сообщает, что s тяжелее other.
func (s Status) WorseThan(other Status) bool { return s.severity() > other.severity() }

// ZFactor — множитель страхового запаса по уровню сервиса (§5.2).
func ZFactor(serviceLevel int) float64 {
	switch serviceLevel {
	case 90:
		return 1.28
	case 99:
		return 2.33
	default: // 95% по умолчанию
		return 1.65
	}
}

// Delivery — ожидаемая поставка по отправленному заказу.
type Delivery struct {
	Day clock.Day
	Qty qty.Qty
}

// Input — всё, что нужно, чтобы посчитать статус одной позиции.
type Input struct {
	Today  clock.Day
	Window Window

	OnHand qty.Qty
	// Incoming — отправленные заказы с ожидаемой датой. В IP попадают только те,
	// что приедут раньше d2 (§5.2).
	Incoming []Delivery

	// Forecast начинается с сегодняшнего дня.
	Forecast forecast.Result
	// ConsumedToday — уже израсходованное сегодня. За сегодня в целевой уровень
	// берётся только неизрасходованная часть прогноза, иначе расход
	// учитывается дважды.
	ConsumedToday qty.Qty

	ServiceLevel int
	// ManualMin — ручной минимальный остаток: запасной вариант для модели M0.
	ManualMin qty.Qty

	MinOrder qty.Qty
	Pack     qty.Qty
}

// Decision — результат расчёта: то, что ложится в item_status.
type Decision struct {
	Status Status `json:"status"`
	// OnOrder — в пути с ожидаемой датой раньше d2.
	OnOrder qty.Qty `json:"on_order"`
	// TargetLevel — целевой уровень запаса S.
	TargetLevel qty.Qty `json:"target_level"`
	SafetyStock qty.Qty `json:"safety_stock"`
	// RecommendedQty — сколько заказать, с учётом упаковки и минимальной партии.
	RecommendedQty qty.Qty `json:"recommended_qty"`
	// StockoutDate — «хватит до»: первый день, когда остаток уходит в минус.
	// Нулевой день означает «хватит на весь горизонт прогноза».
	StockoutDate clock.Day   `json:"stockout_date"`
	Explanation  Explanation `json:"explanation"`
}

// Explanation — блок «почему такой статус»: те же числа, что в формуле,
// но пригодные для показа владельцу (§6.2).
type Explanation struct {
	Model          forecast.Model `json:"model"`
	OnHand         qty.Qty        `json:"on_hand"`
	OnOrder        qty.Qty        `json:"on_order"`
	InventoryPos   qty.Qty        `json:"inventory_position"`
	NeedUntilD2    qty.Qty        `json:"need_until_d2"`
	SafetyStock    qty.Qty        `json:"safety_stock"`
	TargetLevel    qty.Qty        `json:"target_level"`
	Shortfall      qty.Qty        `json:"shortfall"`
	RecommendedQty qty.Qty        `json:"recommended_qty"`
	D1             clock.Day      `json:"d1"`
	D2             clock.Day      `json:"d2"`
	// OrderBy — дедлайн отсечки у поставщика: «закажите до 16:00».
	OrderBy  time.Time `json:"order_by"`
	DaysToD2 int       `json:"days_to_d2"`
	Sigma    float64   `json:"sigma"`
	Z        float64   `json:"z"`
	CanWait  bool      `json:"can_wait"`
}

// Decide считает целевой уровень, рекомендуемое количество и статус (§5.2, §5.3).
func Decide(in Input) Decision {
	onOrder := incomingBefore(in.Incoming, in.Window.D2)
	ip := in.OnHand.Add(onOrder)

	stockoutDate := projectStockout(in)

	// Модель M0: прогноза нет, страхового запаса нет, сравниваем с ручным минимумом.
	if in.Forecast.Model == forecast.ModelM0 {
		return decideManual(in, onOrder, ip, stockoutDate)
	}

	z := ZFactor(in.ServiceLevel)
	daysToD2 := max(in.Window.D2.Sub(in.Today), 0)
	// SS = z · σ · sqrt(n)
	safety := fromFloat(z * in.Forecast.Metrics.Sigma * math.Sqrt(float64(daysToD2)))

	// S = max(0, F_today − c_today) + сумма F_d от завтра до d2−1 + SS
	needToday := in.Forecast.On(in.Today).Sub(in.ConsumedToday).ClampZero()
	needRest := qty.Zero()
	for d := in.Today.AddDays(1); d.Before(in.Window.D2); d = d.AddDays(1) {
		needRest = needRest.Add(in.Forecast.On(d))
	}
	need := needToday.Add(needRest)
	target := need.Add(safety)

	shortfall := target.Sub(ip)
	recommended := qty.Zero()
	if shortfall.IsPositive() {
		// Q = ceil(max(S − IP, min_order) / pack) * pack
		recommended = qty.Max(shortfall, in.MinOrder).CeilTo(in.Pack)
	}

	status := classify(in, ip, target, stockoutDate)

	return Decision{
		Status:         status,
		OnOrder:        onOrder,
		TargetLevel:    target,
		SafetyStock:    safety,
		RecommendedQty: recommended,
		StockoutDate:   stockoutDate,
		Explanation: Explanation{
			Model:          in.Forecast.Model,
			OnHand:         in.OnHand,
			OnOrder:        onOrder,
			InventoryPos:   ip,
			NeedUntilD2:    need,
			SafetyStock:    safety,
			TargetLevel:    target,
			Shortfall:      shortfall.ClampZero(),
			RecommendedQty: recommended,
			D1:             in.Window.D1,
			D2:             in.Window.D2,
			OrderBy:        in.Window.OrderBy,
			DaysToD2:       daysToD2,
			Sigma:          in.Forecast.Metrics.Sigma,
			Z:              z,
			CanWait:        in.Window.CanWait(),
		},
	}
}

// classify раскладывает позицию по статусам. Порядок важен: сначала то, что
// требует немедленной реакции.
func classify(in Input, ip, target qty.Qty, stockoutDate clock.Day) Status {
	if !in.OnHand.IsPositive() {
		return StatusOutOfStock
	}
	// Закончится раньше ближайшей поставки — нужна срочная закупка.
	if !stockoutDate.IsZero() && stockoutDate.Before(in.Window.D1) {
		return StatusCritical
	}
	// Если и сегодня, и завтра поставка придёт в один день, ждать ничего не стоит.
	if ip.LessThan(target) && !in.Window.CanWait() {
		return StatusOrderToday
	}
	return StatusOK
}

// decideManual — ветка модели M0: страхового запаса нет, вместо целевого уровня
// используется ручной минимум (§5.2).
func decideManual(in Input, onOrder, ip qty.Qty, stockoutDate clock.Day) Decision {
	target := in.ManualMin
	shortfall := target.Sub(ip)

	recommended := qty.Zero()
	if shortfall.IsPositive() {
		recommended = qty.Max(shortfall, in.MinOrder).CeilTo(in.Pack)
	}

	// Пока данных мало, статус говорит честно: «сравниваем с минимумом».
	status := StatusNoForecast
	switch {
	case !in.OnHand.IsPositive():
		status = StatusOutOfStock
	case target.IsPositive() && shortfall.IsPositive():
		status = StatusOrderToday
	}

	return Decision{
		Status:         status,
		OnOrder:        onOrder,
		TargetLevel:    target,
		SafetyStock:    qty.Zero(),
		RecommendedQty: recommended,
		StockoutDate:   stockoutDate,
		Explanation: Explanation{
			Model:          forecast.ModelM0,
			OnHand:         in.OnHand,
			OnOrder:        onOrder,
			InventoryPos:   ip,
			NeedUntilD2:    qty.Zero(),
			SafetyStock:    qty.Zero(),
			TargetLevel:    target,
			Shortfall:      shortfall.ClampZero(),
			RecommendedQty: recommended,
			D1:             in.Window.D1,
			D2:             in.Window.D2,
			OrderBy:        in.Window.OrderBy,
			DaysToD2:       max(in.Window.D2.Sub(in.Today), 0),
			CanWait:        in.Window.CanWait(),
		},
	}
}

// incomingBefore суммирует поставки, которые придут строго раньше дня d.
func incomingBefore(deliveries []Delivery, d clock.Day) qty.Qty {
	total := qty.Zero()
	for _, dv := range deliveries {
		if dv.Day.Before(d) {
			total = total.Add(dv.Qty)
		}
	}
	return total
}

// projectStockout считает дату «хватит до»: от остатка по дням вычитается
// прогноз и прибавляются ожидаемые поставки, до первого дня с отрицательным
// остатком (§5.2). Нулевой день означает, что запаса хватает на весь горизонт.
func projectStockout(in Input) clock.Day {
	balance := in.OnHand
	horizon := len(in.Forecast.Points)
	if horizon == 0 {
		return clock.Day{}
	}

	for i := 0; i < horizon; i++ {
		day := in.Today.AddDays(i)

		// Поставка приходит в начале дня и сразу доступна.
		for _, dv := range in.Incoming {
			if dv.Day.Equal(day) {
				balance = balance.Add(dv.Qty)
			}
		}

		need := in.Forecast.On(day)
		if i == 0 {
			// Сегодняшний расход частично уже случился.
			need = need.Sub(in.ConsumedToday).ClampZero()
		}
		balance = balance.Sub(need)

		if balance.IsNegative() {
			return day
		}
	}
	return clock.Day{}
}

func fromFloat(v float64) qty.Qty {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return qty.Zero()
	}
	return qty.FromInt(1).MulFloat(v)
}
