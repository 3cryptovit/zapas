import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Check, ChevronDown, ChevronRight, RotateCcw } from 'lucide-react'

import { ApiError } from '@/lib/api'
import { loadDemoLogin } from '@/lib/demoLogin'
import { useAdvanceSandbox, useFillStep, useGuide, useMe, useResetSandbox } from '@/lib/queries'
import { stepTexts } from '@/lib/guideSteps'
import type { GuideStep, StepID } from '@/lib/types'
import { Button } from './ui'

const PANEL_KEY = 'zapas:guide-open'

function readOpen(tenantId: string, guided: boolean): boolean {
  try {
    const raw = localStorage.getItem(`${PANEL_KEY}:${tenantId}`)
    if (raw !== null) return raw === '1'
  } catch {
    // Хранилище недоступно — решаем по режиму.
  }
  // Пошаговое демо открывается с панелью, готовое — без неё.
  return guided
}

function writeOpen(tenantId: string, open: boolean) {
  try {
    localStorage.setItem(`${PANEL_KEY}:${tenantId}`, open ? '1' : '0')
  } catch {
    // Не запомним — не страшно.
  }
}

/**
 * Пошаговое демо (§7): шаги внедрения по порядку, у каждого — ручной путь
 * и кнопка «заполнить тестовыми данными». Шаг засчитывается по данным,
 * поэтому сделанное руками отмечается так же, как шаблон.
 */
export function GuidePanel() {
  const { data: me } = useMe()
  const sandbox = Boolean(me?.tenant.is_sandbox)
  const { data: guide } = useGuide(sandbox)
  const fill = useFillStep()
  const advance = useAdvanceSandbox()
  const reset = useResetSandbox()

  const tenantId = me?.tenant.id ?? ''
  const login = tenantId ? loadDemoLogin(tenantId) : null
  const [open, setOpen] = useState<boolean | null>(null)
  const [message, setMessage] = useState('')
  const [showPassword, setShowPassword] = useState(false)

  if (!sandbox || !guide || !tenantId) return null

  const isOpen = open ?? readOpen(tenantId, login !== null)
  const toggle = () => {
    writeOpen(tenantId, !isOpen)
    setOpen(!isOpen)
  }

  const done = guide.steps.filter((s) => s.done).length
  const total = guide.steps.length
  const current = guide.steps.find((s) => s.id === guide.current)
  const currentIndex = current ? guide.steps.indexOf(current) + 1 : total

  const runFill = (step: StepID) => {
    setMessage('')
    fill.mutate(step, {
      onSuccess: (result) => {
        setMessage(
          result.filled > 0
            ? `${stepTexts[step].title}: добавлено ${result.filled}`
            : `${stepTexts[step].title}: уже заполнено, повтор ничего не дублирует`,
        )
      },
      onError: (error) => setMessage(error instanceof ApiError ? error.message : 'Не получилось'),
    })
  }

  return (
    <section
      className="mb-8 rounded-xl border border-slate-200 bg-white shadow-card"
      aria-label="Пошаговое демо"
    >
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3 px-5 py-4 sm:px-6">
        <div className="min-w-0 flex-1 basis-64">
          <p className="text-xs font-medium text-slate-500">Пошаговое демо</p>
          <p className="mt-1 text-[15px] leading-snug">
            {guide.current ? (
              <span className="text-slate-500">
                Шаг {currentIndex} из {total}: <strong className="font-semibold text-slate-900">{stepTexts[guide.current].title}</strong>
              </span>
            ) : (
              <strong className="font-semibold text-slate-900">Все шаги пройдены</strong>
            )}
          </p>
          <div
            className="mt-3 h-1 max-w-sm overflow-hidden rounded-full bg-slate-100"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={total}
            aria-valuenow={done}
            aria-label={`Пройдено ${done} из ${total}`}
          >
            <div
              className="h-full rounded-full bg-brand-600 transition-[width] duration-300"
              style={{ width: `${total ? (done / total) * 100 : 0}%` }}
            />
          </div>
        </div>

        {login && (
          <div className="text-[13px] text-slate-500">
            <p>Ваш вход</p>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2">
              <span className="font-mono text-slate-900">{login.email}</span>
              <button
                type="button"
                className="font-mono text-brand-700 underline decoration-brand-200 underline-offset-2 hover:decoration-brand-500"
                onClick={() => setShowPassword((v) => !v)}
              >
                {showPassword ? login.password : 'показать пароль'}
              </button>
            </p>
          </div>
        )}

        <div className="flex flex-wrap items-center gap-1">
          {/* Готовое демо не превратить в пошаговое повторным кликом на
              лендинге: сервер вернёт в то же живое демо. Отсюда — можно. */}
          {!login && (
            <Button
              variant="ghost"
              size="sm"
              icon={RotateCcw}
              onClick={() => reset.mutate('guided', { onSuccess: () => window.location.reload() })}
              loading={reset.isPending}
            >
              Пройти по шагам с нуля
            </Button>
          )}
          <Button variant="ghost" size="sm" onClick={toggle} aria-expanded={isOpen}>
            {isOpen ? 'Свернуть' : 'Все шаги'}
            <ChevronDown
              aria-hidden="true"
              className={`size-4 transition-transform duration-150 ${isOpen ? 'rotate-180' : ''}`}
              strokeWidth={1.75}
            />
          </Button>
        </div>
      </div>

      {isOpen && (
        <ol className="border-t border-slate-100 px-5 py-2 sm:px-6">
          {guide.steps.map((step, index) => (
            <StepRow
              key={step.id}
              step={step}
              index={index + 1}
              current={step.id === guide.current}
              busy={fill.isPending && fill.variables === step.id}
              anyBusy={fill.isPending || advance.isPending}
              onFill={() => runFill(step.id)}
              onAdvance={() => advance.mutate(7)}
              advancing={advance.isPending}
            />
          ))}
        </ol>
      )}

      {message && (
        <p className="border-t border-slate-100 px-5 py-3 text-[13px] text-slate-600 sm:px-6" role="status">
          {message}
        </p>
      )}
    </section>
  )
}

