package auth

// Role — роль пользователя. В MVP их две (§2).
type Role string

const (
	// RoleOwner — собственник или управляющий: настраивает позиции
	// и поставщиков, оформляет заказы, управляет сотрудниками.
	RoleOwner Role = "owner"
	// RoleStaff — бариста, повар, кладовщик: вносит движения и принимает
	// поставки, но не меняет настройки и не оформляет заказы.
	RoleStaff Role = "staff"
)

// Valid сообщает, что роль известна.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleStaff }

// Label — название роли для интерфейса.
func (r Role) Label() string {
	switch r {
	case RoleOwner:
		return "Владелец"
	case RoleStaff:
		return "Сотрудник"
	default:
		return string(r)
	}
}

// CanManageCatalog — заводить и менять позиции, категории и поставщиков.
func (r Role) CanManageCatalog() bool { return r == RoleOwner }

// CanManageSettings — менять настройки тенанта и приглашать сотрудников.
func (r Role) CanManageSettings() bool { return r == RoleOwner }

// CanManageOrders — оформлять, отправлять и отменять заказы.
// Приёмка доступна всем: её делает тот, кто встречает поставку (§6).
func (r Role) CanManageOrders() bool { return r == RoleOwner }

// CanExportMovements — выгружать журнал движений в CSV.
func (r Role) CanExportMovements() bool { return r == RoleOwner }

// CanReverseOthers — сторнировать чужие движения. Своё движение автор
// сторнирует сам, проверку авторства делает сервис (§2).
func (r Role) CanReverseOthers() bool { return r == RoleOwner }
