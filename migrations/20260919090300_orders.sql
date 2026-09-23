-- +goose Up
-- +goose StatementBegin

CREATE TYPE purchase_order_status AS ENUM ('draft', 'sent', 'received', 'cancelled');

CREATE TABLE purchase_orders (
    id          uuid PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL,
    status      purchase_order_status NOT NULL DEFAULT 'draft',
    -- Дата поставки d1, зафиксированная при отправке заказа (§5.1).
    expected_at date,
    sent_at     timestamptz,
    received_at timestamptz,
    cancelled_at timestamptz,
    late_notified_at timestamptz,   -- чтобы алерт об опоздании ушёл один раз
    note        text NOT NULL DEFAULT '',
    created_by  uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT purchase_orders_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT purchase_orders_supplier_fk FOREIGN KEY (tenant_id, supplier_id)
        REFERENCES suppliers (tenant_id, id) ON DELETE RESTRICT,
    -- Статус и отметки времени не расходятся: «отправлен» без sent_at невозможен.
    CONSTRAINT purchase_orders_status_times CHECK (
        (status = 'draft'     AND sent_at IS NULL AND received_at IS NULL AND cancelled_at IS NULL)
     OR (status = 'sent'      AND sent_at IS NOT NULL AND received_at IS NULL AND cancelled_at IS NULL)
     OR (status = 'received'  AND sent_at IS NOT NULL AND received_at IS NOT NULL)
     OR (status = 'cancelled' AND cancelled_at IS NOT NULL)
    )
);

-- «В пути» считается только по отправленным заказам (§10.1).
CREATE INDEX purchase_orders_open_idx ON purchase_orders (tenant_id, supplier_id)
    WHERE status = 'sent';
CREATE INDEX purchase_orders_expected_idx ON purchase_orders (expected_at)
    WHERE status = 'sent';
CREATE INDEX purchase_orders_tenant_created_idx ON purchase_orders (tenant_id, created_at DESC);

CREATE TABLE purchase_order_lines (
    tenant_id    uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    order_id     uuid NOT NULL,
    item_id      uuid NOT NULL,
    -- Количества в базовой единице; единица закупки — представление для владельца.
    qty_ordered  numeric(14,3) NOT NULL CHECK (qty_ordered > 0),
    qty_received numeric(14,3) CHECK (qty_received IS NULL OR qty_received >= 0),
    purchase_unit text NOT NULL DEFAULT '',
    unit_factor  numeric(14,3) NOT NULL DEFAULT 1 CHECK (unit_factor > 0),
    price        numeric(14,2),
    PRIMARY KEY (order_id, item_id),
    CONSTRAINT purchase_order_lines_order_fk FOREIGN KEY (tenant_id, order_id)
        REFERENCES purchase_orders (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT purchase_order_lines_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX purchase_order_lines_item_idx ON purchase_order_lines (tenant_id, item_id);

-- Теперь, когда заказы существуют, движение может сослаться на свой заказ.
ALTER TABLE stock_movements
    ADD CONSTRAINT stock_movements_order_fk FOREIGN KEY (tenant_id, order_id)
        REFERENCES purchase_orders (tenant_id, id) ON DELETE SET NULL (order_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE stock_movements DROP CONSTRAINT IF EXISTS stock_movements_order_fk;
DROP TABLE IF EXISTS purchase_order_lines;
DROP TABLE IF EXISTS purchase_orders;
DROP TYPE IF EXISTS purchase_order_status;
-- +goose StatementEnd
