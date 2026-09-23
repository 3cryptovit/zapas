package orders

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/httpx"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// IdempotencyHeader обязателен для создания и приёмки заказа (§11).
const IdempotencyHeader = "Idempotency-Key"

// Handler — HTTP-обвязка заказов.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes подключает маршруты.
//
// Заказы оформляет владелец; приёмка доступна всем — её делает тот,
// кто встречает поставку (§6).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/orders", h.handleList)
	r.Get("/orders/{id}", h.handleGet)
	r.Post("/orders/{id}/receive", h.handleReceive)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireOwner)

		r.Post("/orders", h.handleCreate)
		r.Patch("/orders/{id}", h.handleUpdate)
		r.Post("/orders/{id}/send", h.handleSend)
		r.Post("/orders/{id}/cancel", h.handleCancel)
	})
}

type lineRequest struct {
	ItemID uuid.UUID `json:"item_id"`
	Qty    qty.Qty   `json:"qty"`
}

type createRequest struct {
	SupplierID uuid.UUID     `json:"supplier_id"`
	Note       string        `json:"note,omitempty"`
	Lines      []lineRequest `json:"lines,omitempty"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	if r.Header.Get(IdempotencyHeader) == "" {
		httpx.Error(w, r, httpx.BadRequest("Обязателен заголовок "+IdempotencyHeader+"."))
		return
	}

	var req createRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if req.SupplierID == uuid.Nil {
		httpx.Error(w, r, httpx.Invalid(httpx.FieldError{
			Field: "supplier_id", Message: "Укажите поставщика.",
		}))
		return
	}

	in := CreateInput{
		SupplierID: req.SupplierID,
		Note:       req.Note,
		CreatedBy:  principal.UserID,
	}
	for _, l := range req.Lines {
		in.Lines = append(in.Lines, CreateLine(l))
	}

	order, err := h.svc.Create(r.Context(), principal.Tenant, in)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, order)
}

type updateRequest struct {
	Lines []lineRequest `json:"lines"`
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, ok := parseID(w, r)
	if !ok {
		return
	}

	var req updateRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	lines := make([]CreateLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, CreateLine(l))
	}

	order, err := h.svc.UpdateLines(r.Context(), principal.Tenant, id, lines)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, order)
}

func (h *Handler) handleSend(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, ok := parseID(w, r)
	if !ok {
		return
	}
	order, err := h.svc.Send(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	// В ответе — готовый текст заявки: его владелец копирует поставщику (FR-19).
	httpx.JSON(w, http.StatusOK, order)
}

func (h *Handler) handleCancel(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, ok := parseID(w, r)
	if !ok {
		return
	}
	order, err := h.svc.Cancel(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, order)
}

type receiveRequest struct {
	Lines []struct {
		ItemID      uuid.UUID `json:"item_id"`
		QtyReceived qty.Qty   `json:"qty_received"`
	} `json:"lines,omitempty"`
}

func (h *Handler) handleReceive(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, ok := parseID(w, r)
	if !ok {
		return
	}
	key := r.Header.Get(IdempotencyHeader)
	if key == "" {
		httpx.Error(w, r, httpx.BadRequest("Обязателен заголовок "+IdempotencyHeader+"."))
		return
	}

	var req receiveRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(w, r, &req); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	actual := make([]ReceiveLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		actual = append(actual, ReceiveLine{ItemID: l.ItemID, Qty: l.QtyReceived})
	}

	order, err := h.svc.Receive(r.Context(), principal.Tenant, id, actual, principal.UserID, key)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, order)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, ok := parseID(w, r)
	if !ok {
		return
	}
	order, err := h.svc.Get(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, order)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var status *Status
	if v := r.URL.Query().Get("status"); v != "" {
		s := Status(v)
		switch s {
		case StatusDraft, StatusSent, StatusReceived, StatusCancelled:
			status = &s
		default:
			httpx.Error(w, r, httpx.BadRequest("Неизвестный статус заказа."))
			return
		}
	}

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}

	list, err := h.svc.List(r.Context(), principal.Tenant, status, limit)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	if list == nil {
		list = []Order{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return uuid.Nil, false
	}
	return id, true
}

// toHTTP переводит доменные ошибки заказов в ответы RFC 9457.
func toHTTP(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound()
	case errors.Is(err, ErrEmptyOrder):
		return httpx.Conflict("empty-order", "Пустой заказ",
			"В заказе нет ни одной строки с количеством больше нуля.")
	case errors.Is(err, ErrAlreadySent):
		return httpx.Conflict("already-sent", "Заказ уже отправлен",
			"Отправить заказ второй раз нельзя.")
	case errors.Is(err, ErrAlreadyReceived):
		return httpx.Conflict("already-received", "Заказ уже принят",
			"Повторная приёмка не создаёт второй приход.")
	case errors.Is(err, ErrWrongStatus):
		return httpx.Conflict("wrong-status", "Недоступно в текущем статусе",
			"Проверьте статус заказа и обновите страницу.")
	default:
		return err
	}
}
