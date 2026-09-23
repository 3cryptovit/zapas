package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
	"github.com/vostapenko/zapas/internal/tenant"
)

// ErrBadCursor — курсор ленты не разбирается.
var ErrBadCursor = errors.New("notify: некорректный курсор")

// Feed — страница ленты со счётчиком непрочитанных (§6).
type Feed struct {
	Items      []Notification `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Unread     int            `json:"unread"`
}

// List отдаёт ленту тенанта. Уведомления без адресата видят все.
func (s *Service) List(ctx context.Context, t tenant.Tenant, userID uuid.UUID, cursor string, limit int) (Feed, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	cursorAt, cursorID, err := decodeCursor(cursor)
	if err != nil {
		return Feed{}, err
	}

	feed := Feed{Items: []Notification{}}

	err = s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		q := sqlc.New(tx)

		rows, err := q.ListNotifications(ctx, sqlc.ListNotificationsParams{
			TenantID: t.ID,
			UserID:   uuid.NullUUID{UUID: userID, Valid: true},
			CursorAt: cursorAt,
			CursorID: cursorID,
			// На одну больше: так видно, есть ли следующая страница.
			RowLimit: int32(limit + 1),
		})
		if err != nil {
			return fmt.Errorf("notify: лента: %w", err)
		}

		for i, r := range rows {
			if i == limit {
				last := rows[i-1]
				feed.NextCursor = encodeCursor(postgres.TimeOrZero(last.CreatedAt), last.ID)
				break
			}
			n := Notification{
				ID:        r.ID,
				Type:      Type(r.Type),
				Read:      r.ReadAt.Valid,
				CreatedAt: postgres.TimeOrZero(r.CreatedAt).Format(time.RFC3339),
			}
			n.TypeLabel = n.Type.Label()
			if r.UserID.Valid {
				id := r.UserID.UUID
				n.UserID = &id
			}
			// Испорченный payload не должен ронять всю ленту.
			_ = json.Unmarshal(r.Payload, &n.Payload)
			feed.Items = append(feed.Items, n)
		}

		unread, err := q.CountUnreadNotifications(ctx, sqlc.CountUnreadNotificationsParams{
			TenantID: t.ID,
			UserID:   uuid.NullUUID{UUID: userID, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("notify: счётчик непрочитанных: %w", err)
		}
		feed.Unread = int(unread)
		return nil
	})
	return feed, err
}

// MarkRead отмечает уведомления прочитанными. Пустой список означает
// «всё прочитано».
func (s *Service) MarkRead(ctx context.Context, t tenant.Tenant, userID uuid.UUID, ids []uuid.UUID) (int, error) {
	if ids == nil {
		ids = []uuid.UUID{}
	}

	var affected int
	err := s.db.InTenantTx(ctx, t.ID.String(), func(ctx context.Context, tx postgres.Tx) error {
		n, err := sqlc.New(tx).MarkNotificationsRead(ctx, sqlc.MarkNotificationsReadParams{
			TenantID: t.ID,
			UserID:   uuid.NullUUID{UUID: userID, Valid: true},
			ReadAt:   postgres.Time(s.clock.Now()),
			Ids:      ids,
		})
		if err != nil {
			return fmt.Errorf("notify: отметка прочитанным: %w", err)
		}
		affected = int(n)
		return nil
	})
	return affected, err
}

// --- курсор ---

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
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
