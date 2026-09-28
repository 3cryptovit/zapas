import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ArrowLeftRight, Undo2 } from 'lucide-react'

import {
  Alert,
  Badge,
  Button,
  Card,
  EmptyState,
  PageHeader,
  Select,
  SignedQty,
  Skeleton,
  Table,
  Td,
  Th,
} from '@/components/ui'
import { useItems, useMe, useMovements, useReverseMovement } from '@/lib/queries'
import type { Movement, MovementType } from '@/lib/types'

const typeOptions: { value: MovementType | 'all'; label: string }[] = [
  { value: 'all', label: 'Все типы' },
  { value: 'receipt', label: 'Приход' },
  { value: 'usage', label: 'Расход' },
  { value: 'writeoff', label: 'Списание' },
  { value: 'adjustment', label: 'Корректировка' },
  { value: 'reversal', label: 'Сторно' },
  { value: 'opening', label: 'Начальный остаток' },
]

/** Журнал движений с фильтрами (FR-11). */
export function MovementsPage() {
  const [params, setParams] = useSearchParams()
  const itemId = params.get('item') ?? ''
  const type = (params.get('type') as MovementType | null) ?? undefined

  const { data: items } = useItems()
  const { data, isLoading, error } = useMovements({ itemId: itemId || undefined, type, limit: 100 })
  const { data: me } = useMe()
  const reverse = useReverseMovement()
  const [reverseError, setReverseError] = useState('')

  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next)
  }

  // Сторнировать можно своё движение; владелец — любое (§2).
  const canReverse = (m: Movement) =>
    m.type !== 'reversal' && !m.reversed && (me?.user.role === 'owner' || m.created_by === me?.user.id)

  const onReverse = (m: Movement) => {
    setReverseError('')
    reverse.mutate(m.id, { onError: (err) => setReverseError(err.message) })
  }

  return (
    <div>
      <PageHeader
        title="Движения"
        description="Приходы, расходы, списания и корректировки. Записи не правятся — ошибка гасится сторно."
      />

      <Card
        flush
        title="Журнал"
        description={data ? `Записей: ${data.items.length}` : undefined}
        action={
          <div className="flex w-full flex-wrap gap-2 sm:w-auto">
            <div className="min-w-0 flex-1 sm:w-52 sm:flex-none">
              <Select aria-label="Позиция" value={itemId} onChange={(e) => setFilter('item', e.target.value)}>
                <option value="">Все позиции</option>
                {items?.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </Select>
            </div>
            <div className="w-40 shrink-0 sm:w-44">
              <Select
                aria-label="Тип движения"
                value={type ?? 'all'}
                onChange={(e) => setFilter('type', e.target.value === 'all' ? '' : e.target.value)}
              >
                {typeOptions.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
          </div>
        }
      >
        {(error || reverseError) && (
          <div className="px-5 pb-4 sm:px-6">
            <Alert kind="error">{error?.message ?? reverseError}</Alert>
          </div>
        )}

        {isLoading ? (
          <div className="px-5 pb-6 sm:px-6">
            <Skeleton className="h-48 w-full" />
          </div>
        ) : !data?.items.length ? (
          <EmptyState
            icon={ArrowLeftRight}
            title="Движений пока нет"
            hint="Запишите приход или расход с дашборда — здесь появится история."
          />
        ) : (
          <>
            {/* Телефон: список */}
            <ul className="divide-y divide-slate-100 md:hidden">
              {data.items.map((m) => (
                <li key={m.id} className="px-5 py-4">
                  <div className="flex items-start justify-between gap-3">
                    <ItemName m={m} />
                    <SignedQty qty={m.qty} />
                  </div>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-slate-500">
                    <Badge>{m.type_label}</Badge>
                    {m.reason_label && <span>{m.reason_label}</span>}
                    <span>{when(m)}</span>
                  </div>
                  {canReverse(m) && (
                    <div className="mt-3">
                      <Button variant="secondary" size="sm" icon={Undo2} onClick={() => onReverse(m)}>
                        Сторно
                      </Button>
                    </div>
                  )}
                </li>
              ))}
            </ul>

            {/* Десктоп: таблица */}
            <div className="hidden md:block">
              <Table>
                <thead>
                  <tr>
                    <Th>Позиция</Th>
                    <Th>Тип</Th>
                    <Th>Когда</Th>
                    <Th align="right">Количество</Th>
                    <Th>
                      <span className="sr-only">Действия</span>
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {data.items.map((m) => (
                    <tr key={m.id} className="transition-colors hover:bg-slate-50/70 [&:last-child>td]:border-0">
                      <Td>
                        <ItemName m={m} />
                        {m.comment && <span className="block text-[13px] text-slate-500">{m.comment}</span>}
                      </Td>
                      <Td>
                        <span className="flex flex-wrap items-center gap-2">
                          <Badge>{m.type_label}</Badge>
                          {m.reason_label && <span className="text-[13px] text-slate-500">{m.reason_label}</span>}
                          {m.reversed && <span className="text-[13px] text-slate-400">сторнировано</span>}
                        </span>
                      </Td>
                      <Td className="whitespace-nowrap text-slate-600">
                        {when(m)}
                        {m.author_name && <span className="block text-[13px] text-slate-500">{m.author_name}</span>}
                      </Td>
                      <Td align="right">
                        <SignedQty qty={m.qty} />
                      </Td>
                      <Td align="right" className="w-px">
                        {canReverse(m) && (
                          <Button variant="ghost" size="sm" icon={Undo2} onClick={() => onReverse(m)}>
                            Сторно
                          </Button>
                        )}
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          </>
        )}

        {data?.next_cursor && (
          <p className="border-t border-slate-100 px-5 py-4 text-[13px] text-slate-500 sm:px-6">
            Показаны последние {data.items.length}. Уточните фильтр, чтобы увидеть остальное.
          </p>
        )}
      </Card>
    </div>
  )
}

function ItemName({ m }: { m: Movement }) {
  return m.item_name ? (
    <Link to={`/items/${m.item_id}`} className="font-medium text-slate-900 hover:text-brand-700">
      {m.item_name}
    </Link>
  ) : (
    <span className="text-slate-400">—</span>
  )
}

function when(m: Movement): string {
  return new Date(m.occurred_at).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' })
}
