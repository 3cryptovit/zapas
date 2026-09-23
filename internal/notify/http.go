package notify

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/httpx"
)

// Handler — HTTP-обвязка ленты.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes подключает маршруты ленты. Она доступна всем: сотруднику тоже
// нужно видеть, что происходит со складом (§6).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/notifications", h.handleList)
	r.Post("/notifications/read", h.handleMarkRead)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}

	feed, err := h.svc.List(r.Context(), principal.Tenant, principal.UserID,
		r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, ErrBadCursor) {
			httpx.Error(w, r, httpx.BadRequest("Некорректный курсор пагинации."))
			return
		}
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, feed)
}

type markReadRequest struct {
	// IDs пустой или отсутствует — отметить всё прочитанным.
	IDs []uuid.UUID `json:"ids,omitempty"`
}

func (h *Handler) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var req markReadRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(w, r, &req); err != nil {
			httpx.Error(w, r, err)
			return
		}
	}

	affected, err := h.svc.MarkRead(r.Context(), principal.Tenant, principal.UserID, req.IDs)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"marked": affected})
}
