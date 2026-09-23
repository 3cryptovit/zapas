package notify

import (
	"fmt"
	"strings"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// DigestData — данные ежедневной сводки (§5.5).
type DigestData struct {
	TenantName string
	Day        clock.Day
	// Suppliers — блок «Заказать сегодня», сгруппированный по поставщикам.
	Suppliers []DigestSupplier
	// Critical — позиции, которые закончатся раньше ближайшей поставки.
	Critical []DigestCritical
	// DashboardURL — ссылка «Открыть дашборд →».
	DashboardURL string
}

// DigestSupplier — что заказать у одного поставщика.
type DigestSupplier struct {
	Name string
	// Cutoff — время отсечки: «до 16:00».
	Cutoff clock.TimeOfDay
	// DeliveryAt — когда приедет, если заказать сегодня.
	DeliveryAt clock.Day
	Lines      []DigestLine
}

// DigestLine — строка рекомендации.
type DigestLine struct {
	Name string
	// Qty в базовой единице, PurchaseQty — в единице закупки.
	Qty          qty.Qty
	Unit         string
	PurchaseQty  *qty.Qty
	PurchaseUnit string
	// StockoutDate — «сейчас хватит до пт».
	StockoutDate clock.Day
}

// DigestCritical — позиция в красной зоне.
type DigestCritical struct {
	Name string
	// StockoutDate — когда закончится.
	StockoutDate clock.Day
	// NextDelivery — ближайшая возможная поставка.
	NextDelivery clock.Day
}

// OrderCount — сколько всего позиций в блоке «Заказать сегодня».
func (d DigestData) OrderCount() int {
	n := 0
	for _, s := range d.Suppliers {
		n += len(s.Lines)
	}
	return n
}

// Empty сообщает, что слать нечего: ни заказов, ни красных позиций.
func (d DigestData) Empty() bool { return d.OrderCount() == 0 && len(d.Critical) == 0 }

// RenderDigest собирает текст сводки ровно в том виде, что в §5.5.
//
//	Кофейня «Демо» · среда, 23 сентября
//
//	Заказать сегодня (3)
//	Молочная ферма — до 16:00, поставка в чт
//	  • Молоко 3,2% — 2 кор. (24 л), сейчас хватит до пт
//	...
func RenderDigest(d DigestData) Payload {
	var b strings.Builder

	fmt.Fprintf(&b, "%s · %s\n", d.TenantName, longDate(d.Day))

	if n := d.OrderCount(); n > 0 {
		fmt.Fprintf(&b, "\nЗаказать сегодня (%d)\n", n)
		for _, s := range d.Suppliers {
			b.WriteString(s.Name)
			b.WriteString(" — до ")
			b.WriteString(s.Cutoff.String())
			if !s.DeliveryAt.IsZero() {
				b.WriteString(", поставка в ")
				b.WriteString(weekdayShort(s.DeliveryAt))
			}
			b.WriteString("\n")

			for _, l := range s.Lines {
				b.WriteString("  • ")
				b.WriteString(l.Name)
				b.WriteString(" — ")
				b.WriteString(formatOrderQty(l))
				if !l.StockoutDate.IsZero() {
					b.WriteString(", сейчас хватит до ")
					b.WriteString(weekdayShort(l.StockoutDate))
				}
				b.WriteString("\n")
			}
		}
	}

	if len(d.Critical) > 0 {
		fmt.Fprintf(&b, "\nКритично (%d)\n", len(d.Critical))
		for _, c := range d.Critical {
			b.WriteString("  • ")
			b.WriteString(c.Name)
			b.WriteString(" — ")
			b.WriteString(stockoutPhrase(c.StockoutDate, d.Day))
			if !c.NextDelivery.IsZero() {
				b.WriteString(", ближайшая поставка в ")
				b.WriteString(weekdayShort(c.NextDelivery))
			}
			b.WriteString("\n")
		}
	}

	if d.DashboardURL != "" {
		b.WriteString("\nОткрыть дашборд →\n")
	}

	payload := Payload{
		Title: fmt.Sprintf("Сводка на %s", shortDate(d.Day)),
		Body:  b.String(),
		Link:  d.DashboardURL,
	}
	for _, s := range d.Suppliers {
		for _, l := range s.Lines {
			payload.Items = append(payload.Items, PayloadItem{
				Name: l.Name,
				Qty:  l.Qty,
				Unit: l.Unit,
				Note: s.Name,
			})
		}
	}
	return payload
}

// CriticalData — данные срочного алерта.
type CriticalData struct {
	ItemName     string
	OnHand       qty.Qty
	Unit         string
	StockoutDate clock.Day
	Today        clock.Day
	NextDelivery clock.Day
	SupplierName string
	Link         string
}

// RenderCritical собирает текст срочного алерта.
func RenderCritical(d CriticalData) Payload {
	var b strings.Builder

	b.WriteString(d.ItemName)
	b.WriteString(" — ")

	if !d.OnHand.IsPositive() {
		b.WriteString("закончилось")
	} else {
		b.WriteString(stockoutPhrase(d.StockoutDate, d.Today))
		b.WriteString(" (сейчас ")
		b.WriteString(d.OnHand.Human())
		b.WriteString(" ")
		b.WriteString(unitLabel(d.Unit))
		b.WriteString(")")
	}

	if !d.NextDelivery.IsZero() {
		b.WriteString(". Ближайшая поставка в ")
		b.WriteString(weekdayShort(d.NextDelivery))
		if d.SupplierName != "" {
			b.WriteString(" от «")
			b.WriteString(d.SupplierName)
			b.WriteString("»")
		}
		b.WriteString(" — нужна срочная закупка.")
	}

	return Payload{
		Title: "Срочно: " + d.ItemName,
		Body:  b.String(),
		Link:  d.Link,
	}
}

// CutoffData — напоминание за 2 часа до отсечки.
type CutoffData struct {
	SupplierName string
	Cutoff       clock.TimeOfDay
	ItemCount    int
	Link         string
}

// RenderCutoff собирает текст напоминания до отсечки.
func RenderCutoff(d CutoffData) Payload {
	body := fmt.Sprintf(
		"«%s» принимает заказы до %s. %s всё ещё не заказано.",
		d.SupplierName, d.Cutoff.String(), pluralItems(d.ItemCount),
	)
	return Payload{
		Title: "Скоро отсечка: " + d.SupplierName,
		Body:  body,
		Link:  d.Link,
	}
}

// LateData — поставка опаздывает.
type LateData struct {
	SupplierName string
	ExpectedAt   clock.Day
	ItemCount    int
	Link         string
}

// RenderLate собирает текст алерта об опоздании.
func RenderLate(d LateData) Payload {
	body := fmt.Sprintf(
		"Поставка от «%s» ожидалась %s и не принята. %s ждут приёмки.",
		d.SupplierName, shortDate(d.ExpectedAt), pluralItems(d.ItemCount),
	)
	return Payload{
		Title: "Поставка опаздывает: " + d.SupplierName,
		Body:  body,
		Link:  d.Link,
	}
}

// MismatchData — расхождение при приёмке.
type MismatchData struct {
	SupplierName string
	Lines        []MismatchLine
	Link         string
}

// MismatchLine — строка расхождения.
type MismatchLine struct {
	Name     string
	Ordered  qty.Qty
	Received qty.Qty
	Unit     string
}

// RenderMismatch собирает текст уведомления о расхождении.
func RenderMismatch(d MismatchData) Payload {
	var b strings.Builder
	fmt.Fprintf(&b, "Приёмка от «%s» расходится с заказом:\n", d.SupplierName)

	for _, l := range d.Lines {
		diff := l.Received.Sub(l.Ordered)
		if diff.IsZero() {
			continue
		}
		sign := ""
		if diff.IsPositive() {
			sign = "+"
		}
		fmt.Fprintf(&b, "  • %s — заказано %s %s, принято %s %s (%s%s)\n",
			l.Name,
			l.Ordered.Human(), unitLabel(l.Unit),
			l.Received.Human(), unitLabel(l.Unit),
			sign, diff.Human(),
		)
	}

	return Payload{
		Title: "Расхождение при приёмке",
		Body:  b.String(),
		Link:  d.Link,
	}
}

// --- форматирование дат ---

var weekdaysShort = [...]string{"", "пн", "вт", "ср", "чт", "пт", "сб", "вс"}

var weekdaysLong = [...]string{
	"", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота", "воскресенье",
}

var monthsGenitive = [...]string{
	"", "января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

var monthsShort = [...]string{
	"", "янв", "фев", "мар", "апр", "мая", "июн",
	"июл", "авг", "сен", "окт", "ноя", "дек",
}

// longDate — «среда, 23 сентября».
func longDate(d clock.Day) string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s, %d %s", weekdaysLong[d.Weekday()], d.Date, monthsGenitive[int(d.Month)])
}

// shortDate — «24 сент».
func shortDate(d clock.Day) string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%d %s", d.Date, monthsShort[int(d.Month)])
}

