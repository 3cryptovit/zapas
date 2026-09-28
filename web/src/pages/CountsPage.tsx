import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ChevronRight, ClipboardCheck, Plus } from 'lucide-react'

import { Alert, BackLink, Badge, Button, Card, EmptyState, Input, PageHeader, Skeleton } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { formatQty, formatQtyWithUnit, inputQty } from '@/lib/format'
import { useCount, useCounts, useCreateCount, usePostCount, useSaveCountLines } from '@/lib/queries'

/** Список пересчётов (§6, экран «Инвентаризация»). */
export function CountsPage() {
  const { data, isLoading } = useCounts()
  const create = useCreateCount()

  return (
    <div>
      <PageHeader
        title="Инвентаризация"
        description="Пересчёт сверяет фактический остаток с учётным и сам создаёт корректировки."
        actions={
          <Button icon={Plus} onClick={() => create.mutate('Пересчёт')} loading={create.isPending}>
            Новый пересчёт
          </Button>
        }
      />

      {create.isError && (
        <div className="mb-6">
          <Alert kind="error">{create.error.message}</Alert>
        </div>
      )}

      <Card flush title="Пересчёты">
        {isLoading ? (
          <div className="px-5 pb-6 sm:px-6">
            <Skeleton className="h-32 w-full" />
          </div>
        ) : !data?.length ? (
          <EmptyState
            icon={ClipboardCheck}
            title="Пересчётов ещё не было"
            hint="Начните первый — займёт пару минут с телефона."
          />
        ) : (
          <ul className="divide-y divide-slate-100">
            {data.map((count) => (
              <li key={count.id}>
                <Link
                  to={`/counts/${count.id}`}
                  className="group flex items-center justify-between gap-4 px-5 py-4 transition-colors hover:bg-slate-50/70 sm:px-6"
                >
                  <span className="min-w-0">
                    <span className="block font-medium text-slate-900">{count.note || 'Пересчёт'}</span>
                    <span className="block text-[13px] text-slate-500">
                      {new Date(count.created_at).toLocaleString('ru-RU', { dateStyle: 'medium', timeStyle: 'short' })}
                    </span>
                  </span>
                  <span className="flex shrink-0 items-center gap-3">
                    {count.status === 'posted' ? (
                      <Badge className="bg-emerald-50 text-emerald-700">Проведён</Badge>
                    ) : (
                      <Badge className="bg-amber-50 text-amber-700">Черновик</Badge>
                    )}
                    <ChevronRight aria-hidden="true" className="size-4 text-slate-300 group-hover:text-slate-500" strokeWidth={1.75} />
                  </span>
                </Link>
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
      if (line.counted_qty !== undefined) initial[line.item_id] = inputQty(line.counted_qty)
    }
    setValues(initial)
  }, [data?.id, data?.lines])

  if (isLoading || !data) {
    return (
      <div className="space-y-8">
        <Skeleton className="h-10 w-64" />
        <Skeleton className="h-96 w-full" />
      </div>
    )
  }

  const posted = data.status === 'posted'
  const total = data.lines?.length ?? 0
  const entered = Object.values(values).filter((v) => v !== '').length

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
    <div>
      <PageHeader
        back={<BackLink to="/counts">Инвентаризация</BackLink>}
        title={data.note || 'Пересчёт'}
        description={
          posted
            ? `Проведён ${data.posted_at ? new Date(data.posted_at).toLocaleString('ru-RU', { dateStyle: 'medium', timeStyle: 'short' }) : ''}`
            : 'Черновик — остатки пока не изменены'
        }
        actions={
          !posted && (
            <>
              <Button variant="secondary" onClick={saveDraft} loading={save.isPending}>
                Сохранить черновик
              </Button>
              <Button onClick={() => runPost(false)} loading={post.isPending}>
                Провести
              </Button>
            </>
          )
        }
      />

      {postError && (
        <div className="mb-6">
          <Alert kind="warning" title={postError.title}>
            {postError.detail}
            {postError.type === '/errors/count-changed' && (
              <div className="mt-3">
                <Button variant="danger" size="sm" onClick={() => runPost(true)} loading={post.isPending}>
                  Всё равно провести
                </Button>
              </div>
            )}
          </Alert>
        </div>
      )}

      <Card
        flush
        title="Позиции"
        description={posted ? `Всего ${total}` : `Введено ${entered} из ${total} · Enter — к следующей`}
      >
        <ul className="divide-y divide-slate-100">
          {data.lines?.map((line, index) => {
            const value = values[line.item_id] ?? ''
            const diff = value === '' ? null : Number(value) - Number(line.current_qty)

            return (
              <li key={line.item_id} className="flex items-center gap-4 px-5 py-3 sm:px-6">
                <span className="min-w-0 flex-1">
                  <span className="block font-medium text-slate-900">{line.item_name}</span>
                  <span className="block text-[13px] text-slate-500 tabular-nums">
                    по учёту {formatQtyWithUnit(line.current_qty, line.base_unit)}
                  </span>
                </span>

                {/* На телефоне разница — под полем, у правого края: иначе она
                    уезжала под название и терялась. */}
                <span className="flex shrink-0 flex-col items-end gap-1 sm:flex-row sm:items-center sm:gap-4">
                  <Input
                    ref={(el: HTMLInputElement | null) => {
                      inputs.current[index] = el
                    }}
                    inputMode="decimal"
                    aria-label={`Факт: ${line.item_name}`}
                    placeholder="факт"
                    disabled={posted}
                    value={value}
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
                    className="w-28 text-right tabular-nums"
                  />

                  <span
                    className={`text-right text-[13px] tabular-nums sm:w-24 sm:text-sm ${
                      diff === null
                        ? 'text-slate-300'
                        : diff === 0
                          ? 'text-emerald-700'
                          : 'font-semibold text-amber-700'
                    }`}
                  >
                    {diff === null ? '—' : diff === 0 ? 'сходится' : `${diff > 0 ? '+' : '−'}${formatQty(Math.abs(diff))}`}
                  </span>
                </span>
              </li>
            )
          })}
        </ul>
      </Card>
    </div>
  )
}
