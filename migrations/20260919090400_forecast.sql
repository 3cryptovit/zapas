-- +goose Up
-- +goose StatementBegin

-- Дневной расход позиции — вход прогноза (§4.1). Пересобирается ночной задачей.
CREATE TABLE daily_consumption (
    tenant_id uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    item_id   uuid NOT NULL,
    day       date NOT NULL,             -- день в часовом поясе тенанта
    qty       numeric(14,3) NOT NULL DEFAULT 0,
    -- «Нет данных ≠ ноль»: день без движений в ряд не попадает.
    has_data  boolean NOT NULL DEFAULT true,
    -- День дефицита: остаток доходил до нуля, спрос был выше учтённого.
    stockout  boolean NOT NULL DEFAULT false,
    PRIMARY KEY (item_id, day),
    CONSTRAINT daily_consumption_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX daily_consumption_tenant_day_idx ON daily_consumption (tenant_id, day);

CREATE TYPE forecast_model AS ENUM (
    'M0', -- ручной минимум: меньше 7 дней данных
    'M1', -- среднее по дню недели
    'M2'  -- экспоненциальное сглаживание с сезонностью
);

-- Прогноз на 14 дней вперёд; хранится только последний расчёт.
CREATE TABLE forecasts (
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    item_id     uuid NOT NULL,
    day         date NOT NULL,
    qty         numeric(14,3) NOT NULL CHECK (qty >= 0),
    model       forecast_model NOT NULL,
    computed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id, day),
    CONSTRAINT forecasts_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX forecasts_tenant_day_idx ON forecasts (tenant_id, day);

-- Метрики бэктеста: точность в карточке и σ для страхового запаса (§4.3, §5.2).
CREATE TABLE forecast_metrics (
    tenant_id   uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    item_id     uuid NOT NULL,
    model       forecast_model NOT NULL,
    wape        double precision NOT NULL DEFAULT 0,
    bias        double precision NOT NULL DEFAULT 0,
    sigma       double precision NOT NULL DEFAULT 0,
    alpha       double precision,        -- подобранный α для M2
    window_days smallint NOT NULL DEFAULT 0,
    days_with_data smallint NOT NULL DEFAULT 0,
    computed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id),
    CONSTRAINT forecast_metrics_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

CREATE TYPE item_status_code AS ENUM (
    'out_of_stock',  -- красный: on_hand = 0
    'critical',      -- красный: закончится раньше ближайшей поставки d1
    'order_today',   -- жёлтый: IP < S
    'ok',            -- зелёный
    'no_forecast'    -- серый: модель M0
);

-- Read-модель дашборда: пересчитывается в транзакции движения и ночью (§5.3).
CREATE TABLE item_status (
    tenant_id       uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    item_id         uuid NOT NULL,
    status          item_status_code NOT NULL DEFAULT 'no_forecast',
    on_order        numeric(14,3) NOT NULL DEFAULT 0,
    stockout_date   date,                  -- «хватит до»
    order_by        timestamptz,           -- дедлайн отсечки у поставщика
    recommended_qty numeric(14,3) NOT NULL DEFAULT 0,
    target_level    numeric(14,3) NOT NULL DEFAULT 0,
    safety_stock    numeric(14,3) NOT NULL DEFAULT 0,
    supplier_id     uuid,
    -- «Почему такой статус»: расчёт словами и числами для карточки (§6.2).
    explanation     jsonb NOT NULL DEFAULT '{}'::jsonb,
    computed_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (item_id),
    CONSTRAINT item_status_item_fk FOREIGN KEY (tenant_id, item_id)
        REFERENCES items (tenant_id, id) ON DELETE CASCADE
);

-- Сортировка дашборда: сначала красные, потом по дате «хватит до».
CREATE INDEX item_status_tenant_status_idx ON item_status (tenant_id, status, stockout_date);
-- Блок «Заказать сегодня» группируется по поставщику.
CREATE INDEX item_status_order_today_idx ON item_status (tenant_id, supplier_id)
    WHERE status IN ('order_today', 'critical', 'out_of_stock');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS item_status;
DROP TYPE IF EXISTS item_status_code;
DROP TABLE IF EXISTS forecast_metrics;
DROP TABLE IF EXISTS forecasts;
DROP TYPE IF EXISTS forecast_model;
DROP TABLE IF EXISTS daily_consumption;
-- +goose StatementEnd
