package sandbox

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/httpx"
	"github.com/vostapenko/zapas/internal/platform/metrics"
	"github.com/vostapenko/zapas/internal/platform/ratelimit"
	"github.com/vostapenko/zapas/internal/tenant"
)

// Лимиты защиты от злоупотреблений (§7.5).
const (
	// PerIPPerHour — не больше пяти песочниц с одного адреса в час.
	PerIPPerHour = 5
	// MaxActive — сверх этого новые не создаются, лендинг показывает видео.
	MaxActive = 300
)

// Handler — HTTP-обвязка песочницы.
type Handler struct {
	svc       *Service
	simulator *Simulator
	auth      *auth.Handler
	limiter   *ratelimit.Limiter
	maxActive int
	perIP     int
}

func NewHandler(svc *Service, sim *Simulator, authHandler *auth.Handler, limiter *ratelimit.Limiter, maxActive, perIP int) *Handler {
	if maxActive <= 0 {
		maxActive = MaxActive
	}
	if perIP <= 0 {
		perIP = PerIPPerHour
	}
	return &Handler{
		svc: svc, simulator: sim, auth: authHandler, limiter: limiter,
		maxActive: maxActive, perIP: perIP,
	}
}

// PublicRoutes — создание демо доступно без входа.
func (h *Handler) PublicRoutes(r chi.Router) {
	r.Post("/sandbox", h.handleCreate)
}

// ProtectedRoutes — управление демо изнутри кабинета.
func (h *Handler) ProtectedRoutes(r chi.Router) {
	r.Post("/sandbox/advance", h.handleAdvance)
	r.Post("/sandbox/reset", h.handleReset)
	r.Put("/sandbox/autopilot", h.handleAutopilot)
}

// appPath — куда вести посетителя: кабинет под путём публичного адреса.
// Слеш на конце обязателен: без него nginx отдаёт не кабинет, а лендинг,
// и переход выглядит так, будто кнопка не сработала.
func (h *Handler) appPath() string {
	return httpx.BasePath(h.svc.baseURL()) + "/app/"
}

type createRequest struct {
	// Honeypot — поле-ловушка: настоящий браузер его не заполняет (§7.5).
	Honeypot string `json:"website,omitempty"`
}

