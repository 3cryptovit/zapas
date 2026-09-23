package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

// Audit пишет запись в журнал аудита: входы, изменения настроек, заказы
// и сторно (§12.2, «Расследование инцидентов»).
//
// Ошибка записи не ломает основную операцию — вход не должен падать из-за
// журнала, — но обязательно попадает в лог.
func (s *Service) Audit(ctx context.Context, e AuditEntry) {
	s.audit(ctx, e.TenantID, e.UserID, e.Action, e.Entity, e.EntityID, e.IP)
}

// AuditEntry — одна запись журнала.
type AuditEntry struct {
	TenantID uuid.UUID
	UserID   *uuid.UUID
	Action   string
	Entity   string
	EntityID *uuid.UUID
	IP       string
	Diff     any
}

func (s *Service) audit(ctx context.Context, tenantID uuid.UUID, userID *uuid.UUID, action, entity string, entityID *uuid.UUID, ip string) {
	err := s.db.InTenantTx(ctx, tenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
			ID:       uuid.Must(uuid.NewV7()),
			TenantID: tenantID,
			UserID:   nullUUID(userID),
			Action:   action,
			Entity:   entity,
			EntityID: nullUUID(entityID),
			Diff:     []byte(`{}`),
			Ip:       parseIP(ip),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "не удалось записать в журнал аудита",
			slog.String("action", action),
			slog.String("err", err.Error()),
		)
	}
}

// AuditWithDiff пишет запись вместе с изменением.
func (s *Service) AuditWithDiff(ctx context.Context, e AuditEntry) {
	diff := []byte(`{}`)
	if e.Diff != nil {
		if encoded, err := json.Marshal(e.Diff); err == nil {
			diff = encoded
		}
	}

	err := s.db.InTenantTx(ctx, e.TenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
			ID:       uuid.Must(uuid.NewV7()),
			TenantID: e.TenantID,
			UserID:   nullUUID(e.UserID),
			Action:   e.Action,
			Entity:   e.Entity,
			EntityID: nullUUID(e.EntityID),
			Diff:     diff,
			Ip:       parseIP(e.IP),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "не удалось записать в журнал аудита",
			slog.String("action", e.Action),
			slog.String("err", err.Error()),
		)
	}
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// parseIP разбирает адрес из RealIP. Мусор молча превращается в NULL:
// журнал аудита не повод ронять запрос.
func parseIP(s string) *netip.Addr {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		if ap, err := netip.ParseAddrPort(s); err == nil {
			a := ap.Addr()
			return &a
		}
		return nil
	}
	return &addr
}
