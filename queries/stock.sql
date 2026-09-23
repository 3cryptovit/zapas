-- Движения, остатки и инвентаризация (§3.3–3.5).
--
-- stock_movements только дописывается: права на UPDATE и DELETE у роли
-- приложения отозваны (ADR-002). Исправление ошибки — сторно.

-- name: GetMovementByIdempotencyKey :one
-- Шаг 1 записи движения: повтор формы с тем же ключом возвращает уже
-- созданное движение и не создаёт второго (FR-10).
SELECT * FROM stock_movements
WHERE tenant_id = $1 AND idempotency_key = $2;

-- name: GetMovement :one
SELECT * FROM stock_movements WHERE tenant_id = $1 AND id = $2;

-- name: LockBalance :one
-- Шаг 2: блокируем строку остатка. Конкурентные списания одной позиции идут
-- по очереди, поэтому проверка «не уйдём ли в минус» честная (§10.2).
SELECT on_hand FROM stock_balances
WHERE warehouse_id = $1 AND item_id = $2
FOR UPDATE;

-- name: EnsureBalance :exec
-- Строка остатка заводится при создании позиции; здесь — страховка на случай,
-- когда позиция появилась в обход обычного пути (импорт, генератор).
INSERT INTO stock_balances (tenant_id, warehouse_id, item_id, on_hand)
VALUES ($1, $2, $3, 0)
ON CONFLICT (warehouse_id, item_id) DO NOTHING;

-- name: InsertMovement :one
INSERT INTO stock_movements (
    id, tenant_id, warehouse_id, item_id, type, qty,
    occurred_at, created_by, reason, comment,
    order_id, count_id, reverses_id, idempotency_key
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: ApplyMovementToBalance :one
-- Шаг 3: остаток меняется в той же транзакции, что и движение.
-- CHECK (on_hand >= 0) — последний рубеж, если проверка в коде даст сбой.
UPDATE stock_balances
SET on_hand = on_hand + sqlc.arg(delta)::numeric, updated_at = sqlc.arg(now)
WHERE warehouse_id = $1 AND item_id = $2
RETURNING on_hand;

-- name: GetBalance :one
SELECT * FROM stock_balances WHERE tenant_id = $1 AND item_id = $2;

-- name: ListBalances :many
SELECT * FROM stock_balances WHERE tenant_id = $1;

-- name: ListMovements :many
-- Журнал с фильтрами и курсорной пагинацией (FR-11).
-- Курсор — пара (occurred_at, id): id разводит движения одной секунды.
SELECT
    m.*,
    i.name  AS item_name,
    i.base_unit,
    u.name  AS author_name
FROM stock_movements m
JOIN items i     ON i.tenant_id = m.tenant_id AND i.id = m.item_id
LEFT JOIN users u ON u.tenant_id = m.tenant_id AND u.id = m.created_by
WHERE m.tenant_id = $1
  AND (sqlc.narg(item_id)::uuid IS NULL OR m.item_id = sqlc.narg(item_id))
  AND (sqlc.narg(movement_type)::movement_type IS NULL OR m.type = sqlc.narg(movement_type))
  AND (sqlc.narg(author_id)::uuid IS NULL OR m.created_by = sqlc.narg(author_id))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR m.occurred_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR m.occurred_at < sqlc.narg(to_at))
  AND (
      sqlc.narg(cursor_at)::timestamptz IS NULL
      OR (m.occurred_at, m.id) < (sqlc.narg(cursor_at), sqlc.narg(cursor_id)::uuid)
  )
ORDER BY m.occurred_at DESC, m.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetReversalOf :one
-- Сторно возможно один раз (FR-7): проверяем, не сторнировали ли уже.
SELECT * FROM stock_movements
WHERE tenant_id = $1 AND reverses_id = $2;

-- name: SumMovementsByItem :many
-- Ночная сверка (FR-17): остаток обязан совпадать с суммой движений.
SELECT item_id, COALESCE(sum(qty), 0)::numeric AS total
FROM stock_movements
WHERE tenant_id = $1
GROUP BY item_id;

-- name: ListDailyConsumptionSource :many
-- Сборка дневного расхода (§4.1): расход, списания и корректировки с обратным
-- знаком, в календарных днях часового пояса тенанта.
-- Сторнированные движения и сами сторно из ряда исключаются.
SELECT
    m.item_id,
    (m.occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day,
    (-sum(m.qty))::numeric AS qty
FROM stock_movements m
WHERE m.tenant_id = $1
  AND m.occurred_at >= $2
  AND m.type IN ('usage', 'writeoff', 'adjustment')
  AND m.reverses_id IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM stock_movements r
      WHERE r.tenant_id = m.tenant_id AND r.reverses_id = m.id
  )
