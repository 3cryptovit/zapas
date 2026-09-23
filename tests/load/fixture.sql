-- Нагрузочная фикстура: 300 позиций и миллион движений (§12).
--
-- Организация и владелец создаются командой seed — там argon2id-хеш
-- пароля, который в SQL не подделать:
--
--   app seed -email perf@zapas.local -password 'perf-пароль-42' -name 'Нагрузка'
--   psql -d zapas -v ON_ERROR_STOP=1 -f tests/load/fixture.sql
--
-- Скрипт идемпотентен: повторный запуск ничего не удваивает.
--
-- Выполнять только от роли, обходящей RLS (postgres или zapas_maint):
-- app.tenant_id здесь не выставлен намеренно, фикстура работает
-- сразу со всей организацией.

\set ON_ERROR_STOP on
\timing on

BEGIN;

-- Сколько чего генерировать. Миллион движений на 300 позиций — это
-- примерно 3300 записей на позицию, то есть года три работы кофейни.
\set n_items 300
\set n_movements 1000000

DO $fixture$
DECLARE
    v_tenant    uuid;
    v_warehouse uuid;
    v_owner     uuid;
    v_supplier  uuid;
    v_category  uuid;
    v_items     int := 300;
    v_movements int := 1000000;
    v_existing  bigint;
    v_per_item  int;
BEGIN
    SELECT id INTO v_tenant FROM tenants WHERE name = 'Нагрузка';
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'организации «Нагрузка» нет — сначала выполните app seed';
    END IF;

    SELECT id INTO v_warehouse FROM warehouses WHERE tenant_id = v_tenant LIMIT 1;
    SELECT id INTO v_owner FROM users WHERE tenant_id = v_tenant LIMIT 1;

    SELECT count(*) INTO v_existing FROM stock_movements WHERE tenant_id = v_tenant;
    IF v_existing > 0 THEN
        RAISE NOTICE 'фикстура уже наполнена: % движений, выхожу', v_existing;
        RETURN;
    END IF;

    -- Справочники.
    INSERT INTO categories (id, tenant_id, name)
    VALUES (gen_random_uuid(), v_tenant, 'Нагрузочная')
    RETURNING id INTO v_category;

    INSERT INTO suppliers (id, tenant_id, name, contact, lead_time_days,
                           delivery_weekdays, order_cutoff)
    VALUES (gen_random_uuid(), v_tenant, 'Нагрузочный поставщик', '@perf',
            1, ARRAY[1,2,3,4,5]::smallint[], '16:00')
    RETURNING id INTO v_supplier;

    -- Позиции.
    INSERT INTO items (id, tenant_id, category_id, name, base_unit,
                       default_supplier_id, service_level, manual_min_qty)
    SELECT gen_random_uuid(), v_tenant, v_category,
           'Позиция ' || lpad(i::text, 3, '0'),
           (ARRAY['kg','l','pcs'])[1 + (i % 3)],
           v_supplier, 95, 0
    FROM generate_series(1, v_items) AS i;

    INSERT INTO supplier_items (tenant_id, supplier_id, item_id, purchase_unit,
                                unit_factor, min_order_qty, pack_multiple)
    SELECT v_tenant, v_supplier, id, 'уп.', 1, 0, 1
    FROM items WHERE tenant_id = v_tenant;

    v_per_item := ceil(v_movements::numeric / v_items);

    -- Начальные остатки: по одному на позицию, поэтому тип opening.
    INSERT INTO stock_movements (id, tenant_id, warehouse_id, item_id, type,
                                 qty, occurred_at, created_by, comment)
    SELECT gen_random_uuid(), v_tenant, v_warehouse, id, 'opening',
           100000, now() - interval '3 years', v_owner, 'фикстура'
    FROM items WHERE tenant_id = v_tenant;

    -- Расход. Знак отрицательный — этого требует ограничение на тип.
    -- Остаток при этом не уходит в минус: начальные 100000 покрывают
    -- 3334 списания по единице с запасом.
    INSERT INTO stock_movements (id, tenant_id, warehouse_id, item_id, type,
                                 qty, occurred_at, created_by, comment)
    SELECT gen_random_uuid(), v_tenant, v_warehouse, it.id, 'usage',
           -(1 + (g % 9))::numeric,
           now() - make_interval(mins => g * 13),
           v_owner, ''
    FROM items it
    CROSS JOIN LATERAL generate_series(1, v_per_item) AS g
    WHERE it.tenant_id = v_tenant;

    -- Остатки считаем из журнала, а не придумываем: они обязаны сойтись.
    INSERT INTO stock_balances (tenant_id, warehouse_id, item_id, on_hand, updated_at)
    SELECT v_tenant, v_warehouse, item_id, sum(qty), now()
    FROM stock_movements WHERE tenant_id = v_tenant
    GROUP BY item_id;

    -- Дашборд джойнит прогноз и статус. Без них запрос получается
    -- легче настоящего, и замер врёт в свою пользу.
    INSERT INTO daily_consumption (tenant_id, item_id, day, qty, has_data, stockout)
    SELECT v_tenant, it.id, (current_date - d)::date,
           (5 + (d % 7))::numeric, true, false
    FROM items it
    CROSS JOIN generate_series(1, 120) AS d
    WHERE it.tenant_id = v_tenant;

    INSERT INTO forecasts (tenant_id, item_id, day, qty, model)
    SELECT v_tenant, it.id, (current_date + d)::date,
           (5 + (d % 7))::numeric, 'M1'
    FROM items it
    CROSS JOIN generate_series(0, 13) AS d
    WHERE it.tenant_id = v_tenant;

    INSERT INTO forecast_metrics (tenant_id, item_id, model, wape, bias, sigma,
                                  window_days, days_with_data)
    SELECT v_tenant, id, 'M1', 12.5, 0.4, 1.8, 28, 28
    FROM items WHERE tenant_id = v_tenant;

    INSERT INTO item_status (tenant_id, item_id, status, on_order, stockout_date,
                             recommended_qty, target_level, safety_stock, supplier_id)
    SELECT v_tenant, id,
           (ARRAY['ok','order_today','critical'])[1 + (row_number() OVER () % 3)]::item_status_code,
           0, current_date + 5, 12, 40, 8, v_supplier
    FROM items WHERE tenant_id = v_tenant;

    RAISE NOTICE 'позиций: %, движений: %', v_items,
        (SELECT count(*) FROM stock_movements WHERE tenant_id = v_tenant);
END
$fixture$;

COMMIT;
