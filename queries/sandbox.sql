-- Пошаговое демо (§7): состояние шагов и защита наполнения.

-- name: GuideState :one
-- По одному числу на шаг. Шаг выполнен, когда в данных есть его след, а
-- не когда нажата кнопка: ручной путь и шаблон засчитываются одинаково.
SELECT
    (SELECT count(*) FROM suppliers s
      WHERE s.tenant_id = t.id AND s.archived_at IS NULL)::int AS suppliers,
    (SELECT count(*) FROM items i
      WHERE i.tenant_id = t.id AND i.archived_at IS NULL)::int AS items,
    -- Прогнозу нужна история: считаем разные дни с расходом.
    (SELECT count(DISTINCT (m.occurred_at AT TIME ZONE t.timezone)::date)
       FROM stock_movements m
      WHERE m.tenant_id = t.id AND m.type = 'usage')::int AS usage_days,
    (SELECT count(*) FROM stock_counts c
      WHERE c.tenant_id = t.id AND c.status = 'posted')::int AS posted_counts,
    (SELECT count(*) FROM forecast_metrics f
      WHERE f.tenant_id = t.id)::int AS forecasts,
    (SELECT count(*) FROM purchase_orders o
      WHERE o.tenant_id = t.id AND o.status IN ('sent', 'received'))::int AS sent_orders,
    (SELECT count(*) FROM purchase_orders o
      WHERE o.tenant_id = t.id AND o.status = 'received')::int AS received_orders,
    (extract(epoch FROM t.clock_offset) / 86400)::int AS virtual_days,
    (SELECT count(*) FROM notifications n
      WHERE n.tenant_id = t.id AND n.read_at IS NOT NULL)::int AS read_notifications
FROM tenants t
WHERE t.id = @tenant_id;

-- name: TryGuideLock :one
-- Сессионная блокировка на тенанта: два наполнения одного демо сразу
-- создали бы дубли. Снимается GuideUnlock на том же соединении.
SELECT pg_try_advisory_lock(hashtextextended('sandbox-guide:' || @tenant_id::text, 0))::boolean;

-- name: GuideUnlock :exec
SELECT pg_advisory_unlock(hashtextextended('sandbox-guide:' || @tenant_id::text, 0));

-- name: ItemsWithMovementsBefore :many
-- Позиции, у которых уже есть движения раньше указанного момента: историю
-- им генератор второй раз не дописывает.
SELECT DISTINCT item_id FROM stock_movements
WHERE tenant_id = @tenant_id AND occurred_at < @before;
