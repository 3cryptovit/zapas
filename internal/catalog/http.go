package catalog

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/auth"
	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/httpx"
	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Handler — HTTP-обвязка каталога.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes подключает маршруты. Чтение доступно всем, запись — владельцу (§11).
func (h *Handler) Routes(r chi.Router) {
	r.Get("/items", h.handleListItems)
	r.Get("/items/{id}", h.handleGetItem)
	r.Get("/categories", h.handleListCategories)
	r.Get("/suppliers", h.handleListSuppliers)
	r.Get("/suppliers/{id}", h.handleGetSupplier)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireOwner)

		r.Post("/items", h.handleCreateItem)
		r.Patch("/items/{id}", h.handleUpdateItem)
		r.Post("/items/{id}/archive", h.handleArchiveItem)
		r.Post("/categories", h.handleCreateCategory)
		r.Post("/suppliers", h.handleCreateSupplier)
		r.Put("/suppliers/{supplierId}/items/{itemId}", h.handleSetTerms)
		r.Post("/imports/items", h.handleImport)
	})
}

func (h *Handler) handleListItems(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	items, err := h.svc.ListItems(r.Context(), principal.Tenant,
		r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleGetItem(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	item, err := h.svc.GetItem(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

type createItemRequest struct {
	Name         string     `json:"name"`
	BaseUnit     BaseUnit   `json:"base_unit"`
	CategoryID   *uuid.UUID `json:"category_id,omitempty"`
	SupplierID   *uuid.UUID `json:"default_supplier_id,omitempty"`
	ServiceLevel int        `json:"service_level,omitempty"`
	ManualMinQty qty.Qty    `json:"manual_min_qty,omitempty"`
}

func (h *Handler) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var req createItemRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	var fields []httpx.FieldError
	if req.Name == "" {
		fields = append(fields, httpx.FieldError{Field: "name", Message: "Укажите название."})
	}
	if !req.BaseUnit.Valid() {
		fields = append(fields, httpx.FieldError{
			Field: "base_unit", Message: "Допустимы kg, l и pcs.",
		})
	}
	if req.ServiceLevel != 0 && !ValidServiceLevel(req.ServiceLevel) {
		fields = append(fields, httpx.FieldError{
			Field: "service_level", Message: "Допустимы 90, 95 и 99.",
		})
	}
	if len(fields) > 0 {
		httpx.Error(w, r, httpx.Invalid(fields...))
		return
	}

	item, err := h.svc.CreateItem(r.Context(), principal.Tenant, CreateItemInput(req))
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, item)
}

type updateItemRequest struct {
	Name         *string    `json:"name,omitempty"`
	CategoryID   *uuid.UUID `json:"category_id,omitempty"`
	SupplierID   *uuid.UUID `json:"default_supplier_id,omitempty"`
	ServiceLevel *int       `json:"service_level,omitempty"`
	ManualMinQty *qty.Qty   `json:"manual_min_qty,omitempty"`
}

func (h *Handler) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}

	var req updateItemRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	item, err := h.svc.UpdateItem(r.Context(), principal.Tenant, id, UpdateItemInput(req))
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (h *Handler) handleArchiveItem(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	if err := h.svc.ArchiveItem(r.Context(), principal.Tenant, id); err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) handleListCategories(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	categories, err := h.svc.ListCategories(r.Context(), principal.Tenant)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": categories})
}

type createCategoryRequest struct {
	Name string `json:"name"`
}

func (h *Handler) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var req createCategoryRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	category, err := h.svc.CreateCategory(r.Context(), principal.Tenant, req.Name)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, category)
}

func (h *Handler) handleListSuppliers(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	suppliers, err := h.svc.ListSuppliers(r.Context(), principal.Tenant,
		r.URL.Query().Get("include_archived") == "true")
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": suppliers})
}

func (h *Handler) handleGetSupplier(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	supplier, err := h.svc.GetSupplier(r.Context(), principal.Tenant, id)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, supplier)
}

type createSupplierRequest struct {
	Name             string `json:"name"`
	Contact          string `json:"contact,omitempty"`
	LeadTimeDays     int    `json:"lead_time_days"`
	DeliveryWeekdays []int  `json:"delivery_weekdays"`
	// OrderCutoff — время отсечки в формате "16:00".
	OrderCutoff string `json:"order_cutoff"`
}

func (h *Handler) handleCreateSupplier(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	var req createSupplierRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}

	var fields []httpx.FieldError
	if req.Name == "" {
		fields = append(fields, httpx.FieldError{Field: "name", Message: "Укажите название."})
	}
	if !ValidWeekdays(req.DeliveryWeekdays) {
		fields = append(fields, httpx.FieldError{
			Field:   "delivery_weekdays",
			Message: "Укажите дни доставки числами от 1 (пн) до 7 (вс), без повторов.",
		})
	}
	cutoff := clock.TimeOfDay{Hour: 16}
	if req.OrderCutoff != "" {
		parsed, err := clock.ParseTimeOfDay(req.OrderCutoff)
		if err != nil {
			fields = append(fields, httpx.FieldError{
				Field: "order_cutoff", Message: "Время отсечки в формате 16:00.",
			})
		} else {
			cutoff = parsed
		}
	}
	if len(fields) > 0 {
		httpx.Error(w, r, httpx.Invalid(fields...))
		return
	}

	supplier, err := h.svc.CreateSupplier(r.Context(), principal.Tenant, CreateSupplierInput{
		Name:             req.Name,
		Contact:          req.Contact,
		LeadTimeDays:     req.LeadTimeDays,
		DeliveryWeekdays: req.DeliveryWeekdays,
		OrderCutoff:      cutoff,
	})
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, supplier)
}

