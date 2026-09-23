import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { Alert, Button, Card, EmptyState, Input, Skeleton } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { formatQty, formatQtyWithUnit } from '@/lib/format'
import { useCount, useCounts, useCreateCount, usePostCount, useSaveCountLines } from '@/lib/queries'

/** Список пересчётов (§6, экран «Инвентаризация»). */
export function CountsPage() {
  const { data, isLoading } = useCounts()
  const create = useCreateCount()

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold text-slate-900">Инвентаризация</h1>
        <Button onClick={() => create.mutate('Пересчёт')} loading={create.isPending}>
          Новый пересчёт
        </Button>
      </div>

      {create.isError && <Alert kind="error">{create.error.message}</Alert>}

      <Card title="Пересчёты">
        {isLoading ? (
          <Skeleton className="h-32 w-full" />
        ) : !data?.length ? (
          <EmptyState
            title="Пересчётов ещё не было"
            hint="Пересчёт сверяет фактический остаток с учётным и сам создаёт корректировки."
          />
        ) : (
          <ul className="divide-y divide-slate-100">
            {data.map((count) => (
              <li key={count.id} className="flex items-center justify-between gap-3 py-3 text-sm">
                <span>
                  <Link to={`/counts/${count.id}`} className="font-medium text-slate-900 hover:underline">
                    {count.note || 'Пересчёт'}
                  </Link>
                  <span className="block text-slate-500">
                    {new Date(count.created_at).toLocaleString('ru-RU')}
                  </span>
                </span>
                <span
                  className={`rounded-full px-2.5 py-1 text-xs font-medium ${
                    count.status === 'posted'
                      ? 'bg-emerald-100 text-emerald-900'
                      : 'bg-amber-100 text-amber-900'
                  }`}
                >
                  {count.status === 'posted' ? 'Проведён' : 'Черновик'}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}

/**
 * Форма пересчёта (FR-15): рассчитана на телефон — крупные поля,
 * переход к следующей позиции по Enter.
 */
export function CountPage() {
  const { id = '' } = useParams()
  const { data, isLoading } = useCount(id)
  const save = useSaveCountLines()
  const post = usePostCount()

  const [values, setValues] = useState<Record<string, string>>({})
  const [postError, setPostError] = useState<ApiError | null>(null)
  const inputs = useRef<(HTMLInputElement | null)[]>([])

  useEffect(() => {
    if (!data?.lines) return
    const initial: Record<string, string> = {}
    for (const line of data.lines) {
      if (line.counted_qty !== undefined) initial[line.item_id] = line.counted_qty
    }
    setValues(initial)
  }, [data?.id, data?.lines])

  if (isLoading || !data) return <Skeleton className="h-96 w-full" />

  const posted = data.status === 'posted'

  const saveDraft = () => {
    const lines = Object.entries(values)
      .filter(([, v]) => v !== '')
      .map(([item_id, counted_qty]) => ({ item_id, counted_qty }))
    if (lines.length) save.mutate({ countId: id, lines })
  }

  const runPost = (force: boolean) => {
    setPostError(null)
    // Сначала сохраняем введённое, потом проводим: иначе проведение
    // не увидит последних цифр.
    const lines = Object.entries(values)
      .filter(([, v]) => v !== '')
      .map(([item_id, counted_qty]) => ({ item_id, counted_qty }))

    const doPost = () =>
      post.mutate(
        { countId: id, force },
        { onError: (err) => setPostError(err instanceof ApiError ? err : null) },
      )

    if (lines.length) {
      save.mutate({ countId: id, lines }, { onSuccess: doPost })
    } else {
      doPost()
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <Link to="/counts" className="text-sm text-slate-500 hover:underline">
          ← К списку пересчётов
        </Link>
        <h1 className="mt-1 text-2xl font-semibold text-slate-900">
          {data.note || 'Пересчёт'}
        </h1>
        <p className="text-sm text-slate-500">
          {posted
            ? `Проведён ${data.posted_at ? new Date(data.posted_at).toLocaleString('ru-RU') : ''}`
            : 'Черновик — остатки пока не изменены'}
        </p>
      </div>

      {postError && (
        <Alert kind="warning" title={postError.title}>
          {postError.detail}
          {postError.type === '/errors/count-changed' && (
            <div className="mt-2">
              <Button variant="danger" onClick={() => runPost(true)} loading={post.isPending}>
                Всё равно провести
              </Button>
            </div>
          )}
        </Alert>
      )}

      <Card title={`Позиции (${data.lines?.length ?? 0})`}>
        <ul className="divide-y divide-slate-100">
          {data.lines?.map((line, index) => {
            const entered = values[line.item_id] ?? ''
            const diff =
              entered === '' ? null : Number(entered) - Number(line.current_qty)

            return (
              <li key={line.item_id} className="py-3">
                <div className="flex flex-wrap items-center gap-3">
                  <span className="min-w-40 flex-1 font-medium text-slate-900">
                    {line.item_name}
                  </span>
                  <span className="text-sm text-slate-500">
                    учёт {formatQtyWithUnit(line.current_qty, line.base_unit)}
                  </span>

                  <Input
                    ref={(el: HTMLInputElement | null) => {
                      inputs.current[index] = el
                    }}
                    inputMode="decimal"
                    placeholder="факт"
                    disabled={posted}
                    value={entered}
                    onChange={(e) =>
                      setValues((prev) => ({
                        ...prev,
                        [line.item_id]: e.target.value.replace(',', '.'),
                      }))
                    }
                    onKeyDown={(e) => {
                      // Enter переводит к следующей позиции: так пересчёт
                      // идёт одной рукой с телефона (FR-15).
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        inputs.current[index + 1]?.focus()
                      }
                    }}
                    className="w-28 text-right"
                  />

                  <span
                    className={`w-24 text-right text-sm tabular-nums ${
                      diff === null ? 'text-slate-400' : diff === 0 ? 'text-slate-500' : 'text-amber-700'
                    }`}
                  >
                    {diff === null ? '—' : diff === 0 ? 'сходится' : `${diff > 0 ? '+' : ''}${formatQty(diff)}`}
                  </span>
                </div>
              </li>
            )
          })}
        </ul>
      </Card>

      {!posted && (
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="secondary" onClick={saveDraft} loading={save.isPending}>
            Сохранить черновик
          </Button>
          <Button onClick={() => runPost(false)} loading={post.isPending}>
            Провести
          </Button>
        </div>
      )}
    </div>
  )
}
