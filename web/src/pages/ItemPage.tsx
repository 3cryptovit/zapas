import { useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeftRight, ArrowRight } from 'lucide-react'
import {
  Area,
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type TooltipProps,
} from 'recharts'

import { MovementDialog } from '@/components/MovementDialog'
import { StatusBadge } from '@/components/StatusBadge'
import { Alert, BackLink, Button, Card, PageHeader, SignedQty, Skeleton } from '@/components/ui'
import { formatDate, formatDay, formatQty, formatQtyWithUnit, formatRelative } from '@/lib/format'
import { useInsights, useMovements } from '@/lib/queries'
import type { Insights } from '@/lib/types'

/*
 * Цвета графиков. Recharts принимает только готовые значения, поэтому они
 * продублированы здесь из токенов styles.css — те же oklch.
 */
const C = {
  brand: 'oklch(0.515 0.13 262)',
  brandSoft: 'oklch(0.94 0.028 262)',
  fact: 'oklch(0.715 0.011 260)',
  excluded: 'oklch(0.925 0.005 260)',
  grid: 'oklch(0.968 0.003 260)',
  axis: 'oklch(0.545 0.015 260)',
  zero: 'oklch(0.872 0.007 260)',
  amber: 'oklch(0.77 0.12 78)',
  green: 'oklch(0.64 0.11 158)',
}

