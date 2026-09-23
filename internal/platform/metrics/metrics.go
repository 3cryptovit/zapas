// Package metrics собирает счётчики Prometheus (§13.4).
//
// Метки подобраны так, чтобы не взорвать кардинальность: путь берётся из
// шаблона роутера (/items/{id}), а не из фактического URL.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zapas_http_requests_total",
		Help: "Запросы к API по методу, маршруту и коду ответа.",
	}, []string{"method", "route", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zapas_http_request_duration_seconds",
		Help:    "Длительность обработки запроса.",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.15, 0.3, 0.5, 1, 2.5, 5},
	}, []string{"method", "route"})

	// TaskDuration — длительность фоновых задач: ночной прогноз, сводка, очистка.
	TaskDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zapas_task_duration_seconds",
		Help:    "Длительность фоновой задачи.",
		Buckets: []float64{0.1, 0.5, 1, 5, 15, 60, 300, 900},
	}, []string{"task", "result"})

	// OutboxPending — сколько уведомлений ждёт отправки; алерт при >100 дольше 10 минут.
	OutboxPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zapas_outbox_pending",
		Help: "Уведомления в очереди на отправку.",
	})

	// OutboxSent — отправленные и провалившиеся уведомления по каналам.
	OutboxSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zapas_outbox_delivered_total",
		Help: "Результат отправки уведомлений.",
	}, []string{"channel", "result"})

	// SandboxActive — активные песочницы; алерт при слишком быстром росте.
	SandboxActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zapas_sandbox_active",
		Help: "Живые демо-тенанты.",
	})

	// SandboxCreated — созданные песочницы: продуктовая метрика лендинга.
	SandboxCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zapas_sandbox_created_total",
		Help: "Созданные демо-тенанты.",
	}, []string{"result"})

	// SandboxAdvance — промотки виртуального времени.
	SandboxAdvance = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "zapas_sandbox_advance_seconds",
		Help:    "Длительность промотки времени в песочнице.",
		Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 30},
	})

	// BalanceDrift — расхождения остатка с журналом, найденные ночной сверкой (FR-17).
	BalanceDrift = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zapas_balance_drift_total",
		Help: "Позиции, где остаток разошёлся с суммой движений.",
	})
)

// ObserveTask — обёртка для фоновой задачи: пишет длительность и результат.
func ObserveTask(name string, start time.Time, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	TaskDuration.WithLabelValues(name, result).Observe(time.Since(start).Seconds())
}

// Middleware считает запросы и латентность по шаблону маршрута chi.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		route := "unmatched"
		if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
			route = rctx.RoutePattern()
		}
		httpRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *recorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}
