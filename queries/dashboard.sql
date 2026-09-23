-- Дашборд собирается одним запросом (§9.2, исключение из правила
-- «не ходить в чужие таблицы»): экран должен отвечать за 300 мс на 300 позициях.

-- name: Dashboard :many
SELECT
    i.id                                   AS item_id,
    i.name                                 AS item_name,
    i.base_unit,
    i.manual_min_qty,
    c.name                                 AS category_name,
    COALESCE(b.on_hand, 0)::numeric        AS on_hand,
    COALESCE(st.on_order, 0)::numeric      AS on_order,
    COALESCE(st.status, 'no_forecast')     AS status,
    st.stockout_date,
    st.order_by,
    COALESCE(st.recommended_qty, 0)::numeric AS recommended_qty,
    COALESCE(st.target_level, 0)::numeric    AS target_level,
    s.id                                   AS supplier_id,
    s.name                                 AS supplier_name,
    s.order_cutoff,
    COALESCE(si.purchase_unit, '')         AS purchase_unit,
    COALESCE(si.unit_factor, 1)::numeric   AS unit_factor,
    fm.model                               AS forecast_model,
    COALESCE(fm.wape, 1)::double precision AS wape,
    b.updated_at                           AS balance_updated_at
FROM items i
LEFT JOIN categories c      ON c.tenant_id = i.tenant_id AND c.id = i.category_id
LEFT JOIN stock_balances b  ON b.tenant_id = i.tenant_id AND b.item_id = i.id
LEFT JOIN item_status st    ON st.tenant_id = i.tenant_id AND st.item_id = i.id
LEFT JOIN suppliers s       ON s.tenant_id = i.tenant_id AND s.id = i.default_supplier_id
LEFT JOIN supplier_items si ON si.tenant_id = i.tenant_id
                           AND si.item_id = i.id
                           AND si.supplier_id = i.default_supplier_id
LEFT JOIN forecast_metrics fm ON fm.tenant_id = i.tenant_id AND fm.item_id = i.id
WHERE i.tenant_id = $1 AND i.archived_at IS NULL
ORDER BY
    -- Красные сверху, дальше по дате «хватит до» (§6.1).
    CASE COALESCE(st.status, 'no_forecast')
        WHEN 'out_of_stock' THEN 0
        WHEN 'critical'     THEN 1
        WHEN 'order_today'  THEN 2
        WHEN 'no_forecast'  THEN 3
        ELSE 4
    END,
    st.stockout_date NULLS LAST,
    i.name;

-- name: ItemInsights :one
-- Карточка позиции: параметры, статус, объяснение и точность (§6.2).
SELECT
    i.id           AS item_id,
    i.name         AS item_name,
    i.base_unit,
    i.service_level,
    i.manual_min_qty,
    c.name         AS category_name,
    COALESCE(b.on_hand, 0)::numeric   AS on_hand,
    COALESCE(st.on_order, 0)::numeric AS on_order,
    COALESCE(st.status, 'no_forecast') AS status,
    st.stockout_date,
    st.order_by,
    COALESCE(st.recommended_qty, 0)::numeric AS recommended_qty,
    COALESCE(st.target_level, 0)::numeric    AS target_level,
    COALESCE(st.safety_stock, 0)::numeric    AS safety_stock,
    COALESCE(st.explanation, '{}'::jsonb)    AS explanation,
    s.id           AS supplier_id,
    s.name         AS supplier_name,
    COALESCE(si.purchase_unit, '')       AS purchase_unit,
    COALESCE(si.unit_factor, 1)::numeric AS unit_factor,
    fm.model       AS forecast_model,
    COALESCE(fm.wape, 1)::double precision  AS wape,
    COALESCE(fm.bias, 0)::double precision  AS bias,
    COALESCE(fm.sigma, 0)::double precision AS sigma,
    COALESCE(fm.days_with_data, 0)          AS days_with_data
FROM items i
LEFT JOIN categories c      ON c.tenant_id = i.tenant_id AND c.id = i.category_id
LEFT JOIN stock_balances b  ON b.tenant_id = i.tenant_id AND b.item_id = i.id
LEFT JOIN item_status st    ON st.tenant_id = i.tenant_id AND st.item_id = i.id
LEFT JOIN suppliers s       ON s.tenant_id = i.tenant_id AND s.id = i.default_supplier_id
LEFT JOIN supplier_items si ON si.tenant_id = i.tenant_id
                           AND si.item_id = i.id
                           AND si.supplier_id = i.default_supplier_id
LEFT JOIN forecast_metrics fm ON fm.tenant_id = i.tenant_id AND fm.item_id = i.id
WHERE i.tenant_id = $1 AND i.id = $2;

-- name: ItemHistory :many
-- График спроса: фактический расход за N дней (§6.2).
SELECT day, qty, has_data, stockout
FROM daily_consumption
WHERE tenant_id = $1 AND item_id = $2 AND day >= $3
ORDER BY day;

-- name: Suggestions :many
-- Блок «Заказать сегодня», сгруппированный по поставщикам (§6).
SELECT
    s.id                                  AS supplier_id,
    s.name                                AS supplier_name,
    s.contact,
    s.order_cutoff,
    i.id                                  AS item_id,
    i.name                                AS item_name,
    i.base_unit,
    COALESCE(b.on_hand, 0)::numeric       AS on_hand,
    st.status,
    st.stockout_date,
    st.order_by,
    st.recommended_qty,
    COALESCE(si.purchase_unit, '')        AS purchase_unit,
    COALESCE(si.unit_factor, 1)::numeric  AS unit_factor,
    si.price
FROM item_status st
JOIN items i     ON i.tenant_id = st.tenant_id AND i.id = st.item_id
JOIN suppliers s ON s.tenant_id = st.tenant_id AND s.id = st.supplier_id
LEFT JOIN stock_balances b  ON b.tenant_id = st.tenant_id AND b.item_id = st.item_id
LEFT JOIN supplier_items si ON si.tenant_id = st.tenant_id
                           AND si.item_id = st.item_id
                           AND si.supplier_id = st.supplier_id
WHERE st.tenant_id = $1
  AND st.status IN ('order_today', 'critical', 'out_of_stock')
  AND st.recommended_qty > 0
  AND i.archived_at IS NULL
ORDER BY s.name, i.name;
