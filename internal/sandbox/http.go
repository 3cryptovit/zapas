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
	// MaxActive — сверх этого новые не создаются.
	MaxActive = 300
)

// Limits — ограничения создания демо из конфига.
type Limits struct {
	MaxActive int
	PerIP     int
	// Disabled выключает создание демо совсем: аварийный рубильник из
	// раннбука. Отдельный флаг, потому что ноль в MaxActive означает
	// «по умолчанию», и SANDBOX_MAX_ACTIVE=0 демо не выключал.
	Disabled bool
}

// Handler — HTTP-обвязка песочницы.
type Handler struct {
	svc       *Service
	simulator *Simulator
	guide     *Guide
	auth      *auth.Handler
	limiter   *ratelimit.Limiter
	limits    Limits
}

func NewHandler(svc *Service, sim *Simulator, guide *Guide, authHandler *auth.Handler, limiter *ratelimit.Limiter, limits Limits) *Handler {
	if limits.MaxActive <= 0 {
		limits.MaxActive = MaxActive
	}
	if limits.PerIP <= 0 {
		limits.PerIP = PerIPPerHour
	}
	return &Handler{
		svc: svc, simulator: sim, guide: guide, auth: authHandler, limiter: limiter,
		limits: limits,
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
	r.Get("/sandbox/guide", h.handleGuide)
	r.Post("/sandbox/guide/{step}/fill", h.handleFill)
}

// appPath — куда вести посетителя: кабинет под путём публичного адреса.
// Слеш на конце обязателен: без него nginx отдаёт не кабинет, а лендинг,
// и переход выглядит так, будто кнопка не сработала.
func (h *Handler) appPath() string {
	return httpx.BasePath(h.svc.baseURL()) + "/app/"
}

// Режимы демо: готовое с историей или пустое для прохождения по шагам.
const (
	ModeReady  = "ready"
	ModeGuided = "guided"
)

type createRequest struct {
	// Honeypot — поле-ловушка: настоящий браузер его не заполняет (§7.5).
	Honeypot string `json:"website,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// loginDTO — вход в пошаговое демо. Пароль отдаётся один раз: в базе
// лежит только хеш.
type loginDTO struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type createResponse struct {
	TenantID  string `json:"tenant_id"`
	ExpiresAt string `json:"expires_at"`
	// RedirectTo — куда вести посетителя после создания.
	RedirectTo string    `json:"redirect_to"`
	Login      *loginDTO `json:"login,omitempty"`
}

func validMode(mode string) error {
	switch mode {
	case "", ModeReady, ModeGuided:
		return nil
	default:
		return httpx.Invalid(httpx.FieldError{
			Field: "mode", Message: "Режим демо: ready или guided.",
		})
	}
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.limits.Disabled {
		metrics.SandboxCreated.WithLabelValues("disabled").Inc()
		httpx.Error(w, r, httpx.New(http.StatusServiceUnavailable, "sandbox-disabled",
			"Демо временно выключено",
			"Демо сейчас недоступно. Попробуйте позже."))
		return
	}

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
	if err := validMode(req.Mode); err != nil {
		httpx.Error(w, r, err)
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
	res, err := h.limiter.Allow(ctx, "sandbox:ip:"+ip, h.limits.PerIP, time.Hour)
	if err == nil && !res.Allowed {
		metrics.SandboxCreated.WithLabelValues("rate_limited").Inc()
		httpx.Error(w, r, tooManyDemos())
		return
	}

	// Потолок одновременно живых демо (§7.5).
	active, err := h.svc.ActiveCount(ctx)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	if active >= h.limits.MaxActive {
		metrics.SandboxCreated.WithLabelValues("capacity").Inc()
		httpx.Error(w, r, httpx.New(http.StatusServiceUnavailable, "sandbox-busy",
			"Демо временно недоступно",
			"Сейчас открыто слишком много демо. Попробуйте через несколько минут."))
		return
	}

	created, login, err := h.create(r, req.Mode)
	if err != nil {
		metrics.SandboxCreated.WithLabelValues("error").Inc()
		httpx.Error(w, r, err)
		return
	}
	metrics.SandboxCreated.WithLabelValues("ok").Inc()

	// Сессия заводится сразу: посетитель попадает в кабинет одной кнопкой.
	if _, err := h.auth.StartSessionFor(ctx, w, created.OwnerID, created.TenantID, r.UserAgent()); err != nil {
		httpx.Error(w, r, err)
		return
	}

	httpx.JSON(w, http.StatusCreated, createResponse{
		TenantID:   created.TenantID.String(),
		ExpiresAt:  created.ExpiresAt.Format(time.RFC3339),
		RedirectTo: h.appPath(),
		Login:      login,
	})
}

// create заводит демо нужного режима.
func (h *Handler) create(r *http.Request, mode string) (Created, *loginDTO, error) {
	if mode == ModeGuided {
		g, err := h.svc.CreateGuided(r.Context())
		if err != nil {
			return Created{}, nil, err
		}
		return g.Created, &loginDTO{Email: g.Email, Password: g.Password}, nil
	}
	created, err := h.svc.Create(r.Context(), 0)
	return created, nil, err
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

type resetRequest struct {
	Mode string `json:"mode,omitempty"`
}

func (h *Handler) handleReset(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())
	if !principal.Tenant.IsSandbox {
		httpx.Error(w, r, toHTTP(ErrNotSandbox))
		return
	}

	var req resetRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(w, r, &req); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}
	if err := validMode(req.Mode); err != nil {
		httpx.Error(w, r, err)
		return
	}

	var (
		created Created
		login   *loginDTO
		err     error
	)
	if req.Mode == ModeGuided {
		var g CreatedGuided
		g, err = h.svc.ResetGuided(r.Context(), principal.Tenant.ID)
		created, login = g.Created, &loginDTO{Email: g.Email, Password: g.Password}
	} else {
		created, err = h.svc.Reset(r.Context(), principal.Tenant.ID, principal.Tenant.Seed)
	}
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
		Login:      login,
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

func (h *Handler) handleGuide(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	state, err := h.guide.State(r.Context(), principal.Tenant)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, state)
}

func (h *Handler) handleFill(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	result, err := h.guide.Fill(r.Context(), principal.Tenant, StepID(chi.URLParam(r, "step")))
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, result)
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

// toHTTP переводит ошибки симулятора и шагов в ответы RFC 9457.
func toHTTP(err error) error {
	var order *StepOrderError
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
	case errors.Is(err, ErrUnknownStep):
		return httpx.NotFound()
	case errors.As(err, &order):
		return httpx.Conflict("step-order", "Сначала предыдущий шаг",
			"Сначала — шаг «"+stepTitles[order.Need]+"»: без него этот не заполнить.")
	case errors.Is(err, ErrNotFillable):
		return httpx.Conflict("not-fillable", "Этот шаг — руками",
			"У этого шага нет тестовых данных: его делают кнопками в интерфейсе.")
	case errors.Is(err, ErrFillBusy):
		return httpx.Conflict("fill-busy", "Шаг уже заполняется",
			"Тестовые данные уже добавляются. Подождите пару секунд.")
	case errors.Is(err, ErrNoTemplateItems):
		return httpx.Conflict("no-template-items", "Нет позиций из шаблона",
			"Историю шаблон пишет только своим позициям. Заполните шаг «Позиции» тестовыми данными.")
	case errors.Is(err, ErrNothingToOrder):
		return httpx.Conflict("nothing-to-order", "Заказывать нечего",
			"Сейчас всего хватает, рекомендаций к заказу нет. Промотайте время: запас кончится.")
	case errors.Is(err, ErrNothingToReceive):
		return httpx.Conflict("nothing-to-receive", "Нет отправленных заказов",
			"Сначала отправьте заказ поставщику.")
	default:
		return err
	}
}
