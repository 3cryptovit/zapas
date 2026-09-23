package httpx

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/logging"
)

// RequestID кладёт идентификатор запроса в контекст и в заголовок ответа,
// чтобы строку лога можно было связать с конкретным обращением.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
	})
}

// RealIP подставляет адрес клиента из заголовка X-Real-IP, но только
// если запрос пришёл от доверенного прокси на петле.
//
// Штатный middleware.RealIP из chi этой проверки не делает: он берёт
// X-Forwarded-For у кого угодно. А от адреса клиента зависят оба
// ограничителя — пять демо в час и защита от перебора пароля, — то есть
// подделка заголовка снимала бы их полностью.
//
// Приложение слушает только 127.0.0.1, снаружи к нему можно попасть
// лишь через nginx, а он заполняет X-Real-IP настоящим адресом
// соединения. Поэтому заголовку верим ровно от петли и больше ниоткуда.
func RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fromLoopback(r.RemoteAddr) {
			if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
				r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
			}
		}
		next.ServeHTTP(w, r)
	})
}

func fromLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Recover превращает панику в 500 и пишет стек в лог, не роняя процесс.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler — штатный способ оборвать ответ, не ошибка.
					if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(rec)
					}
					log.ErrorContext(r.Context(), "паника в обработчике",
						slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())),
						slog.String("path", r.URL.Path),
					)
					Error(w, r, Internal())
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog пишет одну строку на запрос. Тела и заголовки авторизации не логируются.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			log.InfoContext(r.Context(), "запрос",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.bytes),
				slog.Duration("took", time.Since(start)),
			)
		})
	}
}

// SecurityHeaders — заголовки из §12.2. CSP выставляется на Nginx для статики,
// здесь — минимум, который должен быть и на ответах API.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Status отдаёт записанный код ответа — нужен метрикам.
func (r *statusRecorder) Status() int { return r.status }
