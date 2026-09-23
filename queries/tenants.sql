-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;

-- name: CreateTenant :one
INSERT INTO tenants (id, name, timezone, is_sandbox, expires_at, seed, settings)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateTenantSettings :exec
UPDATE tenants SET settings = $2 WHERE id = $1;

-- name: AdvanceTenantClock :one
UPDATE tenants SET clock_offset = clock_offset + $2::interval
WHERE id = $1
RETURNING clock_offset;

-- name: ListActiveTenants :many
-- Обход тенантов ночными задачами. Истёкшие песочницы пропускаются:
-- их всё равно вот-вот удалит очистка.
SELECT * FROM tenants
WHERE expires_at IS NULL OR expires_at > $1
ORDER BY created_at;

-- name: ListExpiredSandboxes :many
-- Очистка песочниц: раз в час, пачками, чтобы не блокировать БД (§7.5).
SELECT id FROM tenants
WHERE is_sandbox AND expires_at IS NOT NULL AND expires_at <= $1
ORDER BY expires_at
LIMIT $2;

-- name: DeleteTenant :exec
-- Удаление каскадом уносит всё содержимое тенанта.
DELETE FROM tenants WHERE id = $1;

-- name: CountActiveSandboxes :one
SELECT count(*)::int AS active FROM tenants
WHERE is_sandbox AND (expires_at IS NULL OR expires_at > $1);

-- name: SetTenantAutopilot :exec
UPDATE tenants SET autopilot = $2 WHERE id = $1;

-- name: ResetTenantClock :exec
UPDATE tenants SET clock_offset = '0' WHERE id = $1;
