// Package api собирает HTTP-приложение: пулы БД, Redis, модули и роутер.
//
// Здесь только проводка. Бизнес-логика живёт в модулях internal/*, и о HTTP
// она не знает (CONVENTIONS.md, «Слои»).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/catalog"
	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/dashboard"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/orders"
	"github.com/vostapenko/zapas/internal/platform/config"
	"github.com/vostapenko/zapas/internal/platform/httpx"
	"github.com/vostapenko/zapas/internal/platform/metrics"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/ratelimit"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/sandbox"
	"github.com/vostapenko/zapas/internal/stock"
)

// App — собранное приложение со всеми зависимостями.
type App struct {
	cfg config.Config
	log *slog.Logger
	// db работает под ролью приложения: RLS действует.
	db *postgres.DB
	// maint — обслуживающая роль: вход, разбор сессии, ночные задачи (ADR-005).
	maint *postgres.DB
	rdb   *redis.Client
	mux   http.Handler

	auth      *auth.Handler
	catalog   *catalog.Handler
	stock     *stock.Handler
	orders    *orders.Handler
	dashboard *dashboard.Handler
	notify    *notify.Handler
	telegram  *notify.TelegramHandler
	sandbox   *sandbox.Handler
}

// Deps — готовые зависимости. Отдельный конструктор нужен тестам: они
// подставляют свои пулы и застывшие часы, не поднимая конфиг целиком.
type Deps struct {
	Config config.Config
	Log    *slog.Logger
	DB     *postgres.DB
	Maint  *postgres.DB
	Redis  *redis.Client
	Clock  clock.Clock
}

// Build поднимает зависимости из конфига и собирает роутер.
func Build(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	db, err := postgres.Connect(ctx, cfg.DB.URL, cfg.DB.MaxConns)
	if err != nil {
		return nil, err
	}
	maint, err := postgres.Connect(ctx, cfg.DB.MaintURL, 4)
	if err != nil {
		db.Close()
		return nil, err
	}

	app := New(Deps{
		Config: cfg,
		Log:    log,
		DB:     db,
		Maint:  maint,
		Redis:  redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr}),
		Clock:  clock.System{},
	})
	return app, nil
}

// New собирает приложение из готовых зависимостей.
func New(d Deps) *App {
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}

	limiter := ratelimit.New(d.Redis)

	authSvc := auth.NewService(d.DB, d.Maint, d.Clock, limiter, d.Config.Session.TTL)
	catalogSvc := catalog.NewService(d.DB, d.Clock)
	// Уведомления пишутся в ту же транзакцию, что и событие (ADR-004),
	// поэтому сервис передаётся и в пополнение, и в заказы.
	notifySvc := notify.NewService(d.DB, d.Maint, d.Clock, d.Config.App.BaseURL)
	// Статус позиции пересчитывается в той же транзакции, что и движение (§5.3).
	replenishmentSvc := replenishment.NewService(d.DB, d.Clock, notifySvc)
	stockSvc := stock.NewService(d.DB, d.Clock, replenishmentSvc)
	ordersSvc := orders.NewService(d.DB, d.Clock, replenishmentSvc, stockSvc, notifySvc)
	dashboardSvc := dashboard.NewService(d.DB, d.Clock)

	// Песочница пользуется теми же сервисами, что и обычный тенант:
	// демо должно вести себя ровно как рабочий кабинет (§7).
	pipelineSvc := pipeline.NewService(d.DB, d.Clock, replenishmentSvc, d.Log)
	sandboxSvc := sandbox.NewService(d.DB, d.Maint, d.Clock, pipelineSvc,
		d.Config.Sandbox.TTL, d.Config.App.BaseURL, d.Log)
	simulator := sandbox.NewSimulator(sandboxSvc, ordersSvc, stockSvc, notifySvc)

	// Песочнице нужен тот же обработчик входа: демо заводит сессию
	// сразу после создания (§7.1).
	authHandler := auth.NewHandler(authSvc, d.Config.Session.SecureCookies, d.Config.App.BaseURL)

	app := &App{
		cfg:       d.Config,
		log:       d.Log,
		db:        d.DB,
		maint:     d.Maint,
		rdb:       d.Redis,
		auth:      authHandler,
		catalog:   catalog.NewHandler(catalogSvc),
		stock:     stock.NewHandler(stockSvc),
		orders:    orders.NewHandler(ordersSvc),
		dashboard: dashboard.NewHandler(dashboardSvc),
		notify:    notify.NewHandler(notifySvc),
		telegram: notify.NewTelegramHandler(
			notifySvc,
			notify.NewTelegramSender(d.Config.Notify.TelegramBotToken),
			d.Config.Notify.TelegramWebhookSecret,
			d.Config.Notify.TelegramBotUsername,
			d.Log,
		),
		sandbox: sandbox.NewHandler(sandboxSvc, simulator, authHandler, limiter,
			d.Config.Sandbox.MaxActive, d.Config.Sandbox.PerIPHourly),
	}
	app.mux = app.routes()
	return app
}

// Handler отдаёт готовый http.Handler.
func (a *App) Handler() http.Handler { return a.mux }

// Close освобождает пулы.
func (a *App) Close() {
	if a.rdb != nil {
		_ = a.rdb.Close()
	}
	if a.maint != nil {
		a.maint.Close()
	}
	if a.db != nil {
		a.db.Close()
	}
}

func (a *App) routes() http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestID)
	r.Use(httpx.RealIP)
	r.Use(httpx.Recover(a.log))
	r.Use(httpx.SecurityHeaders)
	r.Use(metrics.Middleware)
	r.Use(httpx.AccessLog(a.log))
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", a.handleHealthz)
	r.Get("/readyz", a.handleReadyz)

	r.Route("/api/v1", func(r chi.Router) {
		// Публичное: вход и создание демо.
		a.auth.Routes(r)
		a.sandbox.PublicRoutes(r)
		// Вебхук зовёт Telegram, а не браузер: сессии здесь нет,
		// подлинность проверяется секретом в заголовке (§12.2).
		a.telegram.PublicRoutes(r)

		// Всё остальное — только с действующей сессией. Для изменяющих
		// запросов middleware дополнительно проверяет CSRF-токен.
		r.Group(func(r chi.Router) {
			r.Use(a.auth.Require)

			a.auth.ProtectedRoutes(r)
			a.dashboard.Routes(r)
			a.catalog.Routes(r)
			a.stock.Routes(r)
			a.orders.Routes(r)
			a.notify.Routes(r)
			a.sandbox.ProtectedRoutes(r)
			a.telegram.Routes(r)
		})

		r.NotFound(func(w http.ResponseWriter, req *http.Request) {
			httpx.Error(w, req, httpx.NotFound())
		})
	})

	return r
}

// handleHealthz — живость процесса, без обращений к зависимостям.
func (a *App) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz — готовность принимать трафик: проверяются БД и Redis (§11).
func (a *App) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	ready := true

	if err := a.db.Ping(ctx); err != nil {
		checks["postgres"] = "fail"
		ready = false
	}
	if a.rdb != nil {
		if err := a.rdb.Ping(ctx).Err(); err != nil {
			checks["redis"] = "fail"
			ready = false
		}
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
		a.log.WarnContext(ctx, "readyz: зависимость недоступна", slog.Any("checks", checks))
	}
	httpx.JSON(w, status, map[string]any{"ready": ready, "checks": checks})
}

// MetricsHandler отдаёт /metrics на отдельном порту, закрытом снаружи.
func MetricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}
