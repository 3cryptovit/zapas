package stock

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/qty"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// MaxPageSize ограничивает размер страницы журнала.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// ListFilter — фильтры журнала движений (FR-11).
type ListFilter struct {
	ItemID   *uuid.UUID
	Type     *Type
	AuthorID *uuid.UUID
	From     *time.Time
	To       *time.Time
	Cursor   string
	Limit    int
}

// Page — страница журнала с курсором на следующую.
type Page struct {
	Items      []Movement
	NextCursor string
}

// ListMovements отдаёт журнал с курсорной пагинацией.
//
// Курсор — пара (occurred_at, id). Смещение по OFFSET здесь не годится:
// при вставке новых движений страницы поедут и часть записей пропадёт.
func (s *Service) ListMovements(ctx context.Context, t tenant.Tenant, f ListFilter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}

	cursorAt, cursorID, err := decodeCursor(f.Cursor)
	if err != nil {
		return Page{}, err
	}

	params := sqlc.ListMovementsParams{
		TenantID: t.ID,
		CursorAt: cursorAt,
		CursorID: cursorID,
		// Просим на одну запись больше: так видно, есть ли следующая страница,
		// без отдельного COUNT.
		RowLimit: int32(limit + 1),
	}
	if f.ItemID != nil {
		params.ItemID = uuid.NullUUID{UUID: *f.ItemID, Valid: true}
	}
	if f.AuthorID != nil {
		params.AuthorID = uuid.NullUUID{UUID: *f.AuthorID, Valid: true}
	}
	if f.Type != nil {
		mt := sqlc.MovementType(*f.Type)
		params.MovementType = &mt
	}
	if f.From != nil {
		params.FromAt = postgres.Time(*f.From)
	}
	if f.To != nil {
		params.ToAt = postgres.Time(*f.To)
	}

	var rows []sqlc.ListMovementsRow
	err = s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		r, err := sqlc.New(tx).ListMovements(ctx, params)
		if err != nil {
			return fmt.Errorf("stock: журнал движений: %w", err)
		}
		rows = r
		return nil
	})
	if err != nil {
		return Page{}, err
	}

	page := Page{Items: make([]Movement, 0, limit)}
	for i, row := range rows {
		if i == limit {
			// Лишняя запись только сообщила, что страница не последняя.
			last := page.Items[len(page.Items)-1]
			page.NextCursor = encodeCursor(last.OccurredAt, last.ID)
			break
		}
		page.Items = append(page.Items, movementFromListRow(row))
	}
	return page, nil
}

// GetBalance отдаёт остаток позиции.
func (s *Service) GetBalance(ctx context.Context, t tenant.Tenant, itemID uuid.UUID) (Balance, error) {
	var out Balance
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		b, err := s.readBalance(ctx, sqlc.New(tx), t.ID, itemID)
		if err != nil {
			return err
		}
		out = b
		return nil
	})
	return out, err
}

// ListBalances отдаёт остатки всех позиций тенанта.
func (s *Service) ListBalances(ctx context.Context, t tenant.Tenant) (map[uuid.UUID]Balance, error) {
	out := map[uuid.UUID]Balance{}
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		rows, err := sqlc.New(tx).ListBalances(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("stock: остатки: %w", err)
		}
		for _, row := range rows {
			out[row.ItemID] = Balance{
				ItemID:    row.ItemID,
				OnHand:    postgres.Qty(row.OnHand),
				UpdatedAt: postgres.TimeOrZero(row.UpdatedAt),
			}
		}
		return nil
	})
	return out, err
}

// Reconcile сверяет таблицу остатков с журналом (FR-17).
//
// Таблица остатков — кэш журнала, а кэш имеет право разойтись. Ночная задача
// ловит расхождение, а не ждёт, пока его заметит владелец.
func (s *Service) Reconcile(ctx context.Context, t tenant.Tenant) ([]Drift, error) {
	var drifts []Drift

	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		sums, err := q.SumMovementsByItem(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("stock: сумма движений: %w", err)
		}
		balances, err := q.ListBalances(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("stock: остатки: %w", err)
		}

		expected := make(map[uuid.UUID]sqlc.SumMovementsByItemRow, len(sums))
		for _, row := range sums {
			expected[row.ItemID] = row
		}

		for _, b := range balances {
			want := postgres.Qty(expected[b.ItemID].Total)
			got := postgres.Qty(b.OnHand)
			if !want.Equal(got) {
				drifts = append(drifts, Drift{ItemID: b.ItemID, Journal: want, Balance: got})
			}
			delete(expected, b.ItemID)
		}
		// Позиция с движениями, но без строки остатка — тоже расхождение.
		for itemID, row := range expected {
			if total := postgres.Qty(row.Total); !total.IsZero() {
				drifts = append(drifts, Drift{ItemID: itemID, Journal: total})
			}
		}
		return nil
	})
	return drifts, err
}

// Drift — расхождение остатка с журналом.
type Drift struct {
	ItemID  uuid.UUID
	Journal qty.Qty
	Balance qty.Qty
}

func (d Drift) String() string {
	return fmt.Sprintf("позиция %s: журнал %s, остаток %s", d.ItemID, d.Journal, d.Balance)
}

// --- курсор ---

// ErrBadCursor — курсор не разбирается. Обычно это ручная правка ссылки.
var ErrBadCursor = errors.New("stock: некорректный курсор")

func encodeCursor(at time.Time, id uuid.UUID) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (pgtype.Timestamptz, uuid.NullUUID, error) {
	if cursor == "" {
		return pgtype.Timestamptz{}, uuid.NullUUID{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return pgtype.Timestamptz{}, uuid.NullUUID{}, ErrBadCursor
	}
	at, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return pgtype.Timestamptz{}, uuid.NullUUID{}, ErrBadCursor
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return pgtype.Timestamptz{}, uuid.NullUUID{}, ErrBadCursor
	}
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return pgtype.Timestamptz{}, uuid.NullUUID{}, ErrBadCursor
	}
	return postgres.Time(parsedAt), uuid.NullUUID{UUID: parsedID, Valid: true}, nil
}
