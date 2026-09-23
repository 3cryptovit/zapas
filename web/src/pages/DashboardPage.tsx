import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { StatusBadge } from '@/components/StatusBadge'
import { Alert, Button, Card, EmptyState, Input, Select, Skeleton } from '@/components/ui'
import { MovementDialog } from '@/components/MovementDialog'
import { formatDay, formatQty, formatQtyWithUnit, relativeDay } from '@/lib/format'
import { useCreateOrder, useDashboard, useMe } from '@/lib/queries'
import { STATUS, sortItems, type ItemStatus } from '@/lib/status'
import type { DashboardRow } from '@/lib/types'

/**
 * Главный экран (§6): сверху счётчики по статусам, ниже «Заказать сегодня»
 * по поставщикам и таблица всех позиций.
 */
export function DashboardPage() {
  const { data, isLoading, error } = useDashboard()
  const { data: me } = useMe()
  const [statusFilter, setStatusFilter] = useState<ItemStatus | 'all'>('all')
  const [search, setSearch] = useState('')
  const [movementFor, setMovementFor] = useState<DashboardRow | null>(null)

  const rows = useMemo(() => {
    if (!data) return []
    const filtered = data.items.filter((row) => {
      if (statusFilter !== 'all' && row.status !== statusFilter) return false
      if (search && !row.name.toLowerCase().includes(search.toLowerCase())) return false
      return true
    })
    return sortItems(filtered)
  }, [data, statusFilter, search])

  if (error) {
    return <Alert kind="error">Не удалось загрузить дашборд: {error.message}</Alert>
  }

  if (isLoading || !data) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-20 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  const canOrder = me?.tenant.permissions.manage_orders ?? false

  return (
    <div className="space-y-5">
      <Counters counters={data.counters} active={statusFilter} onSelect={setStatusFilter} />

      {data.suggestions.length > 0 && canOrder && (
        <Suggestions suggestions={data.suggestions} today={data.today} />
      )}

      <Card
        title={`Позиции (${rows.length})`}
        action={
          <div className="flex flex-wrap gap-2">
            <Input
              type="search"
              placeholder="Поиск по названию"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="min-h-9 w-44 text-sm"
            />
            <Select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as ItemStatus | 'all')}
              className="min-h-9 w-44 text-sm"
            >
              <option value="all">Все статусы</option>
              {(Object.keys(STATUS) as ItemStatus[]).map((code) => (
                <option key={code} value={code}>
                  {STATUS[code].label}
                </option>
              ))}
            </Select>
          </div>
        }
      >
        {rows.length === 0 ? (
          <EmptyState
            title={data.items.length === 0 ? 'Пока нет ни одной позиции' : 'Ничего не найдено'}
            hint={
              data.items.length === 0
                ? 'Добавьте первую позицию или импортируйте CSV в настройках.'
                : 'Смягчите фильтр или очистите поиск.'
            }
          />
        ) : (
          <ItemsTable rows={rows} today={data.today} onMovement={setMovementFor} />
        )}
      </Card>

      <MovementDialog
        item={
          movementFor
            ? {
                id: movementFor.item_id,
                name: movementFor.name,
                base_unit: movementFor.base_unit,
                on_hand: movementFor.on_hand,
              }
            : null
        }
        onClose={() => setMovementFor(null)}
      />
    </div>
  )
}

interface CountersProps {
  counters: {
    out_of_stock: number
    critical: number
    order_today: number
    ok: number
    no_forecast: number
    total: number
  }
  active: ItemStatus | 'all'
  onSelect: (status: ItemStatus | 'all') => void
}

/** Counters — четыре счётчика сверху экрана, они же фильтр (§6). */
function Counters({ counters, active, onSelect }: CountersProps) {
  const tiles: { code: ItemStatus | 'all'; label: string; value: number; tone: string }[] = [
    {
      code: 'out_of_stock',
      label: 'Закончилось',
      value: counters.out_of_stock,
      tone: 'bg-red-50 text-red-900 ring-red-200',
    },
    {
      code: 'critical',
      label: 'Критично',
      value: counters.critical,
      tone: 'bg-red-50 text-red-900 ring-red-200',
    },
    {
      code: 'order_today',
      label: 'Заказать сегодня',
      value: counters.order_today,
      tone: 'bg-amber-50 text-amber-900 ring-amber-200',
    },
    {
      code: 'ok',
      label: 'Хватает',
      value: counters.ok,
      tone: 'bg-emerald-50 text-emerald-900 ring-emerald-200',
    },
  ]

  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {tiles.map((tile) => (
        <button
          key={tile.code}
          type="button"
          onClick={() => onSelect(active === tile.code ? 'all' : tile.code)}
          aria-pressed={active === tile.code}
          className={`rounded-xl px-4 py-3 text-left ring-1 ring-inset transition ${tile.tone} ${
            active === tile.code ? 'ring-2' : ''
          }`}
        >
          <span className="block text-2xl font-semibold">{tile.value}</span>
          <span className="block text-sm">{tile.label}</span>
        </button>
      ))}
    </div>
  )
}

interface SuggestionsProps {
  suggestions: NonNullable<ReturnType<typeof useDashboard>['data']>['suggestions']
  today: string
}