type setTermsRequest struct {
	PurchaseUnit string   `json:"purchase_unit"`
	UnitFactor   qty.Qty  `json:"unit_factor"`
	MinOrderQty  qty.Qty  `json:"min_order_qty,omitempty"`
	PackMultiple qty.Qty  `json:"pack_multiple,omitempty"`
	Price        *qty.Qty `json:"price,omitempty"`
}

func (h *Handler) handleSetTerms(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	supplierID, err := uuid.Parse(chi.URLParam(r, "supplierId"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemId"))
	if err != nil {
		httpx.Error(w, r, httpx.NotFound())
		return
	}

	var req setTermsRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if !req.UnitFactor.IsPositive() {
		httpx.Error(w, r, httpx.Invalid(httpx.FieldError{
			Field:   "unit_factor",
			Message: "Сколько базовых единиц в единице закупки: например, 12 для коробки на 12 л.",
		}))
		return
	}

	terms, err := h.svc.SetTerms(r.Context(), principal.Tenant, Terms{
		SupplierID:   supplierID,
		ItemID:       itemID,
		PurchaseUnit: req.PurchaseUnit,
		UnitFactor:   req.UnitFactor,
		MinOrderQty:  req.MinOrderQty,
		PackMultiple: req.PackMultiple,
		Price:        req.Price,
	})
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}
	httpx.JSON(w, http.StatusOK, terms)
}

// toHTTP переводит доменные ошибки каталога в ответы RFC 9457.
func toHTTP(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NotFound()
	case errors.Is(err, ErrNameTaken):
		return httpx.Conflict("name-taken", "Название занято",
			"Позиция или поставщик с таким названием уже есть.")
	case errors.Is(err, ErrInvalidUnit):
		return httpx.Invalid(httpx.FieldError{Field: "base_unit", Message: "Допустимы kg, l и pcs."})
	case errors.Is(err, ErrInvalidService):
		return httpx.Invalid(httpx.FieldError{Field: "service_level", Message: "Допустимы 90, 95 и 99."})
	case errors.Is(err, ErrBadWeekdays):
		return httpx.Invalid(httpx.FieldError{
			Field: "delivery_weekdays", Message: "Дни доставки — числа от 1 до 7 без повторов.",
		})
	default:
		return err
	}
}

// handleImport загружает номенклатуру из CSV (FR-4).
//
// Файл принимается и как multipart-форма (так шлёт браузер), и как сырое
// тело: вторым способом удобно проверять импорт из командной строки.
//
// Параметр dry_run=true даёт только предпросмотр: в базе ничего не меняется.
func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	principal := auth.MustFromContext(r.Context())

	dryRun := r.URL.Query().Get("dry_run") == "true"

	body, err := importBody(w, r)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	defer func() { _ = body.Close() }()

	parsed, err := ParseCSV(body)
	if err != nil {
		switch {
		case errors.Is(err, ErrNoRows):
			httpx.Error(w, r, httpx.BadRequest("В файле нет данных."))
		case errors.Is(err, ErrNotText):
			httpx.Error(w, r, httpx.BadRequest(
				"Не удалось прочитать файл. Сохраните его из Excel как «CSV UTF-8»."))
		default:
			httpx.Error(w, r, httpx.BadRequest(err.Error()))
		}
		return
	}

	result, err := h.svc.Import(r.Context(), principal.Tenant, parsed, principal.UserID, dryRun)
	if err != nil {
		httpx.Error(w, r, toHTTP(err))
		return
	}

	// Ошибки по строкам — это не отказ сервера, а результат предпросмотра:
	// владелец должен увидеть их списком и поправить файл.
	status := http.StatusOK
	if !dryRun && result.OK() {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, result)
}

// importBody достаёт содержимое файла из запроса.
func importBody(w http.ResponseWriter, r *http.Request) (io.ReadCloser, error) {
	// Лимит 5 МБ — отдельный от общего лимита тела запроса (§12.2).
	r.Body = http.MaxBytesReader(w, r.Body, MaxCSVSize+1024)

	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		return r.Body, nil
	}

	if err := r.ParseMultipartForm(MaxCSVSize); err != nil {
		return nil, httpx.BadRequest("Не удалось прочитать файл: " + err.Error())
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, httpx.Invalid(httpx.FieldError{
			Field: "file", Message: "Приложите CSV-файл.",
		})
	}
	return file, nil
}