/** Карточка позиции (§6.2). */
export function ItemPage() {
  const { id = '' } = useParams()
  const { data, isLoading, error } = useInsights(id)
  const movements = useMovements({ itemId: id, limit: 10 })
  const [movementOpen, setMovementOpen] = useState(false)

  if (error) return <Alert kind="error">{error.message}</Alert>
  if (isLoading || !data) {
    return (
      <div className="space-y-8">
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-28 w-full" />
        <Skeleton className="h-72 w-full" />
      </div>
    )
  }

  const unit = data.item.base_unit
  const recommended = Number(data.status.recommended_qty) > 0

  return (
    <div>
      <PageHeader
        back={<BackLink to="/">Дашборд</BackLink>}
        title={data.item.name}
        description={
          [data.item.category_name ?? 'Без категории', data.item.supplier_name].filter(Boolean).join(' · ')
        }
        actions={
          <>
            <StatusBadge status={data.status.code} />
            <Button icon={ArrowLeftRight} onClick={() => setMovementOpen(true)}>
              Движение
            </Button>
          </>
        }
      />

      <div className="space-y-6">
        {/* Главные числа: одной строкой, от наличия к действию. */}
        <section className="grid grid-cols-2 rounded-xl border border-slate-200 bg-white shadow-card lg:grid-cols-4">
          <Metric label="На складе" value={formatQtyWithUnit(data.status.on_hand, unit)} />
          <Metric
            label="В пути"
            value={Number(data.status.on_order) > 0 ? formatQtyWithUnit(data.status.on_order, unit) : '—'}
            muted={Number(data.status.on_order) <= 0}
          />
          <Metric
            label="Хватит до"
            value={data.status.stockout_date ? formatDate(data.status.stockout_date) : '—'}
            hint={data.status.stockout_date ? formatRelative(data.status.stockout_date, data.today) || undefined : undefined}
            muted={!data.status.stockout_date}
          />
          <Metric
            label="Рекомендуемый заказ"
            value={
              recommended
                ? data.status.purchase_qty && data.item.purchase_unit
                  ? `${formatQty(data.status.purchase_qty)} ${data.item.purchase_unit}`
                  : formatQtyWithUnit(data.status.recommended_qty, unit)
                : 'не нужен'
            }
            hint={
              recommended && data.status.purchase_qty && data.item.purchase_unit
                ? formatQtyWithUnit(data.status.recommended_qty, unit)
                : undefined
            }
            muted={!recommended}
            accent={recommended}
          />
        </section>

        <div className="grid gap-6 lg:grid-cols-3">
          <Card title="Почему такой статус" className="lg:col-span-2">
            <Explanation data={data} />
          </Card>
          <Card title="Точность прогноза">
            <Accuracy accuracy={data.accuracy} />
          </Card>
        </div>

        <Card
          title="Спрос"
          description="Факт за 60 дней и прогноз на 14"
          action={
            <Legend
              items={[
                { swatch: <BarSwatch color={C.fact} />, label: 'Факт' },
                { swatch: <BarSwatch color={C.excluded} />, label: 'Нет данных или дефицит' },
                { swatch: <LineSwatch color={C.brand} />, label: 'Прогноз' },
                { swatch: <AreaSwatch color={C.brandSoft} />, label: 'Разброс ±z·σ' },
              ]}
            />
          }
        >
          <DemandChart data={data} />
        </Card>

        <Card
          title="Остаток"
          description="Проекция на 14 дней"
          action={
            <Legend
              items={[
                { swatch: <LineSwatch color={C.brand} />, label: 'Остаток' },
                { swatch: <BarSwatch color={C.green} />, label: 'Поставка' },
                { swatch: <LineSwatch color={C.amber} dashed />, label: 'Страховой запас' },
              ]}
            />
          }
        >
          <StockChart data={data} />
        </Card>

        <Card
          flush
          title="Последние движения"
          action={
            <Link
              to={`/movements?item=${id}`}
              className="inline-flex items-center gap-1 text-[13px] font-medium text-brand-700 hover:text-brand-600"
            >
              Весь журнал
              <ArrowRight aria-hidden="true" className="size-3.5" strokeWidth={2} />
            </Link>
          }
        >
          {movements.data?.items.length ? (
            <ul className="divide-y divide-slate-100">
              {movements.data.items.map((m) => (
                <li key={m.id} className="flex items-center justify-between gap-4 px-5 py-3.5 sm:px-6">
                  <span className="min-w-0">
                    <span className="text-sm font-medium text-slate-900">{m.type_label}</span>
                    {m.reason_label && <span className="text-sm text-slate-500"> · {m.reason_label}</span>}
                    <span className="block text-[13px] text-slate-500">
                      {new Date(m.occurred_at).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' })}
                      {m.author_name ? ` · ${m.author_name}` : ''}
                    </span>
                  </span>
                  <SignedQty qty={m.qty} />
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-5 pb-6 text-sm text-slate-500 sm:px-6">Движений пока нет.</p>
          )}
        </Card>
      </div>

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

/** Одно ключевое число: подпись → значение → пояснение. */
function Metric({
  label,
  value,
  hint,
  muted = false,
  accent = false,
}: {
  label: string
  value: string
  hint?: string
  muted?: boolean
  accent?: boolean
}) {
  return (
    <div className="border-slate-100 p-5 sm:p-6 [&:nth-child(-n+2)]:border-b lg:[&:nth-child(-n+2)]:border-b-0 [&:nth-child(odd)]:border-r lg:[&:not(:last-child)]:border-r">
      <p className="text-[13px] text-slate-500">{label}</p>
      <p
        className={`mt-2 text-xl leading-tight font-semibold tracking-tight tabular-nums sm:text-2xl ${
          muted ? 'text-slate-400' : accent ? 'text-brand-700' : 'text-slate-900'
        }`}
      >
        {value}
      </p>
      {hint && <p className="mt-1 text-[13px] text-slate-500 tabular-nums">{hint}</p>}
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
      <p className="text-sm leading-relaxed text-slate-600">
        Данных пока мало — меньше недели, поэтому прогноз не строится. Статус считается по ручному
        минимальному остатку: <Num>{formatQtyWithUnit(data.item.manual_min_qty, unit)}</Num>. Сейчас на складе{' '}
        <Num>{formatQtyWithUnit(data.status.on_hand, unit)}</Num>.
      </p>
    )
  }

  const shortfall = Number(e.shortfall)

  return (
    <div className="space-y-3 text-sm leading-relaxed text-slate-600">
      <p>
        Сейчас <Num>{formatQtyWithUnit(e.on_hand, unit)}</Num>, в пути <Num>{formatQtyWithUnit(e.on_order, unit)}</Num>.
        {e.d2 && (
          <>
            {' '}
            До поставки по следующему заказу ({formatDay(e.d2, data.today)}) нужно{' '}
            <Num>{formatQtyWithUnit(e.need_until_d2, unit)}</Num> плюс страховой запас{' '}
            <Num>{formatQtyWithUnit(e.safety_stock, unit)}</Num>.
          </>
        )}
      </p>

      {shortfall > 0 ? (
        <p className="rounded-lg bg-brand-50 px-4 py-3 text-slate-700">
          Не хватает <Num>{formatQtyWithUnit(e.shortfall, unit)}</Num> — заказать{' '}
          <Num>
            {data.status.purchase_qty && data.item.purchase_unit
              ? `${formatQty(data.status.purchase_qty)} ${data.item.purchase_unit} (${formatQtyWithUnit(e.recommended_qty, unit)})`
              : formatQtyWithUnit(e.recommended_qty, unit)}
          </Num>
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

      <p className="border-t border-slate-100 pt-3 text-[13px] text-slate-500">
        Страховой запас = z · σ · √n, где z = {e.z.toFixed(2)} (уровень сервиса {data.item.service_level}%), σ ={' '}
        {e.sigma.toFixed(2)}, n = {e.days_to_d2} дн.
      </p>
    </div>
  )
}

function Num({ children }: { children: ReactNode }) {
  return <span className="font-semibold text-slate-900 tabular-nums">{children}</span>
}

function Accuracy({ accuracy }: { accuracy: Insights['accuracy'] }) {
  if (accuracy.model === 'M0') {
    return (
      <p className="text-sm leading-relaxed text-slate-600">
        Модель не построена: нужно не меньше 7 дней данных. Сейчас накоплено {accuracy.days_with_data}.
      </p>
    )
  }

  const bias = Math.round(accuracy.bias * 100)
  const pct = Math.round(accuracy.accuracy * 100)

  return (
    <div>
      <p className="text-4xl leading-none font-semibold tracking-tight text-slate-900 tabular-nums">{pct}%</p>
      <p className="mt-2 text-sm text-slate-600">
        за {accuracy.window_days} дней, модель {accuracy.model}
      </p>
      {bias !== 0 && (
        <p className="text-sm text-slate-600">
          {bias > 0 ? 'завышает' : 'занижает'} на {Math.abs(bias)}%
        </p>
      )}
      <p className="mt-4 border-t border-slate-100 pt-3 text-[13px] text-slate-500">
        Точность = 1 − WAPE. Данных в ряду: {accuracy.days_with_data} дней.
      </p>
    </div>
  )
}

/* ——— Графики ——— */

const axisProps = {
  tick: { fontSize: 12, fill: C.axis },
  axisLine: false,
  tickLine: false,
} as const

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
    <div className="h-60 w-full sm:h-64">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={points} margin={{ top: 8, right: 4, bottom: 0, left: -16 }} barCategoryGap={1}>
          <CartesianGrid vertical={false} stroke={C.grid} />
          <XAxis dataKey="day" tickFormatter={shortDay} minTickGap={32} {...axisProps} />
          <YAxis width={48} {...axisProps} />
          <Tooltip content={<ChartTooltip today={data.today} />} cursor={{ fill: C.grid }} />
          <Area dataKey="band" stroke="none" fill={C.brandSoft} fillOpacity={1} name="band" isAnimationActive={false} />
          <Bar dataKey="fact" fill={C.fact} radius={[2, 2, 0, 0]} name="fact" isAnimationActive={false} />
          <Bar dataKey="excluded" fill={C.excluded} radius={[2, 2, 0, 0]} name="excluded" isAnimationActive={false} />
          <Line
            dataKey="forecast"
            stroke={C.brand}
            strokeWidth={1.75}
            dot={false}
            name="forecast"
            isAnimationActive={false}
          />
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
    <div className="h-60 w-full sm:h-64">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={points} margin={{ top: 8, right: 4, bottom: 0, left: -16 }}>
          <CartesianGrid vertical={false} stroke={C.grid} />
          <XAxis dataKey="day" tickFormatter={shortDay} minTickGap={32} {...axisProps} />
          <YAxis width={48} {...axisProps} />
          <Tooltip content={<ChartTooltip today={data.today} />} cursor={{ stroke: C.zero }} />
          <ReferenceLine y={0} stroke={C.zero} />
          {safety > 0 && (
            <ReferenceLine
              y={safety}
              stroke={C.amber}
              strokeDasharray="4 4"
              strokeWidth={1.5}
              // Проекция часто уходит в минус, и ось без этого обрезала
              // бы линию запаса — самую полезную на графике.
              ifOverflow="extendDomain"
            />
          )}
          <Bar dataKey="incoming" fill={C.green} barSize={6} radius={[2, 2, 0, 0]} name="incoming" isAnimationActive={false} />
          <Line
            dataKey="stock"
            stroke={C.brand}
            strokeWidth={1.75}
            dot={false}
            activeDot={{ r: 3.5, strokeWidth: 0 }}
            name="stock"
            isAnimationActive={false}
          />
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

function ChartTooltip({ active, payload, label, today }: TooltipProps<number, string> & { today: string }) {
  if (!active || !payload?.length) return null
  const rows = payload.filter((p) => p.value !== null && p.value !== undefined)
  if (!rows.length) return null

  return (
    <div className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-[13px] shadow-pop">
      <p className="mb-1 font-medium text-slate-900">{formatDay(String(label), today)}</p>
      {rows.map((p) => (
        <p key={String(p.dataKey)} className="flex justify-between gap-6 text-slate-600">
          <span>{chartLabels[String(p.dataKey)] ?? p.dataKey}</span>
          <span className="font-medium text-slate-900 tabular-nums">{formatNumber(p.value)}</span>
        </p>
      ))}
    </div>
  )
}

function Legend({ items }: { items: { swatch: ReactNode; label: string }[] }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1.5 text-xs text-slate-500">
      {items.map((item) => (
        <li key={item.label} className="flex items-center gap-1.5">
          {item.swatch}
          {item.label}
        </li>
      ))}
    </ul>
  )
}

function BarSwatch({ color }: { color: string }) {
  return <span aria-hidden="true" className="inline-block size-2.5 rounded-[2px]" style={{ background: color }} />
}

function AreaSwatch({ color }: { color: string }) {
  return <span aria-hidden="true" className="inline-block h-2.5 w-3.5 rounded-[2px]" style={{ background: color }} />
}

function LineSwatch({ color, dashed = false }: { color: string; dashed?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className="inline-block w-3.5"
      style={{ borderTop: `2px ${dashed ? 'dashed' : 'solid'} ${color}` }}
    />
  )
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