/** Suggestions — блок «Заказать сегодня» по поставщикам (FR-18). */
function Suggestions({ suggestions, today }: SuggestionsProps) {
  const createOrder = useCreateOrder()
  const [createdFor, setCreatedFor] = useState<string>('')

  return (
    <Card title="Заказать сегодня">
      <div className="space-y-4">
        {suggestions.map((block) => (
          <div key={block.supplier_id} className="rounded-lg bg-amber-50/60 p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p className="font-medium text-slate-900">{block.supplier_name}</p>
                <p className="text-sm text-slate-600">
                  до{' '}
                  {String(block.order_by.Hour).padStart(2, '0')}:
                  {String(block.order_by.Minute).padStart(2, '0')}
                  {block.contact ? ` · ${block.contact}` : ''}
                </p>
              </div>
              <Button
                onClick={() => {
                  setCreatedFor('')
                  createOrder.mutate(block.supplier_id, {
                    onSuccess: (order) => setCreatedFor(order.id),
                  })
                }}
                loading={createOrder.isPending}
              >
                Оформить заказ
              </Button>
            </div>

            <ul className="mt-3 space-y-1">
              {block.lines.map((line) => (
                <li key={line.item_id} className="text-sm text-slate-800">
                  <Link to={`/items/${line.item_id}`} className="font-medium hover:underline">
                    {line.name}
                  </Link>
                  {' — '}
                  {line.purchase_qty && line.purchase_unit
                    ? `${formatQty(line.purchase_qty)} ${line.purchase_unit} (${formatQtyWithUnit(line.qty, line.base_unit)})`
                    : formatQtyWithUnit(line.qty, line.base_unit)}
                  {line.stockout_date && (
                    <span className="text-slate-500">
                      {' '}
                      · сейчас хватит до {formatDay(line.stockout_date, today)}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>

      {createdFor && (
        <div className="mt-3">
          <Alert kind="success">
            Черновик заказа создан.{' '}
            <Link to={`/orders/${createdFor}`} className="font-medium underline">
              Открыть и отправить
            </Link>
          </Alert>
        </div>
      )}
      {createOrder.isError && (
        <div className="mt-3">
          <Alert kind="error">{createOrder.error.message}</Alert>
        </div>
      )}
    </Card>
  )
}

interface ItemsTableProps {
  rows: DashboardRow[]
  today: string
  onMovement: (row: DashboardRow) => void
}

/**
 * Таблица позиций. На телефоне превращается в список карточек (§6.1):
 * таблица с семью колонками на 360 px нечитаема.
 */
function ItemsTable({ rows, today, onMovement }: ItemsTableProps) {
  return (
    <>
      {/* Телефон: карточки */}
      <ul className="space-y-2 md:hidden">
        {rows.map((row) => (
          <li key={row.item_id} className="rounded-lg border border-slate-200 p-3">
            <div className="flex items-start justify-between gap-2">
              <Link to={`/items/${row.item_id}`} className="font-medium text-slate-900">
                {row.name}
              </Link>
              <StatusBadge status={row.status} />
            </div>
            <p className="mt-1 text-sm text-slate-600">
              На складе {formatQtyWithUnit(row.on_hand, row.base_unit)}
              {Number(row.on_order) > 0 && `, в пути ${formatQty(row.on_order)}`}
            </p>
            {row.stockout_date && (
              <p className="text-sm text-slate-500">
                Хватит до {formatDay(row.stockout_date, today)}
              </p>
            )}
            <div className="mt-2">
              <Button variant="secondary" className="min-h-9 text-xs" onClick={() => onMovement(row)}>
                Движение
              </Button>
            </div>
          </li>
        ))}
      </ul>

      {/* Десктоп: таблица */}
      <div className="hidden overflow-x-auto md:block">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2 pr-3 font-medium">Позиция</th>
              <th className="py-2 pr-3 font-medium">Категория</th>
              <th className="py-2 pr-3 text-right font-medium">На складе</th>
              <th className="py-2 pr-3 text-right font-medium">В пути</th>
              <th className="py-2 pr-3 font-medium">Хватит до</th>
              <th className="py-2 pr-3 font-medium">Статус</th>
              <th className="py-2 pr-3 text-right font-medium">Заказать</th>
              <th className="py-2 pr-3 font-medium">Поставщик</th>
              <th className="py-2 font-medium"></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.item_id} className="border-b border-slate-100 last:border-0">
                <td className="py-2 pr-3">
                  <Link to={`/items/${row.item_id}`} className="font-medium text-slate-900 hover:underline">
                    {row.name}
                  </Link>
                </td>
                <td className="py-2 pr-3 text-slate-500">{row.category_name ?? '—'}</td>
                <td className="py-2 pr-3 text-right tabular-nums">
                  {formatQtyWithUnit(row.on_hand, row.base_unit)}
                </td>
                <td className="py-2 pr-3 text-right tabular-nums text-slate-500">
                  {Number(row.on_order) > 0 ? formatQty(row.on_order) : '—'}
                </td>
                <td className="py-2 pr-3 text-slate-600">
                  {row.stockout_date ? formatDay(row.stockout_date, today) : '—'}
                </td>
                <td className="py-2 pr-3">
                  <StatusBadge status={row.status} />
                </td>
                <td className="py-2 pr-3 text-right tabular-nums">
                  {Number(row.recommended_qty) > 0
                    ? row.purchase_qty && row.purchase_unit
                      ? `${formatQty(row.purchase_qty)} ${row.purchase_unit}`
                      : formatQty(row.recommended_qty)
                    : '—'}
                </td>
                <td className="py-2 pr-3 text-slate-500">{row.supplier_name ?? '—'}</td>
                <td className="py-2 text-right">
                  <Button
                    variant="secondary"
                    className="min-h-9 px-3 text-xs"
                    onClick={() => onMovement(row)}
                  >
                    Движение
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

// relativeDay используется в подписях дат на карточках.
void relativeDay