GROUP BY m.item_id, day;

-- name: ListDaysWithActivity :many
-- «Нет данных ≠ ноль»: день считается «с данными», если в тенанте был хотя бы
-- один расход или пересчёт (§4.1).
SELECT DISTINCT (occurred_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day
FROM stock_movements
WHERE tenant_id = $1
  AND occurred_at >= $2
  AND type IN ('usage', 'writeoff', 'adjustment');

-- name: UpsertDailyConsumption :exec
INSERT INTO daily_consumption (tenant_id, item_id, day, qty, has_data, stockout)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (item_id, day) DO UPDATE SET
    qty      = EXCLUDED.qty,
    has_data = EXCLUDED.has_data,
    stockout = EXCLUDED.stockout;

-- name: ListDailyConsumption :many
SELECT * FROM daily_consumption
WHERE tenant_id = $1 AND item_id = $2 AND day >= $3
ORDER BY day;

-- name: DeleteDailyConsumptionBefore :execrows
DELETE FROM daily_consumption WHERE tenant_id = $1 AND day < $2;

-- --- инвентаризация ---

-- name: CreateStockCount :one
INSERT INTO stock_counts (id, tenant_id, warehouse_id, scope, note, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetStockCount :one
SELECT * FROM stock_counts WHERE tenant_id = $1 AND id = $2;

-- name: GetStockCountForUpdate :one
-- Блокировка документа: повторное нажатие «провести» не должно создать
-- вторую пачку корректировок.
SELECT * FROM stock_counts WHERE tenant_id = $1 AND id = $2 FOR UPDATE;

-- name: ListStockCounts :many
SELECT * FROM stock_counts WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: PostStockCount :exec
UPDATE stock_counts SET status = 'posted', posted_at = $3, posted_by = $4
WHERE tenant_id = $1 AND id = $2;

-- name: UpsertStockCountLine :exec
INSERT INTO stock_count_lines (tenant_id, count_id, item_id, expected_qty, counted_qty)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (count_id, item_id) DO UPDATE SET
    expected_qty = EXCLUDED.expected_qty,
    counted_qty  = EXCLUDED.counted_qty;

-- name: ListStockCountLines :many
-- Расхождение показывается до проведения: сравниваем внесённый факт
-- с учётом на момент открытия документа и с текущим остатком (FR-13).
SELECT
    l.*,
    i.name AS item_name,
    i.base_unit,
    COALESCE(b.on_hand, 0)::numeric AS current_qty
FROM stock_count_lines l
JOIN items i ON i.tenant_id = l.tenant_id AND i.id = l.item_id
LEFT JOIN stock_balances b ON b.tenant_id = l.tenant_id AND b.item_id = l.item_id
WHERE l.tenant_id = $1 AND l.count_id = $2
ORDER BY i.name;

-- name: ListCountedLines :many
-- При проведении берутся только строки с внесённым фактом.
SELECT * FROM stock_count_lines
WHERE tenant_id = $1 AND count_id = $2 AND counted_qty IS NOT NULL;

-- name: BulkInsertMovements :execrows
-- Пакетная вставка истории для генератора песочницы (§7.1).
--
-- В ТЗ на этом месте COPY, но PostgreSQL не поддерживает COPY FROM для
-- таблиц с row-level security (ADR-005). Разворот массивов даёт тот же
-- один заход в базу и сохраняет RLS: политика проверяется как на INSERT.
INSERT INTO stock_movements (
    id, tenant_id, warehouse_id, item_id, type, qty,
    occurred_at, created_by, reason, comment
)
SELECT
    m.id,
    sqlc.arg(tenant_id)::uuid,
    sqlc.arg(warehouse_id)::uuid,
    m.item_id,
    m.type,
    m.qty,
    m.occurred_at,
    sqlc.arg(created_by)::uuid,
    -- Пустая строка означает «причины нет»: text[] не хранит NULL удобно.
    nullif(m.reason, ''),
    m.comment
FROM (
    SELECT
        unnest(sqlc.arg(ids)::uuid[])            AS id,
        unnest(sqlc.arg(item_ids)::uuid[])       AS item_id,
        -- Типы идут как text[]: pgx не кодирует срез пользовательского
        -- enum-типа, а приведение делает база.
        unnest(sqlc.arg(types)::text[])::movement_type AS type,
        unnest(sqlc.arg(quantities)::numeric[])  AS qty,
        unnest(sqlc.arg(occurred_ats)::timestamptz[]) AS occurred_at,
        unnest(sqlc.arg(reasons)::text[])        AS reason,
        unnest(sqlc.arg(comments)::text[])       AS comment
) AS m;