interface StepRowProps {
  step: GuideStep
  index: number
  current: boolean
  busy: boolean
  anyBusy: boolean
  advancing: boolean
  onFill: () => void
  onAdvance: () => void
}

function StepRow({ step, index, current, busy, anyBusy, advancing, onFill, onAdvance }: StepRowProps) {
  const text = stepTexts[step.id]

  const marker = (
    <span
      className={`flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold tabular-nums ${
        step.done
          ? 'bg-brand-600 text-white'
          : current
            ? 'bg-white text-brand-700 ring-2 ring-brand-600'
            : 'bg-white text-slate-400 ring-1 ring-slate-300'
      }`}
      aria-label={step.done ? 'пройден' : current ? 'текущий' : 'впереди'}
    >
      {step.done ? <Check aria-hidden="true" className="size-3.5" strokeWidth={2.5} /> : index}
    </span>
  )

  // Пройденный и будущий шаги — одна строка: на телефоне девять шагов с
  // кнопками занимали полтора экрана, а действовать можно только в текущем.
  if (!current) {
    return (
      <li className="flex items-center gap-3 py-2">
        {marker}
        <span className={`text-sm ${step.done ? 'text-slate-500' : 'text-slate-400'}`}>{text.title}</span>
      </li>
    )
  }

  return (
    <li className="my-2 grid gap-3 rounded-lg bg-slate-50 px-3 py-3 sm:grid-cols-[1.5rem_minmax(0,1fr)_auto] sm:items-start sm:gap-x-3">
      {marker}

      <div className="min-w-0">
        <p className="text-sm font-semibold text-slate-900">{text.title}</p>
        <p className="mt-1 max-w-2xl text-sm leading-relaxed text-slate-600">{text.why}</p>
      </div>

      <div className="flex flex-wrap items-center gap-2 sm:justify-end">
        {text.manual && (
          <Link
            to={text.manual.to}
            className="inline-flex min-h-9 items-center gap-1 px-2 text-[13px] font-medium text-slate-600 transition-colors hover:text-slate-900"
          >
            {text.manual.label}
            <ChevronRight aria-hidden="true" className="size-3.5" strokeWidth={2} />
          </Link>
        )}
        {step.id === 'time' && (
          <Button size="sm" onClick={onAdvance} loading={advancing} disabled={anyBusy}>
            +7 дней
          </Button>
        )}
        {step.fillable && text.fill && (
          <Button size="sm" onClick={onFill} loading={busy} disabled={anyBusy}>
            {text.fill}
          </Button>
        )}
      </div>
    </li>
  )
}