// weekdayShort — «чт».
func weekdayShort(d clock.Day) string {
	if d.IsZero() {
		return ""
	}
	return weekdaysShort[d.Weekday()]
}

// stockoutPhrase — «хватит только до завтра», «хватит только до пт».
//
// Формулировка намеренно безличная: позиции бывают любого рода и числа
// («молоко закончится», но «стаканы закончатся»), и подбирать окончание
// по названию система не умеет.
func stockoutPhrase(stockout, today clock.Day) string {
	if stockout.IsZero() {
		return "запас на исходе"
	}
	switch stockout.Sub(today) {
	case 0:
		return "хватит только на сегодня"
	case 1:
		return "хватит только до завтра"
	default:
		return "хватит только до " + weekdayShort(stockout)
	}
}

// formatOrderQty — «2 кор. (24 л)» или «24 л», если единицы закупки нет.
func formatOrderQty(l DigestLine) string {
	if l.PurchaseQty != nil && l.PurchaseUnit != "" {
		return fmt.Sprintf("%s %s (%s %s)",
			l.PurchaseQty.Human(), l.PurchaseUnit, l.Qty.Human(), unitLabel(l.Unit))
	}
	return fmt.Sprintf("%s %s", l.Qty.Human(), unitLabel(l.Unit))
}

// pluralItems — «1 позиция», «2 позиции», «5 позиций».
func pluralItems(n int) string {
	abs := n % 100
	last := abs % 10

	switch {
	case abs > 10 && abs < 20:
		return fmt.Sprintf("%d позиций", n)
	case last > 1 && last < 5:
		return fmt.Sprintf("%d позиции", n)
	case last == 1:
		return fmt.Sprintf("%d позиция", n)
	default:
		return fmt.Sprintf("%d позиций", n)
	}
}
