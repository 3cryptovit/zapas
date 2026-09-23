/**
 * Типы ответов API.
 *
 * Количества везде строки ("12.500"): JavaScript теряет точность
 * на числах с плавающей точкой, а остаток обязан сходиться с журналом (§11).
 */

import type { ItemStatus } from './status'

export type BaseUnit = 'kg' | 'l' | 'pcs'

/** Day — календарный день в поясе тенанта, "YYYY-MM-DD". */
export type Day = string

/** Qty — количество строкой. */
export type Qty = string

export interface Me {
  user: {
    id: string
    email: string
    name: string
    role: 'owner' | 'staff'
    role_label: string
    telegram_linked: boolean
  }
  tenant: {
    id: string
    name: string
    timezone: string
    is_sandbox: boolean
    today: Day
    expires_at?: string
    permissions: {
      manage_catalog: boolean
      manage_settings: boolean
      manage_orders: boolean
      export_csv: boolean
    }
  }
}

export interface Dashboard {
  today: Day
  counters: {
    out_of_stock: number
    critical: number
    order_today: number
    ok: number
    no_forecast: number
    total: number
  }
  suggestions: SupplierSuggestion[]
  items: DashboardRow[]
}

export interface DashboardRow {
  item_id: string
  name: string
  base_unit: BaseUnit
  category_name?: string
  on_hand: Qty
  on_order: Qty
  status: ItemStatus
  stockout_date?: Day
  order_by?: string
  recommended_qty: Qty
  purchase_qty?: Qty
  purchase_unit?: string
  supplier_id?: string
  supplier_name?: string
  accuracy?: number
  forecast_model?: string
}

export interface SupplierSuggestion {
  supplier_id: string
  supplier_name: string
  contact?: string
  order_by: { Hour: number; Minute: number }
  lines: SuggestionLine[]
}

export interface SuggestionLine {
  item_id: string
  name: string
  base_unit: BaseUnit
  on_hand: Qty
  qty: Qty
  purchase_qty?: Qty
  purchase_unit?: string
  stockout_date?: Day
  status: ItemStatus
}

export interface Insights {
  today: Day
  item: {
    id: string
    name: string
    base_unit: BaseUnit
    category_name?: string
    service_level: number
    manual_min_qty: Qty
    supplier_name?: string
    supplier_id?: string
    purchase_unit?: string
    unit_factor: Qty
  }
  status: {
    code: ItemStatus
    on_hand: Qty
    on_order: Qty
    stockout_date?: Day
    order_by?: string
    recommended_qty: Qty
    purchase_qty?: Qty
    target_level: Qty
    safety_stock: Qty
    explanation: Explanation
  }
  accuracy: {
    model: string
    accuracy: number
    wape: number
    bias: number
    sigma: number
    window_days: number
    days_with_data: number
  }
  history: { day: Day; qty: Qty; has_data: boolean; stockout: boolean }[]
  forecast: { day: Day; qty: Qty; lower: Qty; upper: Qty }[]
  projection: { day: Day; on_hand: Qty; incoming: Qty }[]
  incoming: { day: Day; qty: Qty }[]
}

/** Explanation — числа блока «почему такой статус» (§6.2). */
export interface Explanation {
  model: string
  on_hand: Qty
  on_order: Qty
  inventory_position: Qty
  need_until_d2: Qty
  safety_stock: Qty
  target_level: Qty
  shortfall: Qty
  recommended_qty: Qty
  d1: Day | null
  d2: Day | null
  order_by: string
  days_to_d2: number
  sigma: number
  z: number
  can_wait: boolean
}

export type MovementType =
  | 'opening'
  | 'receipt'
  | 'usage'
  | 'writeoff'
  | 'adjustment'
  | 'reversal'

export interface Movement {
  id: string
  item_id: string
  item_name?: string
  base_unit?: BaseUnit
  type: MovementType
  type_label: string
  qty: Qty
  occurred_at: string
  created_at: string
  created_by?: string
  author_name?: string
  reason?: string
  reason_label?: string
  comment?: string
  order_id?: string
  count_id?: string
  reverses_id?: string
  reversed: boolean
}

export interface Page<T> {
  items: T[]
  next_cursor?: string
}

export interface MovementResponse {
  movement: Movement
  balance: { item_id: string; on_hand: Qty; on_order: Qty; updated_at: string }
  status: {
    code: ItemStatus
    stockout_date?: Day
    order_by?: string
    recommended_qty: Qty
  }
}

export interface Item {
  id: string
  name: string
  base_unit: BaseUnit
  unit_label: string
  category_id?: string
  category_name?: string
  default_supplier_id?: string
  supplier_name?: string
  service_level: number
  manual_min_qty: Qty
  archived_at?: string
}

export interface Category {
  id: string
  name: string
}

export interface Supplier {
  id: string
  name: string
  contact?: string
  lead_time_days: number
  delivery_weekdays: number[]
  order_cutoff: { Hour: number; Minute: number }
  archived_at?: string
}

export type OrderStatus = 'draft' | 'sent' | 'received' | 'cancelled'

export interface Order {
  id: string
  supplier_id: string
  supplier_name?: string
  status: OrderStatus
  status_label: string
  expected_at: Day | null
  sent_at?: string
  received_at?: string
  cancelled_at?: string
  note?: string
  created_at: string
  lines?: OrderLine[]
  text?: string
  late: boolean
}

export interface OrderLine {
  item_id: string
  item_name: string
  base_unit: BaseUnit
  qty_ordered: Qty
  qty_received?: Qty
  purchase_unit?: string
  unit_factor: Qty
  price?: Qty
}

export type CountStatus = 'draft' | 'posted'

export interface StockCount {
  id: string
  status: CountStatus
  scope: string
  note?: string
  created_at: string
  posted_at?: string
  lines?: CountLine[]
}

export interface CountLine {
  item_id: string
  item_name: string
  base_unit: BaseUnit
  expected_qty: Qty
  current_qty: Qty
  counted_qty?: Qty
  diff?: Qty
}

export type NotificationType =
  | 'daily_digest'
  | 'critical'
  | 'cutoff_reminder'
  | 'order_late'
  | 'receipt_mismatch'

export interface Notification {
  id: string
  type: NotificationType
  type_label: string
  payload: {
    title: string
    body: string
    link?: string
    items?: { name: string; qty?: Qty; unit?: string; note?: string }[]
  }
  read: boolean
  created_at: string
}

export interface Feed {
  items: Notification[]
  next_cursor?: string
  unread: number
}

export interface AdvanceResult {
  days: number
  today: Day
  received: number
  ordered: number
  notifications: number
}

export interface ImportResult {
  dry_run: boolean
  items: number
  categories: number
  suppliers: number
  with_opening: number
  errors?: { line: number; column?: string; message: string }[]
  preview?: {
    line: number
    name: string
    category?: string
    base_unit: BaseUnit
    supplier?: string
    opening_qty: Qty
  }[]
}
