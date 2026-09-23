-- +goose Up
-- +goose StatementBegin

CREATE TABLE categories (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(btrim(name)) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT categories_tenant_id_unique UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX categories_tenant_name_idx ON categories (tenant_id, lower(name));

CREATE TABLE suppliers (
    id               uuid PRIMARY KEY,
    tenant_id        uuid     NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name             text     NOT NULL CHECK (length(btrim(name)) > 0),
    contact          text     NOT NULL DEFAULT '',
    lead_time_days   smallint NOT NULL DEFAULT 1 CHECK (lead_time_days BETWEEN 0 AND 60),
    -- Дни доставки в нумерации ISO: пн = 1 … вс = 7.
    delivery_weekdays smallint[] NOT NULL DEFAULT '{1,2,3,4,5,6,7}',
    order_cutoff     time     NOT NULL DEFAULT '16:00',
    archived_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT suppliers_tenant_id_unique UNIQUE (tenant_id, id),
    CONSTRAINT suppliers_weekdays_valid CHECK (
        array_length(delivery_weekdays, 1) BETWEEN 1 AND 7
        AND delivery_weekdays <@ ARRAY[1,2,3,4,5,6,7]::smallint[]
    )
);

CREATE UNIQUE INDEX suppliers_tenant_name_idx ON suppliers (tenant_id, lower(name));

CREATE TABLE items (
    id                  uuid PRIMARY KEY,
    tenant_id           uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    category_id         uuid,
    name                text NOT NULL CHECK (length(btrim(name)) > 0),
    base_unit           text NOT NULL CHECK (base_unit IN ('kg', 'l', 'pcs')),
    default_supplier_id uuid,
    -- Уровень сервиса в процентах; z-фактор считает код (§5.2).
    service_level       smallint NOT NULL DEFAULT 95 CHECK (service_level IN (90, 95, 99)),
    -- Запасной вариант, пока прогноза нет (модель M0).
    manual_min_qty      numeric(14,3) NOT NULL DEFAULT 0 CHECK (manual_min_qty >= 0),
    archived_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT items_tenant_id_unique UNIQUE (tenant_id, id),
    -- Составные ссылки: позиция не может указать на категорию чужого тенанта.
    CONSTRAINT items_category_fk FOREIGN KEY (tenant_id, category_id)
        REFERENCES categories (tenant_id, id) ON DELETE SET NULL (category_id),
    CONSTRAINT items_supplier_fk FOREIGN KEY (tenant_id, default_supplier_id)
        REFERENCES suppliers (tenant_id, id) ON DELETE SET NULL (default_supplier_id)
);

CREATE UNIQUE INDEX items_tenant_name_idx ON items (tenant_id, lower(name));
CREATE INDEX items_tenant_category_idx ON items (tenant_id, category_id);
-- Дашборд и прогноз работают только с живыми позициями.
CREATE INDEX items_active_idx ON items (tenant_id) WHERE archived_at IS NULL;

-- Условия закупки: цена и упаковка у каждого поставщика свои.
CREATE TABLE supplier_items (
    tenant_id     uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    supplier_id   uuid NOT NULL,
    item_id       uuid NOT NULL,
    purchase_unit text NOT NULL,                       -- «кор.», «уп.», «шт»
    unit_factor   numeric(14,3) NOT NULL CHECK (unit_factor > 0),  -- коробка = 12 л
    min_order_qty numeric(14,3) NOT NULL DEFAULT 0 CHECK (min_order_qty >= 0),
    pack_multiple numeric(14,3) NOT NULL DEFAULT 0 CHECK (pack_multiple >= 0),
    price         numeric(14,2),
    PRIMARY KEY (supplier_id, item_id),
    CONSTRAINT supplier_items_supplier_fk FOREIGN KEY (tenant_id, supplier_id)
        REFERENCES suppliers (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT supplier_items_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX supplier_items_item_idx ON supplier_items (tenant_id, item_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS supplier_items;
DROP TABLE IF EXISTS items;
DROP TABLE IF EXISTS suppliers;
DROP TABLE IF EXISTS categories;
-- +goose StatementEnd
