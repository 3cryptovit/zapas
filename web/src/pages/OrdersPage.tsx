import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Check, ChevronRight, Copy, PackageCheck, Send, Truck } from 'lucide-react'

import {
  Alert,
  BackLink,
  Badge,
  Button,
  Card,
  EmptyState,
  Input,
  PageHeader,
  Skeleton,
  Table,
  Td,
  Th,
} from '@/components/ui'
import { formatDate, formatDay, formatQty, formatQtyWithUnit, inputQty } from '@/lib/format'
import { useCancelOrder, useMe, useOrder, useOrders, useReceiveOrder, useSendOrder } from '@/lib/queries'
import type { Order, OrderStatus } from '@/lib/types'

// Стадия заказа: серый черновик, синий «в пути» — идёт процесс,
// зелёный «принят» — закрыто. Отменённый уходит в фон.
const statusBadge: Record<OrderStatus, string> = {
  draft: 'bg-slate-100 text-slate-600',
  sent: 'bg-brand-50 text-brand-700',
  received: 'bg-emerald-50 text-emerald-700',
  cancelled: 'bg-slate-100 text-slate-400 line-through',
}

const filters: { value: OrderStatus | undefined; label: string }[] = [
  { value: undefined, label: 'Все' },
  { value: 'draft', label: 'Черновики' },
  { value: 'sent', label: 'В пути' },
  { value: 'received', label: 'Принятые' },
]

function OrderBadges({ order }: { order: Order }) {
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <Badge className={statusBadge[order.status]}>{order.status_label}</Badge>
      {order.late && <Badge className="bg-red-50 text-red-700">опаздывает</Badge>}
    </span>
  )
}

