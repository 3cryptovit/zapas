package stock

import (
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

// movementFromRow переводит строку БД в доменное движение.
func movementFromRow(row sqlc.StockMovement) Movement {
	m := Movement{
		ID:         row.ID,
		ItemID:     row.ItemID,
		Type:       Type(row.Type),
		Qty:        postgres.Qty(row.Qty),
		OccurredAt: postgres.TimeOrZero(row.OccurredAt),
		CreatedAt:  postgres.TimeOrZero(row.CreatedAt),
		CreatedBy:  ptrUUID(row.CreatedBy),
		Comment:    row.Comment,
		OrderID:    ptrUUID(row.OrderID),
		CountID:    ptrUUID(row.CountID),
		ReversesID: ptrUUID(row.ReversesID),
	}
	m.TypeLabel = m.Type.Label()
	if row.Reason != nil {
		m.Reason = *row.Reason
		m.ReasonLabel = ReasonLabel(*row.Reason)
	}
	return m
}

// movementFromListRow переводит строку журнала: в ней уже есть название
// позиции и автор, чтобы не делать N+1 запросов.
func movementFromListRow(row sqlc.ListMovementsRow) Movement {
	m := Movement{
		ID:         row.ID,
		ItemID:     row.ItemID,
		ItemName:   row.ItemName,
		BaseUnit:   row.BaseUnit,
		Type:       Type(row.Type),
		Qty:        postgres.Qty(row.Qty),
		OccurredAt: postgres.TimeOrZero(row.OccurredAt),
		CreatedAt:  postgres.TimeOrZero(row.CreatedAt),
		CreatedBy:  ptrUUID(row.CreatedBy),
		Comment:    row.Comment,
		OrderID:    ptrUUID(row.OrderID),
		CountID:    ptrUUID(row.CountID),
		ReversesID: ptrUUID(row.ReversesID),
	}
	m.TypeLabel = m.Type.Label()
	if row.AuthorName != nil {
		m.AuthorName = *row.AuthorName
	}
	if row.Reason != nil {
		m.Reason = *row.Reason
		m.ReasonLabel = ReasonLabel(*row.Reason)
	}
	return m
}

func ptrUUID(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	v := id.UUID
	return &v
}
