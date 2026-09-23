// Package notify отвечает за ленту в интерфейсе, очередь отправки (outbox)
// и сами сообщения (§5.5, ADR-004).
//
// Уведомление пишется в ту же транзакцию, что и событие, которое его
// породило. Отправкой занимается воркер: внешние каналы не должны влиять
// на время ответа API.
package notify

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Type — вид уведомления (§5.5).
type Type string

const (
	// TypeDailyDigest — ежедневная сводка, по умолчанию в 09:00.
	TypeDailyDigest Type = "daily_digest"
	// TypeCritical — позиция стала красной.
	TypeCritical Type = "critical"
	// TypeCutoffReminder — за 2 часа до отсечки, если заказ не отправлен.
	TypeCutoffReminder Type = "cutoff_reminder"
	// TypeOrderLate — поставка опаздывает.
	TypeOrderLate Type = "order_late"
	// TypeReceiptMismatch — расхождение при приёмке больше 5%.
	TypeReceiptMismatch Type = "receipt_mismatch"
)

// Channel — канал доставки. Лента в интерфейсе есть всегда и каналом
// не считается: она и есть таблица notifications.
type Channel string

const (
	ChannelTelegram Channel = "telegram"
	ChannelEmail    Channel = "email"
)

// MaxAttempts — сколько раз пробуем отправить, прежде чем сдаться (ADR-004).
const MaxAttempts = 5

// backoff — экспоненциальная задержка между попытками: 1, 5, 25 минут и далее.
func backoff(attempt int) int {
	minutes := 1
	for i := 1; i < attempt; i++ {
		minutes *= 5
	}
	if minutes > 24*60 {
		minutes = 24 * 60
	}
	return minutes
}

// DedupKey строит ключ дедупликации. Уникальный индекс по нему не даёт
// отправить одно и то же дважды (§5.5).
//
//	critical:<item_id>:<date>       — один на позицию в день
//	digest:<date>                   — одна сводка в день
//	cutoff:<supplier_id>:<date>     — одно напоминание на поставщика в день
//	late:<order_id>                 — одно на заказ
//	mismatch:<order_id>             — одно на заказ
func DedupKey(parts ...string) string {
	key := ""
	for i, p := range parts {
		if i > 0 {
			key += ":"
		}
		key += p
	}
	return key
}

// CriticalKey — ключ срочного алерта: один на позицию в день.
func CriticalKey(itemID uuid.UUID, day clock.Day) string {
	return DedupKey("critical", itemID.String(), day.String())
}

// DigestKey — ключ ежедневной сводки.
func DigestKey(day clock.Day) string { return DedupKey("digest", day.String()) }

// CutoffKey — ключ напоминания до отсечки.
func CutoffKey(supplierID uuid.UUID, day clock.Day) string {
	return DedupKey("cutoff", supplierID.String(), day.String())
}

// LateKey — ключ алерта об опоздании поставки.
func LateKey(orderID uuid.UUID) string { return DedupKey("late", orderID.String()) }

// MismatchKey — ключ уведомления о расхождении при приёмке.
func MismatchKey(orderID uuid.UUID) string { return DedupKey("mismatch", orderID.String()) }

// Payload — содержимое уведомления. Хранится как jsonb и рендерится
// в текст при отправке и в ленте.
type Payload struct {
	Title string `json:"title"`
	// Body — готовый текст для Telegram и ленты.
	Body string `json:"body"`
	// Link — куда вести по клику: позиция или заказ.
	Link string `json:"link,omitempty"`
	// Items — краткая выжимка для ленты, чтобы не парсить Body.
	Items []PayloadItem `json:"items,omitempty"`
}

// PayloadItem — строка уведомления.
type PayloadItem struct {
	ItemID uuid.UUID `json:"item_id,omitempty"`
	Name   string    `json:"name"`
	Qty    qty.Qty   `json:"qty,omitempty"`
	Unit   string    `json:"unit,omitempty"`
	Note   string    `json:"note,omitempty"`
}

// Notification — запись ленты.
type Notification struct {
	ID        uuid.UUID  `json:"id"`
	Type      Type       `json:"type"`
	TypeLabel string     `json:"type_label"`
	Payload   Payload    `json:"payload"`
	Read      bool       `json:"read"`
	CreatedAt string     `json:"created_at"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
}

// Label — вид уведомления по-русски.
func (t Type) Label() string {
	switch t {
	case TypeDailyDigest:
		return "Сводка"
	case TypeCritical:
		return "Срочно"
	case TypeCutoffReminder:
		return "Напоминание"
	case TypeOrderLate:
		return "Опоздание"
	case TypeReceiptMismatch:
		return "Расхождение"
	default:
		return string(t)
	}
}

// unitLabel — единица измерения по-русски.
func unitLabel(unit string) string {
	switch unit {
	case "kg":
		return "кг"
	case "l":
		return "л"
	case "pcs":
		return "шт"
	default:
		return unit
	}
}

// ErrNoRecipients — некому слать: у тенанта нет владельца с привязанным каналом.
var ErrNoRecipients = fmt.Errorf("notify: нет получателей")