/** Список заказов (§6). */
export function OrdersPage() {
  const [filter, setFilter] = useState<OrderStatus | undefined>(undefined)
  const { data, isLoading } = useOrders(filter)

  return (
    <div>
      <PageHeader title="Заказы" description="Черновик собирается из рекомендации на дашборде одной кнопкой." />

      <div
        role="tablist"
        aria-label="Статус заказа"
        className="mb-6 inline-flex max-w-full gap-1 overflow-x-auto rounded-lg bg-slate-100 p-1"
      >
        {filters.map((f) => {
          const active = filter === f.value
          return (
            <button
              key={f.label}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setFilter(f.value)}
              className={`min-h-9 rounded-md px-3 text-[13px] font-medium whitespace-nowrap transition-colors sm:min-h-8 ${
                active ? 'bg-white text-slate-900 shadow-card' : 'text-slate-600 hover:text-slate-900'
              }`}
            >
              {f.label}
            </button>
          )
        })}
      </div>

      <Card flush>
        {isLoading ? (
          <div className="p-6">
            <Skeleton className="h-32 w-full" />
          </div>
        ) : !data?.length ? (
          <EmptyState
            icon={Truck}
            title="Заказов пока нет"
            hint="Заказ собирается одной кнопкой из блока «Заказать сегодня» на дашборде."
          />
        ) : (
          <>
            <ul className="divide-y divide-slate-100 md:hidden">
              {data.map((order) => (
                <li key={order.id}>
                  <Link to={`/orders/${order.id}`} className="flex items-center justify-between gap-3 px-5 py-4">
                    <span className="min-w-0">
                      <span className="block font-medium text-slate-900">{order.supplier_name ?? 'Поставщик'}</span>
                      <span className="mt-1.5 block">
                        <OrderBadges order={order} />
                      </span>
                      <span className="mt-1.5 block text-[13px] text-slate-500">
                        {order.expected_at ? `поставка ${formatDate(order.expected_at)}` : 'дата поставки не назначена'}
                      </span>
                    </span>
                    <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-slate-300" strokeWidth={1.75} />
                  </Link>
                </li>
              ))}
            </ul>

            <div className="hidden pt-5 md:block">
              <Table>
                <thead>
                  <tr>
                    <Th>Поставщик</Th>
                    <Th>Статус</Th>
                    <Th>Поставка</Th>
                    <Th>Создан</Th>
                    <Th>
                      <span className="sr-only">Открыть</span>
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {data.map((order) => (
                    <tr key={order.id} className="group transition-colors hover:bg-slate-50/70 [&:last-child>td]:border-0">
                      <Td>
                        <Link to={`/orders/${order.id}`} className="font-medium text-slate-900 hover:text-brand-700">
                          {order.supplier_name ?? 'Поставщик'}
                        </Link>
                      </Td>
                      <Td>
                        <OrderBadges order={order} />
                      </Td>
                      <Td className="whitespace-nowrap text-slate-600">
                        {order.expected_at ? formatDate(order.expected_at) : <span className="text-slate-300">—</span>}
                      </Td>
                      <Td className="whitespace-nowrap text-slate-500">
                        {new Date(order.created_at).toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' })}
                      </Td>
                      <Td align="right" className="w-px">
                        <Link
                          to={`/orders/${order.id}`}
                          aria-label={`Открыть заказ: ${order.supplier_name ?? 'поставщик'}`}
                          className="inline-flex size-8 items-center justify-center rounded-lg text-slate-300 group-hover:text-slate-500 hover:bg-slate-100"
                        >
                          <ChevronRight aria-hidden="true" className="size-4" strokeWidth={1.75} />
                        </Link>
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          </>
        )}
      </Card>
    </div>
  )
}

/** Карточка заказа: текст заявки, отправка, приёмка (§5.4). */
export function OrderPage() {
  const { id = '' } = useParams()
  const { data, isLoading } = useOrder(id)
  const { data: me } = useMe()
  const send = useSendOrder()
  const cancel = useCancelOrder()
  const receive = useReceiveOrder()

  const [copied, setCopied] = useState(false)
  const [actual, setActual] = useState<Record<string, string>>({})
  const [receiving, setReceiving] = useState(false)

  useEffect(() => {
    if (!data?.lines) return
    const initial: Record<string, string> = {}
    for (const line of data.lines) {
      initial[line.item_id] = inputQty(line.qty_received ?? line.qty_ordered)
    }
    setActual(initial)
  }, [data?.id, data?.lines])

  if (isLoading || !data) {
    return (
      <div className="space-y-8">
        <Skeleton className="h-10 w-64" />
        <Skeleton className="h-72 w-full" />
      </div>
    )
  }

  const canManage = me?.tenant.permissions.manage_orders ?? false
  const error = send.error ?? cancel.error ?? receive.error

  const copyText = async () => {
    if (!data.text) return
    try {
      await navigator.clipboard.writeText(data.text)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Буфер обмена недоступен (не https или отказ): текст всё равно
      // виден на экране и его можно выделить руками.
      setCopied(false)
    }
  }

  const actions = (
    <>
      {canManage && data.status === 'draft' && (
        <>
          <Button variant="secondary" onClick={() => cancel.mutate(id)} loading={cancel.isPending}>
            Отменить
          </Button>
          <Button icon={Send} onClick={() => send.mutate(id)} loading={send.isPending}>
            Отправить поставщику
          </Button>
        </>
      )}

      {data.status === 'sent' &&
        (receiving ? (
          <>
            <Button variant="secondary" onClick={() => setReceiving(false)}>
              Отмена
            </Button>
            <Button
              icon={PackageCheck}
              loading={receive.isPending}
              onClick={() =>
                receive.mutate(
                  {
                    orderId: id,
                    lines: Object.entries(actual).map(([item_id, qty_received]) => ({ item_id, qty_received })),
                  },
                  { onSuccess: () => setReceiving(false) },
                )
              }
            >
              Провести приёмку
            </Button>
          </>
        ) : (
          <>
            {canManage && (
              <Button variant="secondary" onClick={() => cancel.mutate(id)} loading={cancel.isPending}>
                Отменить
              </Button>
            )}
            <Button icon={PackageCheck} onClick={() => setReceiving(true)}>
              Принять поставку
            </Button>
          </>
        ))}
    </>
  )

  return (
    <div>
      <PageHeader
        back={<BackLink to="/orders">Заказы</BackLink>}
        title={data.supplier_name ?? 'Заказ'}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <OrderBadges order={data} />
            {data.expected_at && (
              <span>поставка {formatDay(data.expected_at, me?.tenant.today ?? data.expected_at)}</span>
            )}
          </span>
        }
        actions={actions}
      />

      <div className="space-y-6">
        {error && <Alert kind="error">{error.message}</Alert>}
        {data.late && (
          <Alert kind="warning" title="Поставка опаздывает">
            Заказ ожидался {data.expected_at ? formatDate(data.expected_at) : ''} и ещё не принят.
          </Alert>
        )}
        {receiving && (
          <Alert kind="info">Проверьте, сколько привезли на самом деле: приход запишется по этим цифрам.</Alert>
        )}

        <Card flush title="Строки заказа" description={`${data.lines?.length ?? 0} поз.`}>
          <Table>
            <thead>
              <tr>
                <Th>Позиция</Th>
                <Th align="right">Заказано</Th>
                <Th align="right">{data.status === 'sent' && receiving ? 'Привезли' : 'Принято'}</Th>
              </tr>
            </thead>
            <tbody>
              {data.lines?.map((line) => {
                const mismatch =
                  line.qty_received !== undefined && Number(line.qty_received) !== Number(line.qty_ordered)
                return (
                  <tr key={line.item_id} className="[&:last-child>td]:border-0">
                    <Td>
                      <Link to={`/items/${line.item_id}`} className="font-medium text-slate-900 hover:text-brand-700">
                        {line.item_name}
                      </Link>
                    </Td>
                    <Td align="right" className="whitespace-nowrap text-slate-900">
                      {line.purchase_unit && Number(line.unit_factor) > 0 ? (
                        <>
                          <span className="font-medium">
                            {formatQty(Number(line.qty_ordered) / Number(line.unit_factor))} {line.purchase_unit}
                          </span>
                          <span className="block text-[13px] text-slate-500">
                            {formatQtyWithUnit(line.qty_ordered, line.base_unit)}
                          </span>
                        </>
                      ) : (
                        <span className="font-medium">{formatQtyWithUnit(line.qty_ordered, line.base_unit)}</span>
                      )}
                    </Td>
                    <Td align="right">
                      {data.status === 'sent' && receiving ? (
                        <Input
                          inputMode="decimal"
                          aria-label={`Привезли: ${line.item_name}`}
                          value={actual[line.item_id] ?? ''}
                          onChange={(e) =>
                            setActual((prev) => ({ ...prev, [line.item_id]: e.target.value.replace(',', '.') }))
                          }
                          className="ml-auto w-28 text-right tabular-nums"
                        />
                      ) : line.qty_received !== undefined ? (
                        <span className={mismatch ? 'font-semibold text-amber-700' : 'text-slate-600'}>
                          {formatQty(line.qty_received)}
                        </span>
                      ) : (
                        <span className="text-slate-300">—</span>
                      )}
                    </Td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        </Card>

        {data.text && data.status !== 'draft' && (
          <Card
            title="Текст заявки"
            description="Готов к отправке поставщику"
            action={
              <Button variant="secondary" size="sm" icon={copied ? Check : Copy} onClick={copyText}>
                {copied ? 'Скопировано' : 'Скопировать'}
              </Button>
            }
          >
            <pre className="rounded-lg bg-slate-50 p-4 font-sans text-sm leading-relaxed whitespace-pre-wrap text-slate-700">
              {data.text}
            </pre>
          </Card>
        )}
      </div>
    </div>
  )
}
