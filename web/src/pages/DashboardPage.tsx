import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowLeftRight, Clock, PackageSearch, Search, Truck } from 'lucide-react'

import { MovementDialog } from '@/components/MovementDialog'
import { StatusBadge } from '@/components/StatusBadge'
import { Alert, Button, Card, EmptyState, Input, PageHeader, Select, Skeleton, Table, Td, Th } from '@/components/ui'
import { formatDate, formatDay, formatQty, formatQtyWithUnit, plural } from '@/lib/format'
import { useCreateOrder, useDashboard, useMe } from '@/lib/queries'
import { STATUS, sortItems, type ItemStatus } from '@/lib/status'
import type { Dashboard, DashboardRow } from '@/lib/types'

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
      <div className="space-y-8">
        <Skeleton className="h-8 w-48" />
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-28" />
          ))}
        </div>
        <Skeleton className="h-80 w-full" />
      </div>
    )
  }

  const canOrder = me?.tenant.permissions.manage_orders ?? false

  return (
    <div>
      <PageHeader
        title="Дашборд"
        description={`${formatDate(data.today)} · ${plural(data.counters.total, 'позиция', 'позиции', 'позиций')}`}
      />

      <div className="space-y-8">
        <Counters counters={data.counters} active={statusFilter} onSelect={setStatusFilter} />

        {data.suggestions.length > 0 && canOrder && (
          <Suggestions suggestions={data.suggestions} today={data.today} />
        )}

        <Card
          flush
          title="Позиции"
          description={
            statusFilter === 'all' && !search
              ? 'Сначала то, что требует внимания'
              : `Найдено: ${rows.length}`
          }
          action={
            <div className="flex w-full flex-wrap gap-2 sm:w-auto">
              <div className="relative min-w-0 flex-1 sm:w-56 sm:flex-none">
                <Search
                  aria-hidden="true"
                  className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-slate-400"
                  strokeWidth={1.75}
                />
                <Input
                  type="search"
                  aria-label="Поиск по названию"
                  placeholder="Поиск"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  className="pl-9"
                />
              </div>
              <div className="w-40 shrink-0 sm:w-44">
                <Select
                  aria-label="Статус"
                  value={statusFilter}
                  onChange={(e) => setStatusFilter(e.target.value as ItemStatus | 'all')}
                >
                  <option value="all">Все статусы</option>
                  {(Object.keys(STATUS) as ItemStatus[]).map((code) => (
                    <option key={code} value={code}>
                      {STATUS[code].label}
                    </option>
                  ))}
                </Select>
              </div>
            </div>
          }
        >
          {rows.length === 0 ? (
            <EmptyState
              icon={PackageSearch}
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
      </div>

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
  counters: Dashboard['counters']
  active: ItemStatus | 'all'
  onSelect: (status: ItemStatus | 'all') => void
}

/** Counters — четыре счётчика сверху экрана, они же фильтр (§6). */
function Counters({ counters, active, onSelect }: CountersProps) {
  const tiles: { code: ItemStatus; value: number; hint: string }[] = [
    { code: 'out_of_stock', value: counters.out_of_stock, hint: 'нет на складе' },
    { code: 'critical', value: counters.critical, hint: 'кончится до поставки' },
    { code: 'order_today', value: counters.order_today, hint: 'пора заказывать' },
    { code: 'ok', value: counters.ok, hint: 'запаса хватает' },
  ]

  return (
    <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">
      {tiles.map((tile) => {
        const { label, dot } = STATUS[tile.code]
        const selected = active === tile.code
        // Ноль в тревожной плитке — хорошая новость: число гасим, чтобы
        // глаз не цеплялся за пустые красные клетки.
        const muted = tile.value === 0 && tile.code !== 'ok'
        return (
          <button
            key={tile.code}
            type="button"
            onClick={() => onSelect(selected ? 'all' : tile.code)}
            aria-pressed={selected}
            className={`rounded-xl border bg-white p-4 text-left shadow-card transition-colors sm:p-5 ${
              selected ? 'border-brand-500 ring-1 ring-brand-500' : 'border-slate-200 hover:border-slate-300'
            }`}
          >
            <span className="flex items-center gap-2 text-[13px] font-medium text-slate-600">
              <span aria-hidden="true" className={`size-2 rounded-full ${tile.code === 'out_of_stock' ? 'bg-red-600' : dot}`} />
              {label}
            </span>
            <span
              className={`mt-3 block text-3xl leading-none font-semibold tracking-tight tabular-nums ${
                muted ? 'text-slate-300' : 'text-slate-900'
              }`}
            >
              {tile.value}
            </span>
            <span className="mt-2 block text-[13px] text-slate-500">{tile.hint}</span>
          </button>
        )
      })}
    </div>
  )
}

interface SuggestionsProps {
  suggestions: Dashboard['suggestions']
  today: string
}

/** Suggestions — блок «Заказать сегодня» по поставщикам (FR-18). */
function Suggestions({ suggestions, today }: SuggestionsProps) {
  const createOrder = useCreateOrder()
  const [createdFor, setCreatedFor] = useState<string>('')

  return (
    <section aria-labelledby="order-today">
      <div className="mb-4 flex items-baseline justify-between gap-4">
        <h2 id="order-today" className="text-[15px] font-semibold text-slate-900">
          Заказать сегодня
        </h2>
        <p className="text-[13px] text-slate-500">
          {plural(suggestions.length, 'поставщик', 'поставщика', 'поставщиков')}
        </p>
      </div>

      {createdFor && (
        <div className="mb-4">
          <Alert kind="success">
            Черновик заказа создан.{' '}
            <Link to={`/orders/${createdFor}`} className="font-medium underline underline-offset-2">
              Открыть и отправить
            </Link>
          </Alert>
        </div>
      )}
      {createOrder.isError && (
        <div className="mb-4">
          <Alert kind="error">{createOrder.error.message}</Alert>
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        {suggestions.map((block) => (
          <article
            key={block.supplier_id}
            className="flex flex-col rounded-xl border border-slate-200 bg-white shadow-card"
          >
            <header className="flex items-start justify-between gap-3 px-5 pt-5">
              <div className="min-w-0">
                <h3 className="truncate text-[15px] font-semibold text-slate-900">{block.supplier_name}</h3>
                <p className="mt-1 flex items-center gap-1.5 text-[13px] text-slate-500">
                  <Clock aria-hidden="true" className="size-3.5" strokeWidth={1.75} />
                  заказ до {String(block.order_by.Hour).padStart(2, '0')}:
                  {String(block.order_by.Minute).padStart(2, '0')}
                  {block.contact && <span className="truncate">· {block.contact}</span>}
                </p>
              </div>
              <span className="shrink-0 rounded-md bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-600 tabular-nums">
                {block.lines.length}
              </span>
            </header>

            <ul className="mt-3 flex-1 divide-y divide-slate-100 px-5">
              {block.lines.map((line) => (
                <li key={line.item_id} className="flex items-baseline justify-between gap-4 py-2.5">
                  <span className="min-w-0">
                    <Link
                      to={`/items/${line.item_id}`}
                      className="text-sm font-medium text-slate-900 hover:text-brand-700"
                    >
                      {line.name}
                    </Link>
                    {line.stockout_date && (
                      <span className="block text-[13px] text-slate-500">
                        хватит до {formatDay(line.stockout_date, today)}
                      </span>
                    )}
                  </span>
                  <span className="shrink-0 text-right text-sm font-semibold text-slate-900 tabular-nums">
                    {line.purchase_qty && line.purchase_unit
                      ? `${formatQty(line.purchase_qty)} ${line.purchase_unit}`
                      : formatQtyWithUnit(line.qty, line.base_unit)}
                    {line.purchase_qty && line.purchase_unit && (
                      <span className="block text-xs font-normal text-slate-500">
                        {formatQtyWithUnit(line.qty, line.base_unit)}
                      </span>
                    )}
                  </span>
                </li>
              ))}
            </ul>

            <footer className="px-5 pt-3 pb-5">
              <Button
                size="sm"
                icon={Truck}
                className="w-full sm:w-auto"
                onClick={() => {
                  setCreatedFor('')
                  createOrder.mutate(block.supplier_id, {
                    onSuccess: (order) => setCreatedFor(order.id),
                  })
                }}
                loading={createOrder.isPending && createOrder.variables === block.supplier_id}
                disabled={createOrder.isPending}
              >
                Оформить заказ
              </Button>
            </footer>
          </article>
        ))}
      </div>
    </section>
  )
}

interface ItemsTableProps {
  rows: DashboardRow[]
  today: string
  onMovement: (row: DashboardRow) => void
}

/**
 * Таблица позиций. На телефоне превращается в список (§6.1): таблица
 * с семью колонками на 360 px нечитаема.
 */
function ItemsTable({ rows, today, onMovement }: ItemsTableProps) {
  return (
    <>
      {/* Телефон: список */}
      <ul className="divide-y divide-slate-100 md:hidden">
        {rows.map((row) => (
          <li key={row.item_id} className="px-5 py-4">
            <div className="flex items-start justify-between gap-3">
              <Link to={`/items/${row.item_id}`} className="min-w-0 font-medium text-slate-900">
                {row.name}
              </Link>
              <StatusBadge status={row.status} />
            </div>
            <p className="mt-1.5 text-[13px] text-slate-500">
              <span className="font-medium text-slate-900 tabular-nums">
                {formatQtyWithUnit(row.on_hand, row.base_unit)}
              </span>
              {Number(row.on_order) > 0 && ` · в пути ${formatQty(row.on_order)}`}
              {row.stockout_date && ` · до ${formatDay(row.stockout_date, today)}`}
            </p>
            <div className="mt-3">
              <Button variant="secondary" size="sm" icon={ArrowLeftRight} onClick={() => onMovement(row)}>
                Движение
              </Button>
            </div>
          </li>
        ))}
      </ul>

      {/* Десктоп: таблица */}
      <div className="hidden md:block">
        <Table>
          <thead>
            <tr>
              <Th>Позиция</Th>
              <Th align="right">На складе</Th>
              <Th align="right">В пути</Th>
              <Th>Хватит до</Th>
              <Th>Статус</Th>
              <Th align="right">Заказать</Th>
              <Th className="hidden xl:table-cell">Поставщик</Th>
              <Th>
                <span className="sr-only">Действия</span>
              </Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.item_id} className="group transition-colors hover:bg-slate-50/70 [&:last-child>td]:border-0">
                <Td>
                  <Link to={`/items/${row.item_id}`} className="font-medium text-slate-900 hover:text-brand-700">
                    {row.name}
                  </Link>
                  {row.category_name && <span className="block text-[13px] text-slate-500">{row.category_name}</span>}
                </Td>
                <Td align="right" className="font-medium whitespace-nowrap text-slate-900">
                  {formatQtyWithUnit(row.on_hand, row.base_unit)}
                </Td>
                <Td align="right" className="text-slate-500">
                  {Number(row.on_order) > 0 ? formatQty(row.on_order) : <Dash />}
                </Td>
                <Td className="whitespace-nowrap text-slate-600">
                  {row.stockout_date ? formatDay(row.stockout_date, today) : <Dash />}
                </Td>
                <Td>
                  <StatusBadge status={row.status} />
                </Td>
                <Td align="right" className="font-medium whitespace-nowrap text-slate-900">
                  {Number(row.recommended_qty) > 0 ? (
                    row.purchase_qty && row.purchase_unit ? (
                      `${formatQty(row.purchase_qty)} ${row.purchase_unit}`
                    ) : (
                      formatQty(row.recommended_qty)
                    )
                  ) : (
                    <Dash />
                  )}
                </Td>
                <Td className="hidden text-slate-500 xl:table-cell">{row.supplier_name ?? <Dash />}</Td>
                <Td align="right">
                  <button
                    type="button"
                    onClick={() => onMovement(row)}
                    aria-label={`Записать движение: ${row.name}`}
                    title="Записать движение"
                    className="inline-flex size-8 items-center justify-center rounded-lg text-slate-400 transition-colors group-hover:text-slate-600 hover:bg-slate-100 hover:text-slate-900"
                  >
                    <ArrowLeftRight aria-hidden="true" className="size-4" strokeWidth={1.75} />
                  </button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </div>
    </>
  )
}

/** Прочерк на месте пустого значения: тише основного текста. */
function Dash() {
  return <span className="text-slate-300">—</span>
}
