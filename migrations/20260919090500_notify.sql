-- +goose Up
-- +goose StatementBegin

CREATE TYPE notification_type AS ENUM (
    'daily_digest',     -- ежедневная сводка
    'critical',         -- срочный алерт: позиция стала красной
    'cutoff_reminder',  -- за 2 часа до отсечки, заказ не отправлен
    'order_late',       -- поставка опаздывает
    'receipt_mismatch'  -- расхождение при приёмке больше 5%
);

CREATE TYPE outbox_channel AS ENUM ('telegram', 'email');
CREATE TYPE outbox_status AS ENUM ('pending', 'sent', 'failed');

-- Лента в интерфейсе. Дедупликация — уникальным индексом по dedup_key (§5.5).
CREATE TABLE notifications (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id    uuid,
    type       notification_type NOT NULL,
    payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    dedup_key  text NOT NULL,
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notifications_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT notifications_user_fk FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id) ON DELETE CASCADE
);

-- Ключ уникален внутри тенанта: critical:<item>:<date> и т. п.
CREATE UNIQUE INDEX notifications_dedup_idx ON notifications (tenant_id, dedup_key);
CREATE INDEX notifications_feed_idx ON notifications (tenant_id, created_at DESC, id DESC);
CREATE INDEX notifications_unread_idx ON notifications (tenant_id, user_id)
    WHERE read_at IS NULL;

-- Transactional outbox (ADR-004): запись идёт в одной транзакции со сменой
-- статуса, отправкой занимается воркер.
CREATE TABLE outbox (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    notification_id uuid,
    channel         outbox_channel NOT NULL,
    recipient       text NOT NULL,         -- chat_id или email
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          outbox_status NOT NULL DEFAULT 'pending',
    attempts        smallint NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    CONSTRAINT outbox_notification_fk FOREIGN KEY (tenant_id, notification_id)
        REFERENCES notifications (tenant_id, id) ON DELETE CASCADE
);

-- Воркер забирает пачки строго по этому индексу (FOR UPDATE SKIP LOCKED).
CREATE INDEX outbox_pending_idx ON outbox (next_attempt_at) WHERE status = 'pending';

CREATE TABLE audit_log (
    id        uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id   uuid,
    action    text NOT NULL,        -- login, settings.update, order.send, movement.reverse
    entity    text NOT NULL DEFAULT '',
    entity_id uuid,
    diff      jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip        inet,
    at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_tenant_at_idx ON audit_log (tenant_id, at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS notifications;
DROP TYPE IF EXISTS outbox_status;
DROP TYPE IF EXISTS outbox_channel;
DROP TYPE IF EXISTS notification_type;
-- +goose StatementEnd
