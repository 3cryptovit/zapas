// Package logging настраивает структурный лог (slog, JSON) и переносит
// request_id и tenant_id из контекста в каждую запись (§9.1).
//
// Пароли, токены и тела запросов не логируются (§12.2).
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyTenantID
	ctxKeyUserID
)

// New собирает JSON-логгер, который сам дописывает поля из контекста.
func New(level string, out *os.File) *slog.Logger {
	if out == nil {
		out = os.Stdout
	}
	h := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(&contextHandler{Handler: h})
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler дописывает поля из контекста, чтобы каждый вызов логгера
// не повторял slog.String("request_id", ...) руками.
type contextHandler struct{ slog.Handler }

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok && v != "" {
		r.AddAttrs(slog.String("request_id", v))
	}
	if v, ok := ctx.Value(ctxKeyTenantID).(string); ok && v != "" {
		r.AddAttrs(slog.String("tenant_id", v))
	}
	if v, ok := ctx.Value(ctxKeyUserID).(string); ok && v != "" {
		r.AddAttrs(slog.String("user_id", v))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}

// WithRequestID кладёт идентификатор запроса в контекст.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// WithTenantID кладёт идентификатор тенанта в контекст (только для логов;
// сам доступ к данным идёт через tenant.Context).
func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyTenantID, id)
}

// WithUserID кладёт идентификатор пользователя в контекст.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

// RequestID достаёт идентификатор запроса.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}
