package stock

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/httpx"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// IdempotencyHeader обязателен для движений (§11).
const IdempotencyHeader = "Idempotency-Key"

// Handler — HTTP-обвязка склада.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes подключает маршруты склада. Все они уже за middleware аутентификации.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/movements", h.handleCreate)
	r.Post("/movements/{id}/reverse", h.handleReverse)
	r.Get("/movements", h.handleList)

	r.Post("/counts", h.handleCreateCount)
	r.Get("/counts", h.handleListCounts)
	r.Get("/counts/{id}", h.handleGetCount)
	r.Put("/counts/{id}/lines", h.handleSetCountLines)
	r.Post("/counts/{id}/post", h.handlePostCount)
}

type createMovementRequest struct {
	Type       Type       `json:"type"`
	ItemID     uuid.UUID  `json:"item_id"`
	Qty        qty.Qty    `json:"qty"`
	Reason     string     `json:"reason,omitempty"`
	Comment    string     `json:"comment,omitempty"`
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
	OrderID    *uuid.UUID `json:"order_id,omitempty"`
}

// createMovementResponse содержит новый остаток и статус, чтобы интерфейс
// обновился без второго запроса (§11).
type createMovementResponse struct {
	Movement Movement       `json:"movement"`
	Balance  Balance        `json:"balance"`
	Status   StatusSnapshot `json:"status"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	key := r.Header.Get(IdempotencyHeader)
	if key == "" {
		httpx.Error(w, r, httpx.BadRequest("Обязателен заголовок "+IdempotencyHeader+"."))
		return
	}

	var req createMovementRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	if fields := validateCreate(req); len(fields) > 0 {
		httpx.Error(w, r, httpx.Invalid(fields...))
		return
	}

	in := RecordRequest{
		ItemID:         req.ItemID,
		Type:           req.Type,
		Qty:            req.Qty,
		Reason:         req.Reason,
		Comment:        req.Comment,
		OrderID:        req.OrderID,
		IdempotencyKey: key,
		CreatedBy:      &principal.UserID,
	}
	if req.OccurredAt != nil {
		in.OccurredAt = *req.OccurredAt
	}

	res, err := h.svc.Record(r.Context(), principal.Tenant, in)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}

	// Повтор с тем же ключом отдаёт 200, а не 201: ничего нового не создано.
	status := http.StatusCreated
	if res.Duplicate {
		status = http.StatusOK
	}
	httpx.JSON(w, status, createMovementResponse{
		Movement: res.Movement,
		Balance:  res.Balance,
		Status:   res.Status,
	})
}

func validateCreate(req createMovementRequest) []httpx.FieldError {
	var fields []httpx.FieldError

	if req.ItemID == uuid.Nil {
		fields = append(fields, httpx.FieldError{Field: "item_id", Message: "Укажите позицию."})
	}
	if !req.Type.UserCreatable() {
		fields = append(fields, httpx.FieldError{
			Field:   "type",
			Message: "Допустимы receipt, usage и writeoff. Корректировка создаётся пересчётом, сторно — отдельным действием.",
		})
	}
	if !req.Qty.IsPositive() {
		fields = append(fields, httpx.FieldError{
			Field:   "qty",
			Message: "Количество должно быть больше нуля: знак ставит сервер по типу движения.",
		})
	}
	if req.Type == TypeWriteoff && !ValidReason(req.Reason) {
		fields = append(fields, httpx.FieldError{Field: "reason", Message: "Выберите причину списания."})
	}
	return fields
}

func (h *Handler) handleReverse(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	if r.Header.Get(IdempotencyHeader) == "" {
		httpx.Error(w, r, httpx.BadRequest("Обязателен заголовок "+IdempotencyHeader+"."))
		return
	}

	res, err := h.svc.Reverse(r.Context(), principal.Tenant, id,
		principal.UserID, principal.Role.CanReverseOthers())
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, createMovementResponse{
		Movement: res.Movement,
		Balance:  res.Balance,
		Status:   res.Status,
	})
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())
	q := r.URL.Query()

	filter := ListFilter{Cursor: q.Get("cursor")}

	if v := q.Get("item_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.Error(w, r, httpx.BadRequest("Некорректный item_id."))
			return
		}
		filter.ItemID = &id
	}
	if v := q.Get("author_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.Error(w, r, httpx.BadRequest("Некорректный author_id."))
			return
		}
		filter.AuthorID = &id
	}
	if v := q.Get("type"); v != "" {
		t := Type(v)
		if !t.Valid() {
			httpx.Error(w, r, httpx.BadRequest("Неизвестный тип движения."))
			return
		}
		filter.Type = &t
	}
	if v := q.Get("from"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.Error(w, r, httpx.BadRequest("Параметр from должен быть датой в формате ISO 8601."))
			return
		}
		filter.From = &parsed
	}
	if v := q.Get("to"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.Error(w, r, httpx.BadRequest("Параметр to должен быть датой в формате ISO 8601."))
			return
		}
		filter.To = &parsed
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			httpx.Error(w, r, httpx.BadRequest("Параметр limit должен быть положительным числом."))
			return
		}
		filter.Limit = n
	}

	page, err := h.svc.ListMovements(r.Context(), principal.Tenant, filter)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, httpx.NewPage(page.Items, page.NextCursor))
}

// --- инвентаризация ---

type createCountRequest struct {
	Scope   CountScope  `json:"scope"`
	Note    string      `json:"note,omitempty"`
	ItemIDs []uuid.UUID `json:"item_ids,omitempty"`
}

func (h *Handler) handleCreateCount(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var req createCountRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.Scope == "" {
		req.Scope = ScopeAll
	}

	count, err := h.svc.CreateCount(r.Context(), principal.Tenant, req.Scope, req.Note, req.ItemIDs, principal.UserID)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, count)
}

func (h *Handler) handleListCounts(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	counts, err := h.svc.ListCounts(r.Context(), principal.Tenant, limit)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": counts})
}

func (h *Handler) handleGetCount(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	count, err := h.svc.GetCount(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, count)
}

type setCountLinesRequest struct {
	Lines []struct {
		ItemID     uuid.UUID `json:"item_id"`
		CountedQty qty.Qty   `json:"counted_qty"`
	} `json:"lines"`
}

func (h *Handler) handleSetCountLines(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}

	var req setCountLinesRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	lines := make(map[uuid.UUID]qty.Qty, len(req.Lines))
	for _, l := range req.Lines {
		if l.CountedQty.IsNegative() {
			httpx.Error(w, r, httpx.Invalid(httpx.FieldError{
				Field: "counted_qty", Message: "Количество не может быть отрицательным.",
			}))
			return
		}
		lines[l.ItemID] = l.CountedQty
	}

	if err := h.svc.SetCountLines(r.Context(), principal.Tenant, id, lines); err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}

	count, err := h.svc.GetCount(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, count)
}

type postCountRequest struct {
	// Force подтверждает проведение, когда учёт изменился с момента ввода (FR-13).
	Force bool `json:"force,omitempty"`
}

func (h *Handler) handlePostCount(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	if r.Header.Get(IdempotencyHeader) == "" {
		httpx.Error(w, r, httpx.BadRequest("Обязателен заголовок "+IdempotencyHeader+"."))
		return
	}

	var req postCountRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(w, r, &req); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	count, err := h.svc.PostCount(r.Context(), principal.Tenant, id, principal.UserID, req.Force)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, count)
}

// toHTTP переводит доменные ошибки склада в ответы RFC 9457.
func toHTTP(err error) error {
	var insufficient *InsufficientStockError
	if errors.As(err, &insufficient) {
		// 409 с текущим остатком: интерфейс показывает его в сообщении (FR-8).
		return httpx.Conflict("insufficient-stock", "Недостаточно остатка",
			"На складе "+insufficient.OnHand.String()+", списать "+
				insufficient.Requested.String()+" нельзя. Сделайте пересчёт.").
			With("on_hand", insufficient.OnHand)
	}

	var changed *CountChangedError
	if errors.As(err, &changed) {
		return httpx.Conflict("count-changed", "Учёт изменился",
			"С момента ввода по части позиций прошли движения. Проверьте расхождения и подтвердите проведение.").
			With("items", changed.Items)
	}

	switch {
	case errors.Is(err, ErrItemNotFound):
		return httpx.NotFound()
	case errors.Is(err, ErrItemArchived):
		return httpx.Conflict("item-archived", "Позиция в архиве",
			"По архивной позиции движения не принимаются. Верните её из архива.")
	case errors.Is(err, ErrAlreadyReversed):
		return httpx.Conflict("already-reversed", "Уже сторнировано",
			"Это движение уже сторнировано. Сторно возможно один раз.")
	case errors.Is(err, ErrCannotReverse):
		return httpx.Forbidden("Это движение нельзя сторнировать: сотрудник отменяет только свои записи.")
	case errors.Is(err, ErrCountPosted):
		return httpx.Conflict("count-posted", "Пересчёт уже проведён",
			"Документ проведён, изменить его нельзя.")
	case errors.Is(err, ErrOccurredTooOld):
		return httpx.Invalid(httpx.FieldError{
			Field:   "occurred_at",
			Message: "Время события дальше 7 дней назад. Исправьте остаток пересчётом.",
		})
	case errors.Is(err, ErrOccurredInFuture):
		return httpx.Invalid(httpx.FieldError{
			Field: "occurred_at", Message: "Время события в будущем.",
		})
	case errors.Is(err, ErrBadCursor):
		return httpx.BadRequest("Некорректный курсор пагинации.")
	case errors.Is(err, ErrDuplicateKey):
		return httpx.Conflict("duplicate-request", "Повторный запрос",
			"Запрос с таким ключом идемпотентности уже обрабатывается.")
	default:
		return err
	}
}
