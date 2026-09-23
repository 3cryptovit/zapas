-- +goose Up
-- +goose StatementBegin

CREATE TYPE movement_type AS ENUM (
    'opening',    -- начальный остаток при импорте
    'receipt',    -- приход
    'usage',      -- расход
    'writeoff',   -- списание
    'adjustment', -- корректировка после пересчёта
    'reversal'    -- сторно
);

CREATE TYPE count_status AS ENUM ('draft', 'posted');

-- Инвентаризация объявлена раньше движений: движение-корректировка ссылается на пересчёт.
CREATE TABLE stock_counts (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    warehouse_id uuid NOT NULL,
    status       count_status NOT NULL DEFAULT 'draft',
    scope        text NOT NULL DEFAULT 'all',  -- all | category | key
    note         text NOT NULL DEFAULT '',
    created_by   uuid,
    posted_by    uuid,
    posted_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT stock_counts_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT stock_counts_warehouse_fk FOREIGN KEY (tenant_id, warehouse_id)
        REFERENCES warehouses (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT stock_counts_posted CHECK (
        (status = 'posted') = (posted_at IS NOT NULL)
    )
);

CREATE INDEX stock_counts_tenant_created_idx ON stock_counts (tenant_id, created_at DESC);

CREATE TABLE stock_count_lines (
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    count_id    uuid NOT NULL,
    item_id     uuid NOT NULL,
    -- Учётный остаток на момент открытия документа: по нему считается расхождение
    -- в интерфейсе. Фактическая корректировка при проведении берёт свежий остаток.
    expected_qty numeric(14,3) NOT NULL DEFAULT 0,
    counted_qty  numeric(14,3) CHECK (counted_qty IS NULL OR counted_qty >= 0),
    PRIMARY KEY (count_id, item_id),
    CONSTRAINT stock_count_lines_count_fk FOREIGN KEY (tenant_id, count_id)
        REFERENCES stock_counts (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT stock_count_lines_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

-- Журнал движений. Только INSERT: UPDATE и DELETE отозваны у роли приложения
-- в миграции *_rls.sql (ADR-002). Ошибка исправляется сторно.
CREATE TABLE stock_movements (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    warehouse_id    uuid NOT NULL,
    item_id         uuid NOT NULL,
    type            movement_type NOT NULL,
    -- Знак уже учтён: расход и списание хранятся отрицательными.
    qty             numeric(14,3) NOT NULL CHECK (qty <> 0),
    occurred_at     timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    created_by      uuid,
    reason          text,
    comment         text NOT NULL DEFAULT '',
    order_id        uuid,
    count_id        uuid,
    reverses_id     uuid,
    idempotency_key text,
    CONSTRAINT stock_movements_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT stock_movements_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT stock_movements_warehouse_fk FOREIGN KEY (tenant_id, warehouse_id)
        REFERENCES warehouses (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT stock_movements_count_fk FOREIGN KEY (tenant_id, count_id)
        REFERENCES stock_counts (tenant_id, id) ON DELETE SET NULL (count_id),
    CONSTRAINT stock_movements_reverses_fk FOREIGN KEY (tenant_id, reverses_id)
        REFERENCES stock_movements (tenant_id, id),
    -- Знак жёстко связан с типом: приход не может быть отрицательным.
    CONSTRAINT stock_movements_sign CHECK (
        CASE type
            WHEN 'opening' THEN qty > 0
            WHEN 'receipt' THEN qty > 0
            WHEN 'usage' THEN qty < 0
            WHEN 'writeoff' THEN qty < 0
            ELSE true                     -- adjustment и reversal бывают любого знака
        END
    ),
    -- Причина списания обязательна (FR-6 таблицы движений).
    CONSTRAINT stock_movements_writeoff_reason CHECK (
        type <> 'writeoff' OR (reason IS NOT NULL AND length(btrim(reason)) > 0)
    ),
    CONSTRAINT stock_movements_reversal_link CHECK (
        (type = 'reversal') = (reverses_id IS NOT NULL)
    )
);

-- Журнал позиции и сборка дневного расхода (§10.1).
CREATE INDEX stock_movements_item_occurred_idx
    ON stock_movements (tenant_id, item_id, occurred_at DESC);
-- Журнал с фильтрами по периоду и автору (FR-11).
CREATE INDEX stock_movements_tenant_occurred_idx
    ON stock_movements (tenant_id, occurred_at DESC, id DESC);
CREATE INDEX stock_movements_order_idx ON stock_movements (tenant_id, order_id)
    WHERE order_id IS NOT NULL;

-- Идемпотентность: повтор формы с тем же ключом не создаёт второе движение (FR-10).
CREATE UNIQUE INDEX stock_movements_idempotency_idx
    ON stock_movements (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Сторно возможно ровно один раз (FR-7).
CREATE UNIQUE INDEX stock_movements_reverses_idx
    ON stock_movements (tenant_id, reverses_id)
    WHERE reverses_id IS NOT NULL;

-- Кэш журнала: дашборд не суммирует всю историю, а строка блокируется
-- при записи движения, чтобы параллельные списания не увели остаток в минус (§10.2).
CREATE TABLE stock_balances (
    tenant_id    uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    warehouse_id uuid NOT NULL,
    item_id      uuid NOT NULL,
    on_hand      numeric(14,3) NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (warehouse_id, item_id),
    CONSTRAINT stock_balances_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT stock_balances_warehouse_fk FOREIGN KEY (tenant_id, warehouse_id)
        REFERENCES warehouses (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX stock_balances_tenant_idx ON stock_balances (tenant_id, item_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stock_balances;
DROP TABLE IF EXISTS stock_movements;
DROP TABLE IF EXISTS stock_count_lines;
DROP TABLE IF EXISTS stock_counts;
DROP TYPE IF EXISTS count_status;
DROP TYPE IF EXISTS movement_type;
-- +goose StatementEnd
