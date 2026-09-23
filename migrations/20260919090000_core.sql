-- +goose Up
-- +goose StatementBegin

-- Тенанты — корень изоляции. Всё остальное ссылается сюда и удаляется каскадом.
CREATE TABLE tenants (
    id              uuid PRIMARY KEY,
    name            text        NOT NULL CHECK (length(btrim(name)) > 0),
    timezone        text        NOT NULL DEFAULT 'Europe/Moscow',
    is_sandbox      boolean     NOT NULL DEFAULT false,
    expires_at      timestamptz,                      -- только у песочниц
    clock_offset    interval    NOT NULL DEFAULT '0', -- виртуальное время (ADR-006)
    seed            bigint      NOT NULL DEFAULT 0,   -- воспроизводимость генератора
    autopilot       boolean     NOT NULL DEFAULT false,
    settings        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenants_sandbox_expires CHECK (NOT is_sandbox OR expires_at IS NOT NULL)
);

-- Очистка песочниц раз в час ходит только по этому индексу.
CREATE INDEX tenants_expires_idx ON tenants (expires_at) WHERE is_sandbox;

CREATE TYPE user_role AS ENUM ('owner', 'staff');

CREATE TABLE users (
    id               uuid PRIMARY KEY,
    tenant_id        uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    email            text        NOT NULL,
    password_hash    text        NOT NULL,
    name             text        NOT NULL DEFAULT '',
    role             user_role   NOT NULL,
    telegram_chat_id bigint,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- Email уникален глобально: по нему ищут пользователя до выбора тенанта.
    CONSTRAINT users_email_unique UNIQUE (email)
);

CREATE INDEX users_tenant_idx ON users (tenant_id);
CREATE UNIQUE INDEX users_telegram_idx ON users (telegram_chat_id) WHERE telegram_chat_id IS NOT NULL;

-- Составной ключ, чтобы дочерние записи не связались с пользователем чужого тенанта.
ALTER TABLE users ADD CONSTRAINT users_tenant_id_unique UNIQUE (tenant_id, id);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,      -- SHA-256 токена; сам токен в БД не хранится
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tenant_id    uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    csrf_hash    bytea       NOT NULL,
    user_agent   text        NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE warehouses (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT warehouses_tenant_id_unique UNIQUE (tenant_id, id)
);

CREATE INDEX warehouses_tenant_idx ON warehouses (tenant_id);

-- Одноразовые коды привязки Telegram: t.me/<bot>?start=<code>, живут 15 минут.
CREATE TABLE telegram_link_codes (
    code       text PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX telegram_link_codes_expires_idx ON telegram_link_codes (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS telegram_link_codes;
DROP TABLE IF EXISTS warehouses;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS user_role;
DROP TABLE IF EXISTS tenants;
-- +goose StatementEnd
