import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { Alert, Button, Card, EmptyState, Select, Skeleton } from '@/components/ui'
import { formatQty } from '@/lib/format'
import { useItems, useMe, useMovements, useReverseMovement } from '@/lib/queries'
import type { MovementType } from '@/lib/types'

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

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold text-slate-900">Движения</h1>

      <Card
        title={`Журнал${data ? ` (${data.items.length})` : ''}`}
        action={
          <div className="flex flex-wrap gap-2">
            <Select
              value={itemId}
              onChange={(e) => setFilter('item', e.target.value)}
              className="min-h-9 w-48 text-sm"
            >
              <option value="">Все позиции</option>
              {items?.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </Select>
            <Select
              value={type ?? 'all'}
              onChange={(e) => setFilter('type', e.target.value === 'all' ? '' : e.target.value)}
              className="min-h-9 w-44 text-sm"
            >
              {typeOptions.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </Select>
          </div>
        }
      >
        {error && <Alert kind="error">{error.message}</Alert>}
        {reverseError && <Alert kind="error">{reverseError}</Alert>}

        {isLoading ? (
          <Skeleton className="h-48 w-full" />
        ) : !data?.items.length ? (
          <EmptyState
            title="Движений пока нет"
            hint="Запишите приход или расход с дашборда — здесь появится история."
          />
        ) : (
          <ul className="divide-y divide-slate-100">
            {data.items.map((m) => {
              // Сторнировать можно своё движение; владелец — любое (§2).
              const canReverse =
                m.type !== 'reversal' &&
                !m.reversed &&
                (me?.user.role === 'owner' || m.created_by === me?.user.id)

              return (
                <li key={m.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-3 text-sm">
                  <span className="w-full font-medium text-slate-900 sm:w-auto">
                    {m.item_name ? (
                      <Link to={`/items/${m.item_id}`} className="hover:underline">
                        {m.item_name}
                      </Link>
                    ) : (
                      '—'
                    )}
                  </span>

                  <span className="rounded bg-slate-100 px-2 py-0.5 text-xs text-slate-700">
                    {m.type_label}
                  </span>
                  {m.reason_label && <span className="text-slate-500">{m.reason_label}</span>}

                  <span className="text-slate-500">
                    {new Date(m.occurred_at).toLocaleString('ru-RU')}
                    {m.author_name ? ` · ${m.author_name}` : ''}
                  </span>

                  <span
                    className={`ml-auto tabular-nums font-medium ${
                      Number(m.qty) < 0 ? 'text-red-700' : 'text-emerald-700'
                    }`}
                  >
                    {Number(m.qty) > 0 ? '+' : ''}
                    {formatQty(m.qty)}
                  </span>

                  {canReverse && (
                    <Button
                      variant="ghost"
                      className="min-h-9 px-2 text-xs"
                      onClick={() => {
                        setReverseError('')
                        reverse.mutate(m.id, {
                          onError: (err) => setReverseError(err.message),
                        })
                      }}
                    >
                      Сторно
                    </Button>
                  )}
                </li>
              )
            })}
          </ul>
        )}

        {data?.next_cursor && (
          <p className="pt-3 text-sm text-slate-500">
            Показаны последние {data.items.length}. Уточните фильтр, чтобы увидеть остальное.
          </p>
        )}
      </Card>
    </div>
  )
}
