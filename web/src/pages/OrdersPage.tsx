import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { Alert, Button, Card, EmptyState, Input, Skeleton } from '@/components/ui'
import { formatDay, formatQty, formatQtyWithUnit } from '@/lib/format'
import {
  useCancelOrder,
  useMe,
  useOrder,
  useOrders,
  useReceiveOrder,
  useSendOrder,
} from '@/lib/queries'
import type { OrderStatus } from '@/lib/types'

// Стадия заказа — плотность заливки: пунктир у черновика, штриховка
// у отправленного (в пути), сплошная заливка у принятого.
const statusTone: Record<OrderStatus, string> = {
  draft: 'border border-dashed border-slate-400 text-slate-600',
  sent: 'fill-sparse border border-slate-700 text-slate-900',
  received: 'fill-solid border border-slate-900',
  cancelled: 'border border-slate-300 text-slate-400 line-through',
}

/** Список заказов (§6). */
export function OrdersPage() {
  const [filter, setFilter] = useState<OrderStatus | undefined>(undefined)
  const { data, isLoading } = useOrders(filter)

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold text-slate-900">Заказы</h1>

      <div className="flex flex-wrap gap-2">
        {([undefined, 'draft', 'sent', 'received'] as const).map((value) => (
          <Button
            key={value ?? 'all'}
            variant={filter === value ? 'primary' : 'secondary'}
            className="min-h-9 px-3 text-sm"
            onClick={() => setFilter(value)}
          >
            {value === undefined
              ? 'Все'
              : value === 'draft'
                ? 'Черновики'
                : value === 'sent'
                  ? 'В пути'
                  : 'Принятые'}
          </Button>
        ))}
      </div>

      <Card title="Список">
        {isLoading ? (
          <Skeleton className="h-32 w-full" />
        ) : !data?.length ? (
          <EmptyState
            title="Заказов пока нет"
            hint="Заказ собирается одной кнопкой из блока «Заказать сегодня» на дашборде."
          />
        ) : (
          <ul className="divide-y divide-slate-100">
            {data.map((order) => (
              <li key={order.id} className="flex flex-wrap items-center gap-3 py-3 text-sm">
                <Link
                  to={`/orders/${order.id}`}
                  className="font-medium text-slate-900 hover:underline"
                >
                  {order.supplier_name ?? 'Поставщик'}
                </Link>
                <span className={`px-2 py-1 text-[0.625rem] font-semibold uppercase tracking-[0.12em] ${statusTone[order.status]}`}>
                  {order.status_label}
                </span>
                {order.late && (
                  <span className="rounded-full bg-red-100 px-2.5 py-1 text-xs font-medium text-red-900">
                    опаздывает
                  </span>
                )}
                <span className="ml-auto text-slate-500">
                  {order.expected_at ? `поставка ${order.expected_at}` : ''}
                  {' · '}
                  {new Date(order.created_at).toLocaleDateString('ru-RU')}
                </span>
              </li>
            ))}
          </ul>
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
      initial[line.item_id] = line.qty_received ?? line.qty_ordered
    }
    setActual(initial)
  }, [data?.id, data?.lines])

  if (isLoading || !data) return <Skeleton className="h-96 w-full" />

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

  return (
    <div className="space-y-4">
      <div>
        <Link to="/orders" className="text-sm text-slate-500 hover:underline">
          ← К заказам
        </Link>
        <h1 className="mt-1 text-2xl font-semibold text-slate-900">
          {data.supplier_name ?? 'Заказ'}
        </h1>
        <p className="text-sm text-slate-500">
          {data.status_label}
          {data.expected_at && ` · поставка ${formatDay(data.expected_at, data.expected_at)}`}
        </p>
      </div>

      {error && <Alert kind="error">{error.message}</Alert>}
      {data.late && (
        <Alert kind="warning" title="Поставка опаздывает">
          Заказ ожидался {data.expected_at} и ещё не принят.
        </Alert>
      )}

      <Card title="Строки заказа">
        <ul className="divide-y divide-slate-100">
          {data.lines?.map((line) => (
            <li key={line.item_id} className="flex flex-wrap items-center gap-3 py-3 text-sm">
              <Link to={`/items/${line.item_id}`} className="flex-1 font-medium text-slate-900 hover:underline">
                {line.item_name}
              </Link>

              <span className="text-slate-600">
                заказано {formatQtyWithUnit(line.qty_ordered, line.base_unit)}
                {line.purchase_unit && Number(line.unit_factor) > 0 && (
                  <span className="text-slate-400">
                    {' '}
                    ({formatQty(Number(line.qty_ordered) / Number(line.unit_factor))} {line.purchase_unit})
                  </span>
                )}
              </span>

              {data.status === 'sent' && receiving ? (
                <Input
                  inputMode="decimal"
                  value={actual[line.item_id] ?? ''}
                  onChange={(e) =>
                    setActual((prev) => ({
                      ...prev,
                      [line.item_id]: e.target.value.replace(',', '.'),
                    }))
                  }
                  className="w-28 text-right"
                />
              ) : line.qty_received !== undefined ? (
                <span
                  className={`tabular-nums ${
                    Number(line.qty_received) !== Number(line.qty_ordered)
                      ? 'font-medium text-amber-700'
                      : 'text-slate-600'
                  }`}
                >
                  принято {formatQty(line.qty_received)}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      </Card>

      {data.text && data.status !== 'draft' && (
        <Card
          title="Текст заявки"
          action={
            <Button variant="secondary" className="min-h-9 text-sm" onClick={copyText}>
              {copied ? 'Скопировано' : 'Скопировать'}
            </Button>
          }
        >
          <pre className="whitespace-pre-wrap rounded-lg bg-slate-50 p-3 text-sm text-slate-800">
            {data.text}
          </pre>
        </Card>
      )}

      <div className="flex flex-wrap justify-end gap-2">
        {canManage && data.status === 'draft' && (
          <>
            <Button variant="secondary" onClick={() => cancel.mutate(id)} loading={cancel.isPending}>
              Отменить
            </Button>
            <Button onClick={() => send.mutate(id)} loading={send.isPending}>
              Отправить поставщику
            </Button>
          </>
        )}

        {data.status === 'sent' && (
          <>
            {canManage && (
              <Button variant="secondary" onClick={() => cancel.mutate(id)} loading={cancel.isPending}>
                Отменить
              </Button>
            )}
            {receiving ? (
              <>
                <Button variant="secondary" onClick={() => setReceiving(false)}>
                  Отмена
                </Button>
                <Button
                  loading={receive.isPending}
                  onClick={() =>
                    receive.mutate(
                      {
                        orderId: id,
                        lines: Object.entries(actual).map(([item_id, qty_received]) => ({
                          item_id,
                          qty_received,
                        })),
                      },
                      { onSuccess: () => setReceiving(false) },
                    )
                  }
                >
                  Провести приёмку
                </Button>
              </>
            ) : (
              <Button onClick={() => setReceiving(true)}>Принять поставку</Button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