type createResponse struct {
	TenantID  string `json:"tenant_id"`
	ExpiresAt string `json:"expires_at"`
	// RedirectTo — куда вести посетителя после создания.
	RedirectTo string `json:"redirect_to"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(w, r, &req); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	if req.Honeypot != "" {
		// Бот заполнил скрытое поле. Отвечаем так же, как на превышение
		// лимита: подсказывать, что ловушка сработала, незачем.
		metrics.SandboxCreated.WithLabelValues("bot").Inc()
		httpx.Error(w, r, tooManyDemos())
		return
	}

	// Проверка заголовка Origin: обычный переход с лендинга его присылает.
	if origin := r.Header.Get("Origin"); origin != "" && !h.originAllowed(origin) {
		metrics.SandboxCreated.WithLabelValues("bad_origin").Inc()
		httpx.Error(w, r, httpx.Forbidden("Запрос пришёл со стороннего адреса."))
		return
	}

	// Посетитель уже внутри живого демо — возвращаем его туда же.
	//
	// Иначе повторный клик по кнопке заводит второе демо и тратит лимит,
	// хотя человек просто хотел попасть обратно. Именно так люди в него и
	// упираются: не ботом, а вторым нажатием.
	if p, ok := h.auth.CurrentPrincipal(r); ok && p.Tenant.IsSandbox &&
		h.svc.liveSandbox(p.Tenant) {
		metrics.SandboxCreated.WithLabelValues("reused").Inc()
		httpx.JSON(w, http.StatusOK, createResponse{
			TenantID:   p.Tenant.ID.String(),
			ExpiresAt:  expiresAt(p.Tenant),
			RedirectTo: h.appPath(),
		})
		return
	}

	ip := clientIP(r)
	res, err := h.limiter.Allow(ctx, "sandbox:ip:"+ip, h.perIP, time.Hour)
	if err == nil && !res.Allowed {
		metrics.SandboxCreated.WithLabelValues("rate_limited").Inc()
		httpx.Error(w, r, tooManyDemos())
		return
	}

	// Потолок активных демо: сверх него лендинг показывает видео (§7.5).
	active, err := h.svc.ActiveCount(ctx)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if active >= h.maxActive {
		metrics.SandboxCreated.WithLabelValues("capacity").Inc()
		httpx.Error(w, r, httpx.New(http.StatusServiceUnavailable, "sandbox-busy",
			"Демо временно недоступно",
			"Сейчас открыто слишком много демо. Попробуйте через несколько минут."))
		return
	}

	created, err := h.svc.Create(ctx, 0)
	if err != nil {
		metrics.SandboxCreated.WithLabelValues("error").Inc()
		httpx.Error(w, r, err)
		return
	}
	metrics.SandboxCreated.WithLabelValues("ok").Inc()

	// Сессия заводится сразу: посетитель попадает в кабинет одной кнопкой.
	login, err := h.auth.StartSessionFor(ctx, w, created.OwnerID, created.TenantID, r.UserAgent())
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	_ = login

	httpx.JSON(w, http.StatusCreated, createResponse{
		TenantID:   created.TenantID.String(),
		ExpiresAt:  created.ExpiresAt.Format(time.RFC3339),
		RedirectTo: h.appPath(),
	})
}

// liveSandbox — демо ещё не истекло. Уборщик ходит раз в час, поэтому
// просроченный тенант какое-то время живёт в базе: пускать в него нельзя.
//
// Время берётся у Clock, а не у time.Now: это правило проекта, и оно же
// позволяет тесту проверить истечение, не ожидая сутки.
func (s *Service) liveSandbox(t tenant.Tenant) bool {
	return t.ExpiresAt != nil && t.ExpiresAt.After(s.clock.Now())
}

func expiresAt(t tenant.Tenant) string {
	if t.ExpiresAt == nil {
		return ""
	}
	return t.ExpiresAt.Format(time.RFC3339)
}

type advanceRequest struct {
	Days int `json:"days"`
}

func (h *Handler) handleAdvance(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var req advanceRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	start := time.Now()
	result, err := h.simulator.Advance(r.Context(), principal.Tenant, req.Days)
	metrics.SandboxAdvance.Observe(time.Since(start).Seconds())

	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) handleReset(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())
	if !principal.Tenant.IsSandbox {
		httpx.Error(w, r, toHTTP(ErrNotSandbox))
		return
	}

	created, err := h.svc.Reset(r.Context(), principal.Tenant.ID, principal.Tenant.Seed)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}

	// Старый тенант удалён вместе с сессией — заводим новую.
	if _, err := h.auth.StartSessionFor(r.Context(), w, created.OwnerID, created.TenantID, r.UserAgent()); err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusOK, createResponse{
		TenantID:   created.TenantID.String(),
		ExpiresAt:  created.ExpiresAt.Format(time.RFC3339),
		RedirectTo: h.appPath(),
	})
}

type autopilotRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *Handler) handleAutopilot(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())
	if !principal.Tenant.IsSandbox {
		httpx.Error(w, r, toHTTP(ErrNotSandbox))
		return
	}

	var req autopilotRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := h.svc.SetAutopilot(r.Context(), principal.Tenant.ID, req.Enabled); err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"autopilot": req.Enabled})
}

func (h *Handler) originAllowed(origin string) bool {
	base := h.svc.baseURL()
	return base == "" || httpx.SameOrigin(origin, base)
}

func tooManyDemos() error {
	return httpx.TooManyRequests(
		"С этого адреса уже открыто несколько демо. Попробуйте через час.")
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i != -1 && strings.Count(host, ":") == 1 {
		host = host[:i]
	}
	return host
}

// toHTTP переводит ошибки симулятора в ответы RFC 9457.
func toHTTP(err error) error {
	switch {
	case errors.Is(err, ErrNotSandbox):
		return httpx.Forbidden("Это действие доступно только в демо.")
	case errors.Is(err, ErrBadDays):
		return httpx.Invalid(httpx.FieldError{
			Field: "days", Message: "Промотать можно на 1 или 7 дней.",
		})
	case errors.Is(err, ErrHorizonLimit):
		return httpx.Conflict("horizon-limit", "Достигнут предел демо",
			"Дальше промотать нельзя. Сбросьте демо, чтобы начать заново.")
	default:
		return err
	}
}
