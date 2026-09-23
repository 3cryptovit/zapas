/**
 * Запросы и мутации (TanStack Query).
 *
 * После любого изменения инвалидируется дашборд и лента: §6.3 требует,
 * чтобы данные обновлялись после каждого действия, а не по F5.
 */

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryOptions,
} from '@tanstack/react-query'

import { newIdempotencyKey, request } from './api'
import type {
  AdvanceResult,
  Category,
  Dashboard,
  Feed,
  ImportResult,
  Insights,
  Item,
  Me,
  Movement,
  MovementResponse,
  MovementType,
  Order,
  OrderStatus,
  Page,
  Qty,
  StockCount,
  Supplier,
} from './types'

/** keys — единый реестр ключей кэша: так их не расходятся по файлам. */
export const keys = {
  me: ['me'] as const,
  dashboard: ['dashboard'] as const,
  insights: (id: string) => ['insights', id] as const,
  items: (archived: boolean) => ['items', archived] as const,
  categories: ['categories'] as const,
  suppliers: ['suppliers'] as const,
  movements: (filter: MovementFilter) => ['movements', filter] as const,
  counts: ['counts'] as const,
  count: (id: string) => ['count', id] as const,
  orders: (status?: OrderStatus) => ['orders', status ?? 'all'] as const,
  order: (id: string) => ['order', id] as const,
  notifications: ['notifications'] as const,
}

export interface MovementFilter {
  itemId?: string
  type?: MovementType
  authorId?: string
  limit?: number
}

// --- чтение ---

export function useMe(options?: Partial<UseQueryOptions<Me>>) {
  return useQuery({
    queryKey: keys.me,
    queryFn: () => request<Me>('/me'),
    // Сессия проверяется редко: её смену видно по 401 на любом запросе.
    staleTime: 60_000,
    retry: false,
    ...options,
  })
}

export function useDashboard() {
  return useQuery({
    queryKey: keys.dashboard,
    queryFn: () => request<Dashboard>('/dashboard'),
  })
}

export function useInsights(itemId: string) {
  return useQuery({
    queryKey: keys.insights(itemId),
    queryFn: () => request<Insights>(`/items/${itemId}/insights`),
    enabled: Boolean(itemId),
  })
}

export function useItems(includeArchived = false) {
  return useQuery({
    queryKey: keys.items(includeArchived),
    queryFn: () =>
      request<{ items: Item[] }>(
        `/items${includeArchived ? '?include_archived=true' : ''}`,
      ).then((r) => r.items),
  })
}

export function useCategories() {
  return useQuery({
    queryKey: keys.categories,
    queryFn: () => request<{ items: Category[] }>('/categories').then((r) => r.items),
  })
}

export function useSuppliers() {
  return useQuery({
    queryKey: keys.suppliers,
    queryFn: () => request<{ items: Supplier[] }>('/suppliers').then((r) => r.items),
  })
}

export function useMovements(filter: MovementFilter = {}) {
  return useQuery({
    queryKey: keys.movements(filter),
    queryFn: () => {
      const params = new URLSearchParams()
      if (filter.itemId) params.set('item_id', filter.itemId)
      if (filter.type) params.set('type', filter.type)
      if (filter.authorId) params.set('author_id', filter.authorId)
      params.set('limit', String(filter.limit ?? 50))
      return request<Page<Movement>>(`/movements?${params}`)
    },
  })
}

export function useCounts() {
  return useQuery({
    queryKey: keys.counts,
    queryFn: () => request<{ items: StockCount[] | null }>('/counts').then((r) => r.items ?? []),
  })
}

export function useCount(id: string) {
  return useQuery({
    queryKey: keys.count(id),
    queryFn: () => request<StockCount>(`/counts/${id}`),
    enabled: Boolean(id),
  })
}

export function useOrders(status?: OrderStatus) {
  return useQuery({
    queryKey: keys.orders(status),
    queryFn: () =>
      request<{ items: Order[] }>(`/orders${status ? `?status=${status}` : ''}`).then(
        (r) => r.items,
      ),
  })
}

export function useOrder(id: string) {
  return useQuery({
    queryKey: keys.order(id),
    queryFn: () => request<Order>(`/orders/${id}`),
    enabled: Boolean(id),
  })
}

export function useNotifications() {
  return useQuery({
    queryKey: keys.notifications,
    queryFn: () => request<Feed>('/notifications?limit=50'),
    // Лента обновляется чаще остального: в ней появляются алерты.
    refetchInterval: 60_000,
  })
}

// --- изменения ---

/** useInvalidateAll обновляет всё, на что влияет движение или заказ. */
function useInvalidateAll() {
  const client = useQueryClient()
  return () => {
    void client.invalidateQueries({ queryKey: keys.dashboard })
    void client.invalidateQueries({ queryKey: ['movements'] })
    void client.invalidateQueries({ queryKey: ['insights'] })
    void client.invalidateQueries({ queryKey: ['orders'] })
    void client.invalidateQueries({ queryKey: keys.notifications })
    void client.invalidateQueries({ queryKey: ['counts'] })
  }
}

