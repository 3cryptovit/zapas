package dashboard

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/platform/httpx"
)

// Handler — HTTP-обвязка дашборда.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes подключает маршруты чтения. Дашборд и карточка доступны всем;
// блок рекомендаций сотрудник видит без кнопок заказа (§6).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/dashboard", h.handleDashboard)
	r.Get("/items/{id}/insights", h.handleInsights)
	r.With(auth.RequireOwner).Get("/suggestions", h.handleSuggestions)
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	view, err := h.svc.Load(r.Context(), principal.Tenant)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, view)
}

func (h *Handler) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	view, err := h.svc.Load(r.Context(), principal.Tenant)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"today": view.Today,
		"items": view.Suggestions,
	})
}

func (h *Handler) handleInsights(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}

	insights, err := h.svc.LoadInsights(r.Context(), principal.Tenant, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Error(w, r, httpx.NotFound())
			return
		}
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, insights)
}
