-- Запросы входа и сессий.
--
-- Часть из них выполняется от обслуживающей роли (BYPASSRLS): в момент входа
-- и разбора сессии тенант ещё не известен (ADR-005). Такие запросы помечены
-- в комментарии.

-- name: GetUserByEmail :one
-- Кросс-тенантный: email уникален глобально. Только обслуживающая роль.
SELECT * FROM users WHERE email = lower($1);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO users (id, tenant_id, email, password_hash, name, role)
VALUES ($1, $2, lower($3), $4, $5, $6)
RETURNING *;

-- name: ListUsers :many
SELECT * FROM users WHERE tenant_id = $1 ORDER BY created_at;

-- name: SetUserTelegramChatID :exec
UPDATE users SET telegram_chat_id = $2 WHERE id = $1;

-- name: ClearUserTelegramChatID :exec
UPDATE users SET telegram_chat_id = NULL WHERE telegram_chat_id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, tenant_id, csrf_hash, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSession :one
-- Кросс-тенантный: тенант определяется как раз по сессии.
-- Отдаёт сразу и пользователя, чтобы не ходить в БД дважды на каждый запрос.
SELECT
    s.token_hash,
    s.csrf_hash,
    s.expires_at,
    s.last_seen_at,
    u.id         AS user_id,
    u.tenant_id,
    u.email,
    u.name,
    u.role,
    u.telegram_chat_id,
    t.name       AS tenant_name,
    t.timezone,
    t.is_sandbox,
    t.clock_offset,
    t.settings,
    t.autopilot,
    t.seed,
    t.expires_at AS tenant_expires_at
FROM sessions s
JOIN users u   ON u.id = s.user_id
JOIN tenants t ON t.id = u.tenant_id
WHERE s.token_hash = $1 AND s.expires_at > $2;

-- name: TouchSession :exec
-- Продление срока сессии; last_seen_at заодно показывает активность.
UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < $1;

-- name: CreateTelegramLinkCode :exec
INSERT INTO telegram_link_codes (code, tenant_id, user_id, expires_at)
VALUES ($1, $2, $3, $4);

-- name: UseTelegramLinkCode :one
-- Код одноразовый: помечаем использованным той же командой, что и читаем.
UPDATE telegram_link_codes
SET used_at = $2
WHERE code = $1 AND used_at IS NULL AND expires_at > $2
RETURNING tenant_id, user_id;

-- name: DeleteExpiredTelegramLinkCodes :execrows
DELETE FROM telegram_link_codes WHERE expires_at < $1;

-- name: InsertAuditLog :exec
INSERT INTO audit_log (id, tenant_id, user_id, action, entity, entity_id, diff, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
