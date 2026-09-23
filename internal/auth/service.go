package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/ratelimit"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Ошибки уровня сервиса.
var (
	// ErrInvalidCredentials одинакова и для неизвестного email, и для
	// неверного пароля: иначе по ответу можно перебрать, кто зарегистрирован.
	ErrInvalidCredentials = errors.New("auth: неверный email или пароль")
	ErrSessionNotFound    = errors.New("auth: сессия не найдена или истекла")
	ErrTooManyAttempts    = errors.New("auth: слишком много попыток входа")
	ErrSandboxRestricted  = errors.New("auth: действие недоступно в демо")
)

// Ограничения входа (§12.2): 5 попыток в минуту на IP и на email.
const (
	loginLimit  = 5
	loginWindow = time.Minute
)

// Principal — кто выполняет запрос.
type Principal struct {
	UserID         uuid.UUID
	Email          string
	Name           string
	Role           Role
	TelegramChatID *int64
	Tenant         tenant.Tenant
	// CSRFHash — хеш токена double-submit, сверяется с заголовком.
	CSRFHash []byte
}

// Service — вход, сессии и разбор принципала.
type Service struct {
	// db работает под ролью приложения: RLS действует.
	db *postgres.DB
	// maint — обслуживающая роль (BYPASSRLS). Нужна ровно в двух местах:
	// поиск пользователя по email при входе и разбор сессии по хешу токена.
	// В обоих случаях тенант ещё не известен (ADR-005).
	maint   *postgres.DB
	clock   clock.Clock
	limiter *ratelimit.Limiter
	ttl     time.Duration
}

func NewService(db, maint *postgres.DB, cl clock.Clock, limiter *ratelimit.Limiter, ttl time.Duration) *Service {
	return &Service{db: db, maint: maint, clock: cl, limiter: limiter, ttl: ttl}
}

// LoginResult — что отдаётся клиенту после успешного входа.
type LoginResult struct {
	Principal    Principal
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
}

// Login проверяет пароль и заводит сессию.
//
// Ответ на неверный email и на неверный пароль одинаков и занимает примерно
// одинаковое время: разница в любом из них позволяет перебрать, какие адреса
// зарегистрированы.
func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if err := s.checkLoginLimit(ctx, ip, email); err != nil {
		return LoginResult{}, err
	}

	var user sqlc.User
	err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		u, err := sqlc.New(tx).GetUserByEmail(ctx, email)
		if err != nil {
			return err
		}
		user = u
		return nil
	})
	if err != nil {
		if postgres.IsNoRows(err) {
			// Пользователя нет — всё равно тратим время на проверку пароля.
			BurnTime()
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("auth: поиск пользователя: %w", err)
	}

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil {
		return LoginResult{}, fmt.Errorf("auth: проверка пароля: %w", err)
	}
	if !ok {
		return LoginResult{}, ErrInvalidCredentials
	}

	result, err := s.startSession(ctx, user.ID, user.TenantID, userAgent)
	if err != nil {
		return LoginResult{}, err
	}

	// Удачный вход снимает счётчик попыток.
	_ = s.limiter.Reset(ctx, "login:ip:"+ip)
	_ = s.limiter.Reset(ctx, "login:email:"+email)

	s.audit(ctx, user.TenantID, &user.ID, "login", "user", &user.ID, ip)
	return result, nil
}

// StartSession заводит сессию без проверки пароля. Нужен песочнице: демо
// создаётся одной кнопкой и сразу пускает посетителя внутрь (§7.1).
func (s *Service) StartSession(ctx context.Context, userID, tenantID uuid.UUID, userAgent string) (LoginResult, error) {
	return s.startSession(ctx, userID, tenantID, userAgent)
}

