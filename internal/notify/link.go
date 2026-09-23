package notify

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// LinkCodeTTL — код привязки живёт 15 минут (§5.5).
const LinkCodeTTL = 15 * time.Minute

// Ошибки привязки.
var (
	ErrLinkCodeInvalid = errors.New("notify: код привязки неверен или истёк")
	ErrChatTaken       = errors.New("notify: этот чат уже привязан к другому аккаунту")
)

// LinkInvite — ссылка, по которой пользователь привязывает Telegram.
type LinkInvite struct {
	// URL вида t.me/<bot>?start=<код>.
	URL       string    `json:"url"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateLinkInvite выдаёт одноразовый код привязки (§5.5).
//
// Код короткий и одноразовый: он живёт 15 минут и сгорает при первом
// использовании, поэтому перехват ссылки из истории чата бесполезен.
func (s *Service) CreateLinkInvite(ctx context.Context, t tenant.Tenant, userID uuid.UUID, botUsername string) (LinkInvite, error) {
	if botUsername == "" {
		return LinkInvite{}, fmt.Errorf("notify: не задано имя бота")
	}

	code, err := newLinkCode()
	if err != nil {
		return LinkInvite{}, err
	}
	expiresAt := s.clock.Now().Add(LinkCodeTTL)

	err = s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).CreateTelegramLinkCode(ctx, sqlc.CreateTelegramLinkCodeParams{
			Code:      code,
			TenantID:  t.ID,
			UserID:    userID,
			ExpiresAt: postgres.Time(expiresAt),
		})
	})
	if err != nil {
		return LinkInvite{}, fmt.Errorf("notify: код привязки: %w", err)
	}

	return LinkInvite{
		URL:       fmt.Sprintf("https://t.me/%s?start=%s", botUsername, code),
		Code:      code,
		ExpiresAt: expiresAt,
	}, nil
}

// LinkChat связывает чат Telegram с пользователем по коду.
//
// Выполняется обслуживающей ролью: вебхук приходит от Telegram, и тенант
// в этот момент известен только из самого кода (ADR-005).
func (s *Service) LinkChat(ctx context.Context, code string, chatID int64) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", ErrLinkCodeInvalid
	}

	var greeting string

	err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		// Код гасится тем же запросом, что и читается: повторное
		// использование невозможно даже при гонке.
		row, err := q.UseTelegramLinkCode(ctx, sqlc.UseTelegramLinkCodeParams{
			Code:   code,
			UsedAt: postgres.Time(s.clock.Now()),
		})
		if err != nil {
			if postgres.IsNoRows(err) {
				return ErrLinkCodeInvalid
			}
			return fmt.Errorf("notify: чтение кода: %w", err)
		}

		// Один чат — один пользователь: старую привязку снимаем,
		// иначе уникальный индекс не даст записать новую.
		if err := q.ClearUserTelegramChatID(ctx, &chatID); err != nil {
			return fmt.Errorf("notify: снятие старой привязки: %w", err)
		}
		if err := q.SetUserTelegramChatID(ctx, sqlc.SetUserTelegramChatIDParams{
			ID:             row.UserID,
			TelegramChatID: &chatID,
		}); err != nil {
			return fmt.Errorf("notify: привязка чата: %w", err)
		}

		user, err := q.GetUserByID(ctx, row.UserID)
		if err == nil {
			greeting = user.Name
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return greeting, nil
}

// UnlinkChat снимает привязку по команде /stop (§5.5).
func (s *Service) UnlinkChat(ctx context.Context, chatID int64) error {
	return s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).ClearUserTelegramChatID(ctx, &chatID)
	})
}

// newLinkCode выдаёт короткий код без похожих символов.
func newLinkCode() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("notify: генерация кода: %w", err)
	}
	// base32 без паддинга: код читается вслух и не содержит 0/O и 1/l.
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)), nil
}
