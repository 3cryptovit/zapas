package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/httpx"
	"github.com/vostapenko/zapas/internal/platform/logging"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Имена cookie. Префикс общий, чтобы их было видно среди чужих в браузере.
const (
	SessionCookie = "zapas_session"
	CSRFCookie    = "zapas_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// Handler — HTTP-обвязка входа: маршруты, middleware и cookie.
type Handler struct {
	svc *Service
	// secure выставляет флаг Secure у cookie. В проде обязателен (§12.2),
	// локально мешает: по http такая cookie не поедет.
	secure bool
	// baseURL нужен для проверки Origin при защите от CSRF.
	baseURL string
	// cookiePath — путь приложения: на том же домене живут чужие страницы,
	// и сессия Zapas им не нужна.
	cookiePath string
}

func NewHandler(svc *Service, secure bool, baseURL string) *Handler {
	return &Handler{svc: svc, secure: secure, baseURL: baseURL,
		cookiePath: httpx.BasePath(baseURL) + "/"}
}

// Routes подключает публичные маршруты: вход доступен без сессии.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/auth/login", h.handleLogin)
}

// ProtectedRoutes подключает маршруты, которым нужна действующая сессия:
// они регистрируются внутри группы с middleware Require.
func (h *Handler) ProtectedRoutes(r chi.Router) {
	r.Post("/auth/logout", h.handleLogout)
	r.Get("/me", h.handleMe)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Honeypot — поле-ловушка для ботов: настоящий браузер его не заполнит.
	Honeypot string `json:"website,omitempty"`
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Honeypot != "" {
		// Бот заполнил скрытое поле. Отвечаем как на неверный пароль.
		httpx.Error(w, r, invalidCredentials())
		return
	}

	var fields []httpx.FieldError
	if strings.TrimSpace(req.Email) == "" {
		fields = append(fields, httpx.FieldError{Field: "email", Message: "Укажите email."})
	}
	if req.Password == "" {
		fields = append(fields, httpx.FieldError{Field: "password", Message: "Укажите пароль."})
	}
	if len(fields) > 0 {
		httpx.Error(w, r, httpx.Invalid(fields...))
		return
	}

	result, err := h.svc.Login(r.Context(), req.Email, req.Password,
		clientIP(r), r.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			httpx.Error(w, r, invalidCredentials())
		case errors.Is(err, ErrTooManyAttempts):
			httpx.Error(w, r, httpx.TooManyRequests(
				"Слишком много попыток входа. Попробуйте через минуту."))
		default:
			httpx.Error(w, r, err)
		}
		return
	}

	h.setCookies(w, result)
	httpx.JSON(w, http.StatusOK, meResponse(result.Principal, h.svc.clock))
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Выход без сессии — не ошибка: кнопка должна работать всегда.
	if principal, ok := FromContext(r.Context()); ok {
		if cookie, err := r.Cookie(SessionCookie); err == nil {
			if err := h.svc.Logout(r.Context(), cookie.Value, principal.Tenant.ID); err != nil {
				httpx.Error(w, r, err)
				return
			}
		}
	}
	h.clearCookies(w)
	httpx.NoContent(w)
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := FromContext(r.Context())
	if !ok {
		httpx.Error(w, r, httpx.Unauthorized())
		return
	}
	httpx.JSON(w, http.StatusOK, meResponse(principal, h.svc.clock))
}

// StartSessionFor заводит сессию без пароля и выставляет cookie.
//
// Нужен песочнице: демо создаётся одной кнопкой и сразу пускает
// посетителя внутрь (§7.1). Для обычного входа есть handleLogin.
func (h *Handler) StartSessionFor(ctx context.Context, w http.ResponseWriter, userID, tenantID uuid.UUID, userAgent string) (LoginResult, error) {
	result, err := h.svc.StartSession(ctx, userID, tenantID, userAgent)
	if err != nil {
		return LoginResult{}, err
	}
	h.setCookies(w, result)
	return result, nil
}

