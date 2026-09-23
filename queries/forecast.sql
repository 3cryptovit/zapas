-- Прогноз, метрики и read-модель статусов (§4, §5.3).

-- name: DeleteForecastsFrom :execrows
-- Ночной пересчёт идемпотентен: старый горизонт сносится целиком,
-- потом пишется новый (§4.4).
DELETE FROM forecasts WHERE tenant_id = $1 AND item_id = $2 AND day >= $3;

-- name: UpsertForecast :exec
INSERT INTO forecasts (tenant_id, item_id, day, qty, model, computed_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (item_id, day) DO UPDATE SET
    qty         = EXCLUDED.qty,
    model       = EXCLUDED.model,
    computed_at = EXCLUDED.computed_at;

-- name: ListForecastForItem :many
SELECT day, qty, model FROM forecasts
WHERE tenant_id = $1 AND item_id = $2 AND day >= $3
ORDER BY day;

-- name: ListForecastsFrom :many
-- Пакетная версия для ночного пересчёта статусов: один запрос на тенанта
-- вместо запроса на позицию.
SELECT item_id, day, qty, model FROM forecasts
WHERE tenant_id = $1 AND day >= $2
ORDER BY item_id, day;

-- name: UpsertForecastMetrics :exec
INSERT INTO forecast_metrics (
    tenant_id, item_id, model, wape, bias, sigma, alpha,
    window_days, days_with_data, computed_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (item_id) DO UPDATE SET
    model          = EXCLUDED.model,
    wape           = EXCLUDED.wape,
    bias           = EXCLUDED.bias,
    sigma          = EXCLUDED.sigma,
    alpha          = EXCLUDED.alpha,
    window_days    = EXCLUDED.window_days,
    days_with_data = EXCLUDED.days_with_data,
    computed_at    = EXCLUDED.computed_at;

-- name: GetForecastMetrics :one
SELECT * FROM forecast_metrics WHERE tenant_id = $1 AND item_id = $2;

-- name: ListForecastMetrics :many
SELECT * FROM forecast_metrics WHERE tenant_id = $1;

-- name: UpsertItemStatus :exec
INSERT INTO item_status (
    tenant_id, item_id, status, on_order, stockout_date, order_by,
    recommended_qty, target_level, safety_stock, supplier_id,
    explanation, computed_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (item_id) DO UPDATE SET
    status          = EXCLUDED.status,
    on_order        = EXCLUDED.on_order,
    stockout_date   = EXCLUDED.stockout_date,
    order_by        = EXCLUDED.order_by,
    recommended_qty = EXCLUDED.recommended_qty,
    target_level    = EXCLUDED.target_level,
    safety_stock    = EXCLUDED.safety_stock,
    supplier_id     = EXCLUDED.supplier_id,
    explanation     = EXCLUDED.explanation,
    computed_at     = EXCLUDED.computed_at;

-- name: GetItemStatus :one
SELECT * FROM item_status WHERE tenant_id = $1 AND item_id = $2;

-- name: ListItemStatuses :many
SELECT * FROM item_status WHERE tenant_id = $1;

-- name: GetItemForStatus :one
-- Всё, что нужно расчёту статуса одной позиции, одной строкой:
-- параметры позиции, условия поставщика, остаток и σ из бэктеста.
SELECT
    i.id                              AS item_id,
    i.name                            AS item_name,
    i.base_unit,
    i.service_level,
    i.manual_min_qty,
    COALESCE(b.on_hand, 0)::numeric   AS on_hand,
    s.id                              AS supplier_id,
    s.name                            AS supplier_name,
    s.lead_time_days,
    s.delivery_weekdays,
    s.order_cutoff,
    COALESCE(si.min_order_qty, 0)::numeric  AS min_order_qty,
    COALESCE(si.pack_multiple, 0)::numeric  AS pack_multiple,
    COALESCE(si.purchase_unit, '')          AS purchase_unit,
    COALESCE(si.unit_factor, 1)::numeric    AS unit_factor,
    COALESCE(fm.sigma, 0)::double precision AS sigma,
    fm.model                          AS metrics_model,
    COALESCE(fm.wape, 1)::double precision  AS wape,
    COALESCE(fm.bias, 0)::double precision  AS bias
FROM items i
LEFT JOIN stock_balances b ON b.tenant_id = i.tenant_id AND b.item_id = i.id
LEFT JOIN suppliers s      ON s.tenant_id = i.tenant_id AND s.id = i.default_supplier_id
LEFT JOIN supplier_items si ON si.tenant_id = i.tenant_id
                           AND si.item_id = i.id
                           AND si.supplier_id = i.default_supplier_id
LEFT JOIN forecast_metrics fm ON fm.tenant_id = i.tenant_id AND fm.item_id = i.id
WHERE i.tenant_id = $1 AND i.id = $2;

-- name: ListItemsForStatus :many
-- Пакетная версия для ночного пересчёта.
SELECT
    i.id                              AS item_id,
    i.name                            AS item_name,
    i.base_unit,
    i.service_level,
    i.manual_min_qty,
    COALESCE(b.on_hand, 0)::numeric   AS on_hand,
    s.id                              AS supplier_id,
    s.name                            AS supplier_name,
    s.lead_time_days,
    s.delivery_weekdays,
    s.order_cutoff,
    COALESCE(si.min_order_qty, 0)::numeric  AS min_order_qty,
    COALESCE(si.pack_multiple, 0)::numeric  AS pack_multiple,
    COALESCE(si.purchase_unit, '')          AS purchase_unit,
    COALESCE(si.unit_factor, 1)::numeric    AS unit_factor,
    COALESCE(fm.sigma, 0)::double precision AS sigma,
    fm.model                          AS metrics_model,
    COALESCE(fm.wape, 1)::double precision  AS wape,
    COALESCE(fm.bias, 0)::double precision  AS bias
FROM items i
LEFT JOIN stock_balances b ON b.tenant_id = i.tenant_id AND b.item_id = i.id
LEFT JOIN suppliers s      ON s.tenant_id = i.tenant_id AND s.id = i.default_supplier_id
LEFT JOIN supplier_items si ON si.tenant_id = i.tenant_id
                           AND si.item_id = i.id
                           AND si.supplier_id = i.default_supplier_id
LEFT JOIN forecast_metrics fm ON fm.tenant_id = i.tenant_id AND fm.item_id = i.id
WHERE i.tenant_id = $1 AND i.archived_at IS NULL
ORDER BY i.id;

-- name: SumConsumptionForDay :one
-- Сколько уже израсходовано сегодня: за сегодняшний день в целевой уровень
-- идёт только неизрасходованная часть прогноза (§5.2).
SELECT COALESCE(-sum(qty), 0)::numeric AS consumed
FROM stock_movements
WHERE tenant_id = $1
  AND item_id = $2
  AND type IN ('usage', 'writeoff', 'adjustment')
  AND (occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date = sqlc.arg(day)::date;

-- name: SumConsumptionByItemForDay :many
SELECT item_id, COALESCE(-sum(qty), 0)::numeric AS consumed
FROM stock_movements
WHERE tenant_id = $1
  AND type IN ('usage', 'writeoff', 'adjustment')
  AND (occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date = sqlc.arg(day)::date
GROUP BY item_id;

-- name: ListStockoutDays :many
-- Дни, когда остаток доходил до нуля: модель не должна учиться на заниженных
-- цифрах (§4.1). Остаток восстанавливается из журнала нарастающим итогом.
WITH running AS (
    SELECT
        item_id,
        (occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day,
        sum(qty) OVER (
            PARTITION BY item_id
            ORDER BY occurred_at, id
            ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
        ) AS balance
    FROM stock_movements
    WHERE tenant_id = $1 AND occurred_at < sqlc.arg(until)
)
SELECT item_id, day
FROM running
WHERE balance <= 0
GROUP BY item_id, day;

-- name: ListConsumptionMovementsForSeries :many
-- Сырые движения расхода для сборки ряда (§4.1).
--
-- Отдаются по одному, а не агрегатом по дням, потому что корректировки
-- из пересчёта нужно размазать по интервалу между пересчётами: в режиме
-- «только пересчёт» весь расход за несколько дней записан одним движением.
-- Сторнированные движения и сами сторно в ряд не попадают.
SELECT
    m.item_id,
    (m.occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day,
    m.qty,
    m.count_id
FROM stock_movements m
WHERE m.tenant_id = $1
  AND m.occurred_at >= sqlc.arg(since)
  AND m.occurred_at < sqlc.arg(until)
  AND m.type IN ('usage', 'writeoff', 'adjustment')
  AND m.reverses_id IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM stock_movements r
      WHERE r.tenant_id = m.tenant_id AND r.reverses_id = m.id
  )
ORDER BY m.item_id, m.occurred_at;

-- name: ListDaysWithUsage :many
-- День «с данными»: был хотя бы один расход или проведённый пересчёт (§4.1).
SELECT DISTINCT d.day FROM (
    SELECT (m.occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day
    FROM stock_movements m
    WHERE m.tenant_id = $1
      AND m.occurred_at >= sqlc.arg(since)
      AND m.type IN ('usage', 'writeoff', 'adjustment')
    UNION
    SELECT (c.posted_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day
    FROM stock_counts c
    WHERE c.tenant_id = $1 AND c.status = 'posted' AND c.posted_at >= sqlc.arg(since)
) d
ORDER BY d.day;

-- name: BulkUpsertDailyConsumption :execrows
-- Ночной пересчёт переписывает ряд целиком: у 40 позиций за 90 дней это
-- 3600 строк, и поштучные запросы не укладываются в бюджет задачи (§12).
INSERT INTO daily_consumption (tenant_id, item_id, day, qty, has_data, stockout)
SELECT
    sqlc.arg(tenant_id)::uuid,
    d.item_id, d.day, d.qty, d.has_data, d.stockout
FROM (
    SELECT
        unnest(sqlc.arg(item_ids)::uuid[])      AS item_id,
        unnest(sqlc.arg(days)::date[])          AS day,
        unnest(sqlc.arg(quantities)::numeric[]) AS qty,
        unnest(sqlc.arg(has_data)::boolean[])   AS has_data,
        unnest(sqlc.arg(stockouts)::boolean[])  AS stockout
) AS d
ON CONFLICT (item_id, day) DO UPDATE SET
    qty      = EXCLUDED.qty,
    has_data = EXCLUDED.has_data,
    stockout = EXCLUDED.stockout;

-- name: BulkUpsertForecasts :execrows
INSERT INTO forecasts (tenant_id, item_id, day, qty, model, computed_at)
SELECT
    sqlc.arg(tenant_id)::uuid,
    f.item_id, f.day, f.qty, f.model,
    sqlc.arg(computed_at)::timestamptz
FROM (
    SELECT
        unnest(sqlc.arg(item_ids)::uuid[])      AS item_id,
        unnest(sqlc.arg(days)::date[])          AS day,
        unnest(sqlc.arg(quantities)::numeric[]) AS qty,
        -- Модели идут как text[]: pgx не кодирует срез пользовательского
        -- enum-типа, приведение делает база.
        unnest(sqlc.arg(models)::text[])::forecast_model AS model
) AS f
ON CONFLICT (item_id, day) DO UPDATE SET
    qty         = EXCLUDED.qty,
    model       = EXCLUDED.model,
    computed_at = EXCLUDED.computed_at;

-- name: DeleteForecastsFromForItems :execrows
-- Снос старого горизонта у позиций без модели одним запросом.
DELETE FROM forecasts
WHERE tenant_id = $1
  AND day >= sqlc.arg(day)
  AND item_id = ANY(sqlc.arg(item_ids)::uuid[]);