func (s *Service) startSession(ctx context.Context, userID, tenantID uuid.UUID, userAgent string) (LoginResult, error) {
	token, tokenHash, err := NewToken()
	if err != nil {
		return LoginResult{}, err
	}
	csrfToken, csrfHash, err := NewCSRFToken()
	if err != nil {
		return LoginResult{}, err
	}

	now := s.clock.Now()
	expiresAt := now.Add(s.ttl)

	err = s.db.InTenantTx(ctx, tenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).CreateSession(ctx, sqlc.CreateSessionParams{
			TokenHash: tokenHash,
			UserID:    userID,
			TenantID:  tenantID,
			CsrfHash:  csrfHash,
			UserAgent: truncate(userAgent, 256),
			ExpiresAt: postgres.Time(expiresAt),
		})
	})
	if err != nil {
		return LoginResult{}, fmt.Errorf("auth: создание сессии: %w", err)
	}

	principal, err := s.Resolve(ctx, token)
	if err != nil {
		return LoginResult{}, err
	}

	return LoginResult{
		Principal:    principal,
		SessionToken: token,
		CSRFToken:    csrfToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// Resolve разбирает токен сессии в принципала.
//
// Запрос кросс-тенантный по необходимости: тенант как раз и определяется
// по сессии. Дальше все обращения к данным идут уже под ролью приложения
// с выставленным app.tenant_id.
func (s *Service) Resolve(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrSessionNotFound
	}

	var row sqlc.GetSessionRow
	err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		r, err := sqlc.New(tx).GetSession(ctx, sqlc.GetSessionParams{
			TokenHash: HashToken(token),
			ExpiresAt: postgres.Time(s.clock.Now()),
		})
		if err != nil {
			return err
		}
		row = r
		return nil
	})
	if err != nil {
		if postgres.IsNoRows(err) {
			return Principal{}, ErrSessionNotFound
		}
		return Principal{}, fmt.Errorf("auth: разбор сессии: %w", err)
	}

	// Истёкшая песочница не должна пускать внутрь, даже если сессия ещё жива:
	// её данные вот-вот удалит фоновая задача.
	if row.IsSandbox && row.TenantExpiresAt.Valid &&
		!row.TenantExpiresAt.Time.After(s.clock.Now()) {
		return Principal{}, ErrSessionNotFound
	}

	return Principal{
		UserID:         row.UserID,
		Email:          row.Email,
		Name:           row.Name,
		Role:           Role(row.Role),
		TelegramChatID: row.TelegramChatID,
		CSRFHash:       row.CsrfHash,
		Tenant: tenant.Tenant{
			ID:          row.TenantID,
			Name:        row.TenantName,
			Location:    tenant.LoadLocation(row.Timezone),
			IsSandbox:   row.IsSandbox,
			ClockOffset: postgres.Duration(row.ClockOffset),
			ExpiresAt:   postgres.TimePtr(row.TenantExpiresAt),
			Autopilot:   row.Autopilot,
			Seed:        row.Seed,
			Settings:    tenant.ParseSettings(row.Settings),
		},
	}, nil
}

// Touch продлевает сессию. Вызывается не чаще раза в час, чтобы не писать
// в БД на каждом запросе.
func (s *Service) Touch(ctx context.Context, token string, tenantID uuid.UUID) error {
	now := s.clock.Now()
	return s.db.InTenantTx(ctx, tenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).TouchSession(ctx, sqlc.TouchSessionParams{
			TokenHash:  HashToken(token),
			LastSeenAt: postgres.Time(now),
			ExpiresAt:  postgres.Time(now.Add(s.ttl)),
		})
	})
}

// Logout удаляет сессию: после выхода токен не действует немедленно (ADR-007).
func (s *Service) Logout(ctx context.Context, token string, tenantID uuid.UUID) error {
	return s.db.InTenantTx(ctx, tenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).DeleteSession(ctx, HashToken(token))
	})
}

func (s *Service) checkLoginLimit(ctx context.Context, ip, email string) error {
	// Лимит и по IP, и по email: первый ловит перебор паролей к одному
	// аккаунту, второй — перебор аккаунтов с разных адресов.
	for _, key := range []string{"login:ip:" + ip, "login:email:" + email} {
		res, err := s.limiter.Allow(ctx, key, loginLimit, loginWindow)
		if err != nil {
			// Redis лёг — вход важнее лимита, пропускаем.
			continue
		}
		if !res.Allowed {
			return ErrTooManyAttempts
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
