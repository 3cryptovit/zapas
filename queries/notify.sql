-- Лента, transactional outbox и журнал аудита (§5.5, ADR-004).

-- name: InsertNotification :one
-- Дедупликация уникальным индексом по (tenant_id, dedup_key):
-- повтор молча не создаёт вторую запись.
INSERT INTO notifications (id, tenant_id, user_id, type, payload, dedup_key, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, dedup_key) DO NOTHING
RETURNING *;

-- name: ListNotifications :many
SELECT * FROM notifications
WHERE tenant_id = $1
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id) OR user_id IS NULL)
  AND (
      sqlc.narg(cursor_at)::timestamptz IS NULL
      OR (created_at, id) < (sqlc.narg(cursor_at), sqlc.narg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountUnreadNotifications :one
SELECT count(*)::int AS unread
FROM notifications
WHERE tenant_id = $1 AND read_at IS NULL
  AND (user_id IS NULL OR user_id = $2);

-- name: MarkNotificationsRead :execrows
UPDATE notifications SET read_at = $3
WHERE tenant_id = $1 AND read_at IS NULL
  AND (user_id IS NULL OR user_id = $2)
  AND (cardinality(sqlc.arg(ids)::uuid[]) = 0 OR id = ANY(sqlc.arg(ids)::uuid[]));

-- name: InsertOutbox :exec
INSERT INTO outbox (id, tenant_id, notification_id, channel, recipient, payload, next_attempt_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ClaimOutbox :many
-- Воркер забирает пачку. SKIP LOCKED позволяет нескольким воркерам работать
-- параллельно, не дожидаясь друг друга (ADR-004).
UPDATE outbox o SET attempts = o.attempts + 1
WHERE o.id IN (
    SELECT c.id FROM outbox c
    WHERE c.status = 'pending' AND c.next_attempt_at <= $1
    ORDER BY c.next_attempt_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
RETURNING o.*;

-- name: MarkOutboxSent :exec
UPDATE outbox SET status = 'sent', sent_at = $2, last_error = '' WHERE id = $1;

-- name: RescheduleOutbox :exec
UPDATE outbox SET next_attempt_at = $2, last_error = $3 WHERE id = $1;

-- name: MarkOutboxFailed :exec
UPDATE outbox SET status = 'failed', last_error = $2 WHERE id = $1;

-- name: CountPendingOutbox :one
SELECT count(*)::int AS pending FROM outbox WHERE status = 'pending';

-- name: ListRecipients :many
-- Кому слать: владельцу — сводки и алерты, сотруднику — только лента.
SELECT id, email, name, role, telegram_chat_id
FROM users
WHERE tenant_id = $1 AND role = 'owner';
