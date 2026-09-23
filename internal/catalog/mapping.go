package catalog

import (
	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

func itemFromRow(row sqlc.Item) Item {
	item := Item{
		ID:           row.ID,
		Name:         row.Name,
		BaseUnit:     BaseUnit(row.BaseUnit),
		CategoryID:   ptrUUID(row.CategoryID),
		SupplierID:   ptrUUID(row.DefaultSupplierID),
		ServiceLevel: int(row.ServiceLevel),
		ManualMinQty: postgres.Qty(row.ManualMinQty),
		ArchivedAt:   postgres.TimePtr(row.ArchivedAt),
	}
	item.UnitLabel = item.BaseUnit.Label()
	return item
}

func supplierFromRow(row sqlc.Supplier) Supplier {
	return Supplier{
		ID:               row.ID,
		Name:             row.Name,
		Contact:          row.Contact,
		LeadTimeDays:     int(row.LeadTimeDays),
		DeliveryWeekdays: fromInt16(row.DeliveryWeekdays),
		OrderCutoff:      postgres.ClockTime(row.OrderCutoff),
		ArchivedAt:       postgres.TimePtr(row.ArchivedAt),
	}
}

func termsFromRow(row sqlc.SupplierItem) Terms {
	t := Terms{
		SupplierID:   row.SupplierID,
		ItemID:       row.ItemID,
		PurchaseUnit: row.PurchaseUnit,
		UnitFactor:   postgres.Qty(row.UnitFactor),
		MinOrderQty:  postgres.Qty(row.MinOrderQty),
		PackMultiple: postgres.Qty(row.PackMultiple),
	}
	if price, ok := postgres.NullQty(row.Price); ok {
		t.Price = &price
	}
	return t
}

func ptrUUID(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	v := id.UUID
	return &v
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// toInt16 и fromInt16 переводят дни доставки между доменом и smallint[].
func toInt16(days []int) []int16 {
	out := make([]int16, 0, len(days))
	for _, d := range days {
		out = append(out, int16(d))
	}
	return out
}

func fromInt16(days []int16) []int {
	out := make([]int, 0, len(days))
	for _, d := range days {
		out = append(out, int(d))
	}
	return out
}