// Require — middleware аутентификации. Кладёт в контекст принципала
// и тенанта; без действующей сессии отвечает 401.
// CurrentPrincipal возвращает владельца сессии, если она есть и жива.
//
// В отличие от Require ничего не требует и не отвечает ошибкой: нужен
// публичным маршрутам, которым полезно знать, что посетитель уже внутри,
// но которые работают и без этого.
func (h *Handler) CurrentPrincipal(r *http.Request) (Principal, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return Principal{}, false
	}
	principal, err := h.svc.Resolve(r.Context(), cookie.Value)
	if err != nil {
		return Principal{}, false
	}
	return principal, true
}

func (h *Handler) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookie)
		if err != nil {
			httpx.Error(w, r, httpx.Unauthorized())
			return
		}

		principal, err := h.svc.Resolve(r.Context(), cookie.Value)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				// Просроченную cookie стираем, иначе браузер шлёт её вечно.
				h.clearCookies(w)
				httpx.Error(w, r, httpx.Unauthorized())
				return
			}
			httpx.Error(w, r, err)
			return
		}

		// Изменяющие запросы защищены double-submit токеном и проверкой Origin.
		if isMutating(r.Method) {
			if err := h.checkCSRF(r, principal); err != nil {
				httpx.Error(w, r, err)
				return
			}
		}

		ctx := WithPrincipal(r.Context(), principal)
		ctx = tenant.WithTenant(ctx, principal.Tenant)
		ctx = logging.WithTenantID(ctx, principal.Tenant.ID.String())
		ctx = logging.WithUserID(ctx, principal.UserID.String())

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOwner пропускает только владельца.
func RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := FromContext(r.Context())
		if !ok {
			httpx.Error(w, r, httpx.Unauthorized())
			return
		}
		if principal.Role != RoleOwner {
			httpx.Error(w, r, httpx.Forbidden("Это действие доступно только владельцу."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// DenySandbox закрывает действия, недоступные в демо: внешние каналы,
// приглашение пользователей, смена email и пароля (§7.5).
func DenySandbox(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := FromContext(r.Context())
		if ok && principal.Tenant.IsSandbox {
			httpx.Error(w, r, httpx.Forbidden("В демо это действие отключено."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) checkCSRF(r *http.Request, p Principal) error {
	header := r.Header.Get(CSRFHeader)
	if header == "" {
		return httpx.Forbidden("Отсутствует заголовок " + CSRFHeader + ".")
	}
	if !EqualTokens(HashToken(header), p.CSRFHash) {
		return httpx.Forbidden("Неверный CSRF-токен.")
	}

	// Origin проверяется дополнительно: заголовок подделать из браузера нельзя.
	// Его отсутствие допустимо — некоторые клиенты его не шлют, — а вот чужое
	// значение означает запрос со стороннего сайта.
	if origin := r.Header.Get("Origin"); origin != "" && h.baseURL != "" {
		if !httpx.SameOrigin(origin, h.baseURL) {
			return httpx.Forbidden("Запрос пришёл со стороннего адреса.")
		}
	}
	return nil
}

func (h *Handler) setCookies(w http.ResponseWriter, result LoginResult) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    result.SessionToken,
		Path:     h.cookiePath,
		Expires:  result.ExpiresAt,
		HttpOnly: true, // недоступна из JavaScript: XSS не уносит сессию
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:  CSRFCookie,
		Value: result.CSRFToken,
		Path:  h.cookiePath,
		// Читается скриптом намеренно: фронтенд копирует значение в заголовок.
		HttpOnly: false,
		Expires:  result.ExpiresAt,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{SessionCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     h.cookiePath,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
			HttpOnly: name == SessionCookie,
			Secure:   h.secure,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func invalidCredentials() error {
	return httpx.New(http.StatusUnauthorized, "invalid-credentials",
		"Неверный email или пароль",
		"Проверьте адрес и пароль и попробуйте ещё раз.")
}

func clientIP(r *http.Request) string {
	// middleware.RealIP уже разобрал X-Forwarded-For.
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i != -1 && strings.Count(host, ":") == 1 {
		host = host[:i]
	}
	return host
}

// --- контекст ---

type principalKey struct{}

// WithPrincipal кладёт принципала в контекст.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext достаёт принципала.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// MustFromContext достаёт принципала; паника означает ошибку проводки маршрутов.
func MustFromContext(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("auth: в контексте нет принципала — маршрут не закрыт middleware")
	}
	return p
}
