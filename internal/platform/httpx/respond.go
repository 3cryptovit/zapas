package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// MaxBodyBytes — потолок тела запроса (§12.2). Для импорта CSV лимит свой.
const MaxBodyBytes = 1 << 20 // 1 МБ

// JSON пишет успешный ответ.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Заголовки уже ушли — остаётся только записать в лог.
		slog.Error("не удалось записать ответ", slog.String("err", err.Error()))
	}
}

// NoContent отвечает 204.
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Error приводит любую ошибку к RFC 9457 и пишет её в ответ.
// Детали ошибок 5xx наружу не уходят, только в лог.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	p := AsProblem(err)
	if p.Instance == "" {
		p.Instance = r.URL.Path
	}
	if p.Status >= 500 {
		slog.ErrorContext(r.Context(), "ошибка запроса",
			slog.String("err", err.Error()),
			slog.String("path", r.URL.Path),
			slog.String("method", r.Method),
		)
	}
	w.Header().Set("Content-Type", ContentTypeProblem)
	w.WriteHeader(p.Status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.ErrorContext(r.Context(), "не удалось записать ошибку", slog.String("err", err.Error()))
	}
}

// Decode читает тело запроса в dst с ограничением размера и без неизвестных
// полей: опечатка в имени поля должна быть видна сразу, а не молча проигнорирована.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return New(http.StatusUnsupportedMediaType, "unsupported-media-type",
			"Неподдерживаемый формат", "Ожидается application/json.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	// В теле должен быть ровно один JSON-объект.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("В теле запроса должен быть один JSON-объект.")
	}
	return nil
}

func decodeError(err error) error {
	var maxErr *http.MaxBytesError
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError

	switch {
	case errors.As(err, &maxErr):
		return PayloadTooLarge(fmt.Sprintf("Тело запроса больше %d байт.", maxErr.Limit))
	case errors.As(err, &syntaxErr):
		return BadRequest(fmt.Sprintf("Некорректный JSON на позиции %d.", syntaxErr.Offset))
	case errors.As(err, &typeErr):
		return Invalid(FieldError{
			Field:   typeErr.Field,
			Message: fmt.Sprintf("Ожидается %s.", typeErr.Type.String()),
		})
	case errors.Is(err, io.EOF):
		return BadRequest("Пустое тело запроса.")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return Invalid(FieldError{Field: field, Message: "Неизвестное поле."})
	default:
		return BadRequest("Не удалось разобрать тело запроса.")
	}
}

// Page — ответ с курсорной пагинацией (§11).
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// NewPage упаковывает страницу, гарантируя [] вместо null в JSON.
func NewPage[T any](items []T, next string) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, NextCursor: next}
}
