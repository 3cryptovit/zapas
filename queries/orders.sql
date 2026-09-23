-- Заказы поставщику и приёмка (§5.4).

-- name: CreatePurchaseOrder :one
INSERT INTO purchase_orders (id, tenant_id, supplier_id, note, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetPurchaseOrder :one
SELECT * FROM purchase_orders WHERE tenant_id = $1 AND id = $2;

-- name: GetPurchaseOrderForUpdate :one
-- Блокировка заказа: повторное нажатие «принять» не должно создать
-- второй приход (FR-20).
SELECT * FROM purchase_orders WHERE tenant_id = $1 AND id = $2 FOR UPDATE;

-- name: ListPurchaseOrders :many
SELECT
    o.*,
    s.name AS supplier_name
FROM purchase_orders o
JOIN suppliers s ON s.tenant_id = o.tenant_id AND s.id = o.supplier_id
WHERE o.tenant_id = $1
  AND (sqlc.narg(status)::purchase_order_status IS NULL OR o.status = sqlc.narg(status))
ORDER BY o.created_at DESC
LIMIT $2;

-- name: SendPurchaseOrder :exec
UPDATE purchase_orders
SET status = 'sent', sent_at = $3, expected_at = $4
WHERE tenant_id = $1 AND id = $2 AND status = 'draft';

-- name: ReceivePurchaseOrder :exec
UPDATE purchase_orders
SET status = 'received', received_at = $3
WHERE tenant_id = $1 AND id = $2 AND status = 'sent';

-- name: CancelPurchaseOrder :exec
UPDATE purchase_orders
SET status = 'cancelled', cancelled_at = $3
WHERE tenant_id = $1 AND id = $2 AND status IN ('draft', 'sent');

-- name: MarkOrderLateNotified :exec
UPDATE purchase_orders SET late_notified_at = $3 WHERE tenant_id = $1 AND id = $2;

-- name: ListLateOrders :many
-- Опоздания: к концу ожидаемого дня заказ не принят (FR-21).
SELECT o.*, s.name AS supplier_name
FROM purchase_orders o
JOIN suppliers s ON s.tenant_id = o.tenant_id AND s.id = o.supplier_id
WHERE o.tenant_id = $1
  AND o.status = 'sent'
  AND o.expected_at < sqlc.arg(today)
  AND o.late_notified_at IS NULL;

-- name: UpsertPurchaseOrderLine :exec
INSERT INTO purchase_order_lines (
    tenant_id, order_id, item_id, qty_ordered, purchase_unit, unit_factor, price
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (order_id, item_id) DO UPDATE SET
    qty_ordered   = EXCLUDED.qty_ordered,
    purchase_unit = EXCLUDED.purchase_unit,
    unit_factor   = EXCLUDED.unit_factor,
    price         = EXCLUDED.price;

-- name: SetPurchaseOrderLineReceived :exec
UPDATE purchase_order_lines
SET qty_received = $4
WHERE tenant_id = $1 AND order_id = $2 AND item_id = $3;

-- name: DeletePurchaseOrderLine :exec
DELETE FROM purchase_order_lines
WHERE tenant_id = $1 AND order_id = $2 AND item_id = $3;

-- name: ListPurchaseOrderLines :many
SELECT
    l.*,
    i.name AS item_name,
    i.base_unit
FROM purchase_order_lines l
JOIN items i ON i.tenant_id = l.tenant_id AND i.id = l.item_id
WHERE l.tenant_id = $1 AND l.order_id = $2
ORDER BY i.name;

-- name: ListIncomingByItem :many
-- «В пути»: количества по отправленным заказам с ожидаемой датой.
-- Учитываются только заказы со статусом sent (§10.1, частичный индекс).
SELECT
    l.item_id,
    o.expected_at,
    sum(l.qty_ordered)::numeric AS qty
FROM purchase_order_lines l
JOIN purchase_orders o ON o.tenant_id = l.tenant_id AND o.id = l.order_id
WHERE l.tenant_id = $1 AND o.status = 'sent'
GROUP BY l.item_id, o.expected_at;

-- name: ListIncomingForItem :many
SELECT
    o.expected_at,
    sum(l.qty_ordered)::numeric AS qty
FROM purchase_order_lines l
JOIN purchase_orders o ON o.tenant_id = l.tenant_id AND o.id = l.order_id
WHERE l.tenant_id = $1 AND l.item_id = $2 AND o.status = 'sent'
GROUP BY o.expected_at;

-- name: HasOpenOrderForSupplier :one
-- Напоминание до отсечки не шлётся, если заказ уже отправлен (§5.5).
SELECT EXISTS (
    SELECT 1 FROM purchase_orders
    WHERE tenant_id = $1 AND supplier_id = $2 AND status = 'sent' AND sent_at >= $3
) AS exists;
