package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TelegramSender шлёт сообщения через Bot API.
//
// Токен и секрет вебхука живут только в переменных окружения (§12.2);
// в репозитории их нет.
type TelegramSender struct {
	token  string
	client *http.Client
	// apiBase вынесен в поле, чтобы тест мог подставить httptest-сервер.
	apiBase string
}

func NewTelegramSender(token string) *TelegramSender {
	return &TelegramSender{
		token:   token,
		client:  &http.Client{Timeout: 10 * time.Second},
		apiBase: "https://api.telegram.org",
	}
}

// Configured сообщает, что канал можно использовать.
func (s *TelegramSender) Configured() bool { return s != nil && s.token != "" }

// Send отправляет текст в чат.
func (s *TelegramSender) Send(ctx context.Context, chatID string, payload Payload) error {
	if !s.Configured() {
		return fmt.Errorf("%w: не задан TELEGRAM_BOT_TOKEN", ErrPermanent)
	}

	body, err := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    telegramText(payload),
		// HTML отключён намеренно: названия позиций вводит пользователь,
		// и экранировать их на каждом шаге — лишний источник ошибок.
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("telegram: сериализация: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", s.apiBase, s.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: запрос: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// Сеть моргнула — это повод повторить.
		return fmt.Errorf("telegram: отправка: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusForbidden, http.StatusBadRequest:
		// Пользователь заблокировал бота или чат не существует:
		// повторять бессмысленно.
		return fmt.Errorf("%w: telegram ответил %d", ErrPermanent, resp.StatusCode)
	case http.StatusTooManyRequests:
		return fmt.Errorf("telegram: превышен лимит запросов")
	default:
		return fmt.Errorf("telegram: ответ %d", resp.StatusCode)
	}
}

// telegramText собирает текст сообщения: заголовок отдельной строкой,
// если он не дублирует начало тела.
func telegramText(p Payload) string {
	body := strings.TrimRight(p.Body, "\n")
	if body == "" {
		return p.Title
	}
	if p.Link != "" {
		body += "\n\n" + p.Link
	}
	return body
}
