package auth

import (
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/clock"
)

// MeResponse — ответ GET /me: кто вошёл и в каком тенанте (§11).
//
// Виртуальная дата отдаётся всем: интерфейс показывает её в плашке демо,
// а рабочему тенанту она равна настоящей.
type MeResponse struct {
	User   userDTO   `json:"user"`
	Tenant tenantDTO `json:"tenant"`
}

type userDTO struct {
	ID             uuid.UUID `json:"id"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	Role           Role      `json:"role"`
	RoleLabel      string    `json:"role_label"`
	TelegramLinked bool      `json:"telegram_linked"`
}

type tenantDTO struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Timezone  string    `json:"timezone"`
	IsSandbox bool      `json:"is_sandbox"`
	// Today — сегодняшний день по часам тенанта: для песочницы он смещён.
	Today clock.Day `json:"today"`
	// ExpiresAt — когда удалится демо-тенант; у рабочего пусто.
	ExpiresAt *string `json:"expires_at,omitempty"`
	// Permissions — что доступно этой роли. Интерфейс прячет кнопки по ним,
	// но решение всё равно принимает сервер.
	Permissions permissionsDTO `json:"permissions"`
}

type permissionsDTO struct {
	ManageCatalog  bool `json:"manage_catalog"`
	ManageSettings bool `json:"manage_settings"`
	ManageOrders   bool `json:"manage_orders"`
	ExportCSV      bool `json:"export_csv"`
}

func meResponse(p Principal, cl clock.Clock) MeResponse {
	resp := MeResponse{
		User: userDTO{
			ID:             p.UserID,
			Email:          p.Email,
			Name:           p.Name,
			Role:           p.Role,
			RoleLabel:      p.Role.Label(),
			TelegramLinked: p.TelegramChatID != nil,
		},
		Tenant: tenantDTO{
			ID:        p.Tenant.ID,
			Name:      p.Tenant.Name,
			Timezone:  p.Tenant.Loc().String(),
			IsSandbox: p.Tenant.IsSandbox,
			Today:     p.Tenant.Today(cl),
			Permissions: permissionsDTO{
				ManageCatalog:  p.Role.CanManageCatalog(),
				ManageSettings: p.Role.CanManageSettings(),
				ManageOrders:   p.Role.CanManageOrders(),
				ExportCSV:      p.Role.CanExportMovements(),
			},
		},
	}
	if p.Tenant.ExpiresAt != nil {
		formatted := p.Tenant.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
		resp.Tenant.ExpiresAt = &formatted
	}
	return resp
}
