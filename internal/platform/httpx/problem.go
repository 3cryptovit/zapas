// Package httpx — общий транспортный слой: ошибки RFC 9457, чтение и запись
// JSON, курсорная пагинация. Бизнес-логика сюда не попадает.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ContentTypeProblem — тип ответа об ошибке по RFC 9457.
const ContentTypeProblem = "application/problem+json"

// Problem — тело ошибки. Расширения (например, on_hand) попадают в JSON
// на верхний уровень, как требует RFC 9457.
type Problem struct {
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Status     int            `json:"status"`
	Detail     string         `json:"detail,omitempty"`
	Instance   string         `json:"instance,omitempty"`
	Errors     []FieldError   `json:"errors,omitempty"`
	Extensions map[string]any `json:"-"`
}

// FieldError — ошибка конкретного поля формы.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (p *Problem) Error() string {
	return fmt.Sprintf("%d %s: %s", p.Status, p.Title, p.Detail)
}

// With добавляет расширение к ответу об ошибке.
func (p *Problem) With(key string, value any) *Problem {
	if p.Extensions == nil {
		p.Extensions = map[string]any{}
	}
	p.Extensions[key] = value
	return p
}

// MarshalJSON разворачивает расширения на верхний уровень объекта.
func (p Problem) MarshalJSON() ([]byte, error) {
	type alias Problem // без собственного MarshalJSON, иначе рекурсия
	base, err := json.Marshal(alias(p))
	if err != nil {
		return nil, err
	}
	if len(p.Extensions) == 0 {
		return base, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range p.Extensions {
		// Расширение не имеет права затереть обязательные поля RFC 9457.
		switch k {
		case "type", "title", "status", "detail", "instance", "errors":
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		merged[k] = raw
	}
	return json.Marshal(merged)
}

// New собирает ошибку. Тип — ссылка вида /errors/<slug>, она же ключ в docs/api.
func New(status int, slug, title, detail string) *Problem {
	return &Problem{
		Type:   "/errors/" + slug,
		Title:  title,
		Status: status,
		Detail: detail,
	}
}

// Готовые ошибки для типовых случаев.

func BadRequest(detail string) *Problem {
	return New(http.StatusBadRequest, "bad-request", "Некорректный запрос", detail)
}

// Invalid — ошибка валидации с разбивкой по полям.
func Invalid(errs ...FieldError) *Problem {
	p := New(http.StatusUnprocessableEntity, "validation-failed", "Проверьте заполненные поля", "")
	p.Errors = errs
	return p
}

func Unauthorized() *Problem {
	return New(http.StatusUnauthorized, "unauthorized", "Нужно войти", "Сессия не найдена или истекла.")
}

func Forbidden(detail string) *Problem {
	return New(http.StatusForbidden, "forbidden", "Недостаточно прав", detail)
}

// NotFound отдаётся и на чужой ресурс тоже: существование объекта другого
// тенанта не раскрывается (§11).
func NotFound() *Problem {
	return New(http.StatusNotFound, "not-found", "Не найдено", "Объект не найден.")
}

func Conflict(slug, title, detail string) *Problem {
	return New(http.StatusConflict, slug, title, detail)
}

func TooManyRequests(detail string) *Problem {
	return New(http.StatusTooManyRequests, "rate-limited", "Слишком много запросов", detail)
}

func PayloadTooLarge(detail string) *Problem {
	return New(http.StatusRequestEntityTooLarge, "payload-too-large", "Запрос слишком большой", detail)
}

func Internal() *Problem {
	return New(http.StatusInternalServerError, "internal", "Внутренняя ошибка",
		"Что-то пошло не так. Мы уже знаем и разбираемся.")
}

// AsProblem достаёт *Problem из цепочки ошибок; для всего остального отдаёт 500,
// чтобы наружу не утекли детали внутренней ошибки.
func AsProblem(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return Internal()
}