export interface CreateMovementInput {
  type: 'receipt' | 'usage' | 'writeoff'
  item_id: string
  qty: Qty
  reason?: string
  comment?: string
  occurred_at?: string
}

export function useCreateMovement() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (input: CreateMovementInput) =>
      request<MovementResponse>('/movements', {
        method: 'POST',
        body: input,
        // Ключ на попытку: повтор с плохой связи не создаст второе
        // движение (FR-10).
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useReverseMovement() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (movementId: string) =>
      request<MovementResponse>(`/movements/${movementId}/reverse`, {
        method: 'POST',
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useCreateOrder() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (supplierId: string) =>
      request<Order>('/orders', {
        method: 'POST',
        body: { supplier_id: supplierId },
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useSendOrder() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (orderId: string) =>
      request<Order>(`/orders/${orderId}/send`, {
        method: 'POST',
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useCancelOrder() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (orderId: string) =>
      request<Order>(`/orders/${orderId}/cancel`, {
        method: 'POST',
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useReceiveOrder() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (input: { orderId: string; lines?: { item_id: string; qty_received: Qty }[] }) =>
      request<Order>(`/orders/${input.orderId}/receive`, {
        method: 'POST',
        body: input.lines ? { lines: input.lines } : {},
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useCreateCount() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (note: string) =>
      request<StockCount>('/counts', { method: 'POST', body: { scope: 'all', note } }),
    onSuccess: invalidate,
  })
}

export function useSaveCountLines() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: (input: { countId: string; lines: { item_id: string; counted_qty: Qty }[] }) =>
      request<StockCount>(`/counts/${input.countId}/lines`, {
        method: 'PUT',
        body: { lines: input.lines },
      }),
    onSuccess: (_data, input) => {
      void client.invalidateQueries({ queryKey: keys.count(input.countId) })
    },
  })
}

export function usePostCount() {
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (input: { countId: string; force?: boolean }) =>
      request<StockCount>(`/counts/${input.countId}/post`, {
        method: 'POST',
        body: { force: input.force ?? false },
        idempotencyKey: newIdempotencyKey(),
      }),
    onSuccess: invalidate,
  })
}

export function useMarkNotificationsRead() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: (ids?: string[]) =>
      request<{ marked: number }>('/notifications/read', {
        method: 'POST',
        body: ids ? { ids } : {},
      }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.notifications })
    },
  })
}

export function useAdvanceSandbox() {
  const client = useQueryClient()
  const invalidate = useInvalidateAll()

  return useMutation({
    mutationFn: (days: 1 | 7) =>
      request<AdvanceResult>('/sandbox/advance', { method: 'POST', body: { days } }),
    onSuccess: () => {
      // Виртуальная дата изменилась — обновляем и её тоже.
      void client.invalidateQueries({ queryKey: keys.me })
      invalidate()
    },
  })
}

export function useSetAutopilot() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: (enabled: boolean) =>
      request<{ autopilot: boolean }>('/sandbox/autopilot', {
        method: 'PUT',
        body: { enabled },
      }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.me })
    },
  })
}

export function useResetSandbox() {
  return useMutation({
    mutationFn: () => request<{ redirect_to: string }>('/sandbox/reset', { method: 'POST' }),
  })
}

export function useLogin() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: (input: { email: string; password: string }) =>
      request<Me>('/auth/login', { method: 'POST', body: input }),
    onSuccess: (me) => {
      client.setQueryData(keys.me, me)
    },
  })
}

export function useLogout() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: () => request<void>('/auth/logout', { method: 'POST' }),
    onSuccess: () => {
      client.clear()
    },
  })
}

export function useImportCSV() {
  const invalidate = useInvalidateAll()
  const client = useQueryClient()

  return useMutation({
    mutationFn: (input: { csv: string; dryRun: boolean }) =>
      request<ImportResult>(`/imports/items${input.dryRun ? '?dry_run=true' : ''}`, {
        method: 'POST',
        raw: { body: input.csv, contentType: 'text/csv' },
      }),
    onSuccess: (result) => {
      if (!result.dry_run) {
        void client.invalidateQueries({ queryKey: ['items'] })
        void client.invalidateQueries({ queryKey: keys.suppliers })
        void client.invalidateQueries({ queryKey: keys.categories })
        invalidate()
      }
    },
  })
}

export function useCreateItem() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: (input: {
      name: string
      base_unit: string
      category_id?: string
      default_supplier_id?: string
      manual_min_qty?: Qty
    }) => request<Item>('/items', { method: 'POST', body: input }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['items'] })
      void client.invalidateQueries({ queryKey: keys.dashboard })
    },
  })
}

/** useLinkTelegram выдаёт одноразовую ссылку привязки чата (§5.5). */
export function useLinkTelegram() {
  return useMutation({
    mutationFn: () =>
      request<{ url: string; code: string; expires_at: string }>('/telegram/link', {
        method: 'POST',
      }),
  })
}

export function useCreateSupplier() {
  const client = useQueryClient()

  return useMutation({
    mutationFn: (input: {
      name: string
      contact?: string
      lead_time_days: number
      delivery_weekdays: number[]
      order_cutoff: string
    }) => request<Supplier>('/suppliers', { method: 'POST', body: input }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.suppliers })
    },
  })
}
