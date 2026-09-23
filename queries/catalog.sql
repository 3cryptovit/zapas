-- Номенклатура, категории и поставщики (§3.1, §3.2).
--
-- Везде есть tenant_id: RLS уже отсечёт чужое, но явный предикат нужен
-- планировщику — tenant_id стоит первым в составных индексах.

-- name: ListCategories :many
SELECT * FROM categories WHERE tenant_id = $1 ORDER BY name;

-- name: CreateCategory :one
INSERT INTO categories (id, tenant_id, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetCategoryByName :one
SELECT * FROM categories WHERE tenant_id = $1 AND lower(name) = lower($2);

-- name: ListSuppliers :many
SELECT * FROM suppliers
WHERE tenant_id = $1 AND (archived_at IS NULL OR sqlc.arg(include_archived)::boolean)
ORDER BY name;

-- name: GetSupplier :one
SELECT * FROM suppliers WHERE tenant_id = $1 AND id = $2;

-- name: GetSupplierByName :one
SELECT * FROM suppliers WHERE tenant_id = $1 AND lower(name) = lower($2);

-- name: CreateSupplier :one
INSERT INTO suppliers (id, tenant_id, name, contact, lead_time_days, delivery_weekdays, order_cutoff)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateSupplier :one
-- COALESCE позволяет прислать только изменённые поля (PATCH).
UPDATE suppliers SET
    name              = COALESCE(sqlc.narg(name), name),
    contact           = COALESCE(sqlc.narg(contact), contact),
    lead_time_days    = COALESCE(sqlc.narg(lead_time_days), lead_time_days),
    delivery_weekdays = COALESCE(sqlc.narg(delivery_weekdays), delivery_weekdays),
    order_cutoff      = COALESCE(sqlc.narg(order_cutoff), order_cutoff)
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: ArchiveSupplier :exec
UPDATE suppliers SET archived_at = $3 WHERE tenant_id = $1 AND id = $2;

-- name: ListItems :many
-- Таблица позиций дашборда: до 500 строк без пагинации (§6.1).
SELECT
    i.*,
    c.name AS category_name,
    s.name AS supplier_name
FROM items i
LEFT JOIN categories c ON c.tenant_id = i.tenant_id AND c.id = i.category_id
LEFT JOIN suppliers  s ON s.tenant_id = i.tenant_id AND s.id = i.default_supplier_id
WHERE i.tenant_id = $1
  AND (i.archived_at IS NULL OR sqlc.arg(include_archived)::boolean)
ORDER BY i.name;

-- name: ListActiveItemIDs :many
SELECT id FROM items WHERE tenant_id = $1 AND archived_at IS NULL ORDER BY id;

-- name: GetItem :one
SELECT * FROM items WHERE tenant_id = $1 AND id = $2;

-- name: GetItemByName :one
SELECT * FROM items WHERE tenant_id = $1 AND lower(name) = lower($2);

-- name: CreateItem :one
INSERT INTO items (
    id, tenant_id, category_id, name, base_unit,
    default_supplier_id, service_level, manual_min_qty
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateItem :one
UPDATE items SET
    name                = COALESCE(sqlc.narg(name), name),
    category_id         = COALESCE(sqlc.narg(category_id), category_id),
    default_supplier_id = COALESCE(sqlc.narg(default_supplier_id), default_supplier_id),
    service_level       = COALESCE(sqlc.narg(service_level), service_level),
    manual_min_qty      = COALESCE(sqlc.narg(manual_min_qty), manual_min_qty)
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: ArchiveItem :exec
-- Позиция не удаляется, а архивируется (FR-1): её движения остаются в журнале.
UPDATE items SET archived_at = $3 WHERE tenant_id = $1 AND id = $2;

-- name: GetSupplierItem :one
SELECT * FROM supplier_items
WHERE tenant_id = $1 AND supplier_id = $2 AND item_id = $3;

-- name: GetSupplierItemForItem :one
-- Условия закупки у поставщика по умолчанию — их и берёт расчёт заказа.
SELECT si.* FROM supplier_items si
JOIN items i ON i.tenant_id = si.tenant_id AND i.id = si.item_id
WHERE si.tenant_id = $1 AND si.item_id = $2 AND si.supplier_id = i.default_supplier_id;

-- name: ListSupplierItems :many
SELECT * FROM supplier_items WHERE tenant_id = $1 AND supplier_id = $2;

-- name: UpsertSupplierItem :one
INSERT INTO supplier_items (
    tenant_id, supplier_id, item_id, purchase_unit,
    unit_factor, min_order_qty, pack_multiple, price
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (supplier_id, item_id) DO UPDATE SET
    purchase_unit = EXCLUDED.purchase_unit,
    unit_factor   = EXCLUDED.unit_factor,
    min_order_qty = EXCLUDED.min_order_qty,
    pack_multiple = EXCLUDED.pack_multiple,
    price         = EXCLUDED.price
RETURNING *;

-- name: ListWarehouses :many
SELECT * FROM warehouses WHERE tenant_id = $1 ORDER BY name;

-- name: CreateWarehouse :one
INSERT INTO warehouses (id, tenant_id, name) VALUES ($1, $2, $3) RETURNING *;

-- name: GetDefaultWarehouse :one
-- В MVP у организации один склад (§1, допущения).
SELECT * FROM warehouses WHERE tenant_id = $1 ORDER BY created_at LIMIT 1;
