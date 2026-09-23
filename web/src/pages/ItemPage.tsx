import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  Area,
  Bar,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { MovementDialog } from '@/components/MovementDialog'
import { StatusBadge } from '@/components/StatusBadge'
import { Alert, Button, Card, Skeleton } from '@/components/ui'
import { formatDay, formatQty, formatQtyWithUnit, unitLabel } from '@/lib/format'
import { useInsights, useMovements } from '@/lib/queries'
import type { Insights } from '@/lib/types'

/** Карточка позиции (§6.2). */
export function ItemPage() {
  const { id = '' } = useParams()
  const { data, isLoading, error } = useInsights(id)
  const movements = useMovements({ itemId: id, limit: 10 })
  const [movementOpen, setMovementOpen] = useState(false)

  if (error) return <Alert kind="error">{error.message}</Alert>
  if (isLoading || !data) return <Skeleton className="h-96 w-full" />

  const unit = data.item.base_unit

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <Link to="/" className="text-sm text-slate-500 hover:underline">
            ← К дашборду
          </Link>
          <h1 className="mt-1 text-2xl font-semibold text-slate-900">{data.item.name}</h1>
          <p className="text-sm text-slate-500">
            {data.item.category_name ?? 'Без категории'}
            {data.item.supplier_name ? ` · ${data.item.supplier_name}` : ''}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <StatusBadge status={data.status.code} />
          <Button onClick={() => setMovementOpen(true)}>Движение</Button>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card title="Сейчас">
          <dl className="space-y-2 text-sm">
            <Row label="На складе" value={formatQtyWithUnit(data.status.on_hand, unit)} />
            <Row label="В пути" value={formatQtyWithUnit(data.status.on_order, unit)} />
            <Row
              label="Хватит до"
              value={data.status.stockout_date ? formatDay(data.status.stockout_date, data.today) : '—'}
            />
            <Row
              label="Рекомендуемый заказ"
              value={
                Number(data.status.recommended_qty) > 0
                  ? data.status.purchase_qty && data.item.purchase_unit
                    ? `${formatQty(data.status.purchase_qty)} ${data.item.purchase_unit} (${formatQtyWithUnit(data.status.recommended_qty, unit)})`
                    : formatQtyWithUnit(data.status.recommended_qty, unit)
                  : 'не нужен'
              }
            />
          </dl>
        </Card>

        <Card title="Почему такой статус" className="lg:col-span-2">
          <Explanation data={data} />
        </Card>
      </div>

      <Card title="Точность прогноза">
        <Accuracy accuracy={data.accuracy} />
      </Card>

      <Card title="Спрос: факт за 60 дней и прогноз на 14">
        <DemandChart data={data} />
      </Card>

      <Card title="Остаток: проекция на 14 дней">
        <StockChart data={data} />
      </Card>

      <Card
        title="Последние движения"
        action={
          <Link to={`/movements?item=${id}`} className="text-sm text-brand-700 hover:underline">
            Весь журнал →
          </Link>
        }
      >
        {movements.data?.items.length ? (
          <ul className="divide-y divide-slate-100">
            {movements.data.items.map((m) => (
              <li key={m.id} className="flex items-center justify-between gap-3 py-2 text-sm">
                <span>
                  <span className="font-medium text-slate-900">{m.type_label}</span>
                  {m.reason_label && <span className="text-slate-500"> · {m.reason_label}</span>}
                  <span className="block text-slate-500">
                    {new Date(m.occurred_at).toLocaleString('ru-RU')}
                    {m.author_name ? ` · ${m.author_name}` : ''}
                  </span>
                </span>
                <span
                  className={`tabular-nums font-medium ${
                    Number(m.qty) < 0 ? 'text-red-700' : 'text-emerald-700'
                  }`}
                >
                  {Number(m.qty) > 0 ? '+' : ''}
                  {formatQty(m.qty)}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="py-4 text-sm text-slate-500">Движений пока нет.</p>
        )}
      </Card>

      <MovementDialog
        item={
          movementOpen
            ? {
                id: data.item.id,
                name: data.item.name,
                base_unit: unit,
                on_hand: data.status.on_hand,
              }
            : null
        }
        onClose={() => setMovementOpen(false)}
      />
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-3">
      <dt className="text-slate-500">{label}</dt>
      <dd className="text-right font-medium text-slate-900">{value}</dd>
    </div>
  )
}

/**
 * Блок «почему такой статус» (§6.2): расчёт словами и числами.
 * Без него владелец не доверится рекомендации.
 */
function Explanation({ data }: { data: Insights }) {
  const e = data.status.explanation
  const unit = data.item.base_unit

  if (data.accuracy.model === 'M0') {
    return (
      <p className="text-sm text-slate-700">
        Данных пока мало — меньше недели, поэтому прогноз не строится.
        Статус считается по ручному минимальному остатку:{' '}
        <strong>{formatQtyWithUnit(data.item.manual_min_qty, unit)}</strong>.
        Сейчас на складе {formatQtyWithUnit(data.status.on_hand, unit)}.
      </p>
    )
  }

  const shortfall = Number(e.shortfall)

  return (
    <div className="space-y-2 text-sm text-slate-700">
      <p>
        Сейчас <strong>{formatQtyWithUnit(e.on_hand, unit)}</strong>, в пути{' '}
        <strong>{formatQtyWithUnit(e.on_order, unit)}</strong>.
        {e.d2 && (
          <>
            {' '}
            До поставки по следующему заказу ({formatDay(e.d2, data.today)}) нужно{' '}
            <strong>{formatQtyWithUnit(e.need_until_d2, unit)}</strong> плюс страховой запас{' '}
            <strong>{formatQtyWithUnit(e.safety_stock, unit)}</strong>.
          </>
        )}
      </p>

      {shortfall > 0 ? (
        <p>
          Не хватает <strong>{formatQtyWithUnit(e.shortfall, unit)}</strong> →{' '}
          заказать{' '}
          <strong>
            {data.status.purchase_qty && data.item.purchase_unit
              ? `${formatQty(data.status.purchase_qty)} ${data.item.purchase_unit} (${formatQtyWithUnit(e.recommended_qty, unit)})`
              : formatQtyWithUnit(e.recommended_qty, unit)}
          </strong>
          .
        </p>
      ) : e.can_wait ? (
        <p>
          Заказ сегодня и завтра приедет в один и тот же день
          {e.d1 ? ` (${formatDay(e.d1, data.today)})` : ''} — ждать ничего не стоит.
        </p>
      ) : (
        <p>Запаса хватает: целевой уровень {formatQtyWithUnit(e.target_level, unit)} уже покрыт.</p>
      )}

      <p className="text-slate-500">
        Страховой запас = z · σ · √n, где z = {e.z.toFixed(2)} (уровень сервиса{' '}
        {data.item.service_level}%), σ = {e.sigma.toFixed(2)}, n = {e.days_to_d2} дн.
      </p>
    </div>
  )
}

function Accuracy({ accuracy }: { accuracy: Insights['accuracy'] }) {
  if (accuracy.model === 'M0') {
    return (
      <p className="text-sm text-slate-600">
        Модель не построена: нужно не меньше 7 дней данных. Сейчас накоплено{' '}
        {accuracy.days_with_data}.
      </p>
    )
  }

  const bias = Math.round(accuracy.bias * 100)

  return (
    <p className="text-sm text-slate-700">
      <strong className="text-lg">{Math.round(accuracy.accuracy * 100)}%</strong> за{' '}
      {accuracy.window_days} дней, модель {accuracy.model}
      {bias !== 0 && (
        <>
          , {bias > 0 ? 'завышает' : 'занижает'} на {Math.abs(bias)}%
        </>
      )}
      .
      <span className="mt-1 block text-slate-500">
        Точность = 1 − WAPE. Данных в ряду: {accuracy.days_with_data} дней.
      </span>
    </p>
  )
}

/** DemandChart — столбцы факта и линия прогноза с полосой ±z·σ (§6.2). */
function DemandChart({ data }: { data: Insights }) {
  const points = [
    ...data.history.map((h) => ({
      day: h.day,
      fact: h.has_data && !h.stockout ? Number(h.qty) : null,
      // Дни дефицита и без данных показываем отдельно: модель на них
      // не училась, и владелец должен это видеть.
      excluded: !h.has_data || h.stockout ? Number(h.qty) : null,
      forecast: null as number | null,
      band: null as [number, number] | null,
    })),
    ...data.forecast.map((f) => ({
      day: f.day,
      fact: null,
      excluded: null,
      forecast: Number(f.qty),
      band: [Number(f.lower), Number(f.upper)] as [number, number],
    })),
  ]

  return (
    <div className="h-64 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={points} margin={{ top: 5, right: 5, bottom: 5, left: -20 }}>
          <CartesianGrid strokeDasharray="2 4" stroke="#DBD7D2" />
          <XAxis dataKey="day" tickFormatter={shortDay} tick={{ fontSize: 11 }} minTickGap={24} />
          <YAxis tick={{ fontSize: 11 }} />
          <Tooltip
            formatter={(value: unknown, name: string) => [formatNumber(value), chartLabel(name)]}
            labelFormatter={(day: string) => formatDay(day, data.today)}
          />
          <Legend formatter={chartLabel} />
          <Area dataKey="band" stroke="none" fill="#2B2A28" fillOpacity={0.08} name="band" />
          <Bar dataKey="fact" fill="#2B2A28" name="fact" />
          <Bar dataKey="excluded" fill="#D2CEC9" name="excluded" />
          <Line dataKey="forecast" stroke="#2B2A28" strokeWidth={2} strokeDasharray="5 3" dot={false} name="forecast" />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}

/** StockChart — проекция остатка с линией страхового запаса (§6.2). */
function StockChart({ data }: { data: Insights }) {
  const points = data.projection.map((p) => ({
    day: p.day,
    stock: Number(p.on_hand),
    incoming: Number(p.incoming) || null,
  }))

  const safety = Number(data.status.safety_stock)

  return (
    <div className="h-64 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={points} margin={{ top: 5, right: 5, bottom: 5, left: -20 }}>
          <CartesianGrid strokeDasharray="2 4" stroke="#DBD7D2" />
          <XAxis dataKey="day" tickFormatter={shortDay} tick={{ fontSize: 11 }} minTickGap={24} />
          <YAxis tick={{ fontSize: 11 }} />
          <Tooltip
            formatter={(value: unknown, name: string) => [formatNumber(value), chartLabel(name)]}
            labelFormatter={(day: string) => formatDay(day, data.today)}
          />
          <Legend formatter={chartLabel} />
          {safety > 0 && (
            <ReferenceLine
              y={safety}
              stroke="#2B2A28"
              strokeDasharray="4 4"
              label={{ value: 'страховой запас', fontSize: 11, position: 'insideTopRight' }}
            />
          )}
          <ReferenceLine y={0} stroke="#111111" strokeWidth={2} />
          <Line dataKey="stock" stroke="#2B2A28" strokeWidth={2} dot={false} name="stock" />
          <Bar dataKey="incoming" fill="#FFFFFF" stroke="#2B2A28" strokeWidth={1} name="incoming" />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}

const chartLabels: Record<string, string> = {
  fact: 'Факт',
  excluded: 'Нет данных / дефицит',
  forecast: 'Прогноз',
  band: 'Разброс ±z·σ',
  stock: 'Остаток',
  incoming: 'Поставка',
}

function chartLabel(name: string): string {
  return chartLabels[name] ?? name
}

function shortDay(day: string): string {
  const [, month, date] = day.split('-')
  return `${date}.${month}`
}

function formatNumber(value: unknown): string {
  if (Array.isArray(value)) {
    return `${formatQty(String(value[0]))} … ${formatQty(String(value[1]))}`
  }
  return typeof value === 'number' ? formatQty(value) : '—'
}

void unitLabel
