import { useState } from 'react'
import { RotateCcw } from 'lucide-react'

import { loadDemoLogin } from '@/lib/demoLogin'
import { formatDate } from '@/lib/format'
import { useAdvanceSandbox, useMe, useResetSandbox, useSetAutopilot } from '@/lib/queries'
import { Button } from './ui'

/**
 * Полоса демо (§7.4): виртуальная дата, промотка времени, автопилот
 * и сброс. Держится над содержимым, чтобы посетитель не забыл, что
 * смотрит демо, но не спорит с ним за внимание.
 */
export function SandboxBanner() {
  const { data: me } = useMe()
  const advance = useAdvanceSandbox()
  const autopilot = useSetAutopilot()
  const reset = useResetSandbox()
  const [lastResult, setLastResult] = useState<string>('')

  if (!me?.tenant.is_sandbox) return null

  const hoursLeft = me.tenant.expires_at
    ? Math.max(0, Math.round((new Date(me.tenant.expires_at).getTime() - Date.now()) / 3_600_000))
    : null

  // Пока запрос идёт, показываем то, что попросили, а не старое значение.
  const autopilotOn = autopilot.isPending ? Boolean(autopilot.variables) : me.tenant.autopilot

  const run = (days: 1 | 7) => {
    setLastResult('')
    advance.mutate(days, {
      onSuccess: (result) => {
        const parts = [`+${result.days} дн.`]
        if (result.received) parts.push(`принято поставок: ${result.received}`)
        if (result.ordered) parts.push(`заказов автопилотом: ${result.ordered}`)
        if (result.notifications) parts.push(`уведомлений: ${result.notifications}`)
        setLastResult(parts.join(' · '))
      },
      onError: (error) => setLastResult(error instanceof Error ? error.message : 'Ошибка'),
    })
  }

  return (
    <div className="border-b border-slate-200 bg-white">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-6 gap-y-3 px-4 py-3 sm:px-6 lg:px-10">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          <span className="rounded-md bg-brand-50 px-2 py-0.5 text-xs font-medium text-brand-700">
            Демо
          </span>
          <span className="text-slate-500">
            Сегодня в демо{' '}
            <span className="font-medium text-slate-900">{formatDate(me.tenant.today)}</span>
          </span>
          {hoursLeft !== null && (
            <span className="hidden text-slate-400 sm:inline">удалится через {hoursLeft} ч</span>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2 sm:ml-auto">
          <Switch
            label="Автопилот"
            longLabel="Автопилот заказов"
            checked={autopilotOn}
            disabled={autopilot.isPending}
            onChange={(on) => autopilot.mutate(on)}
          />

          <span aria-hidden="true" className="mx-1 hidden h-5 w-px bg-slate-200 sm:block" />

          <Button variant="secondary" size="sm" onClick={() => run(1)} loading={advance.isPending && advance.variables === 1} disabled={advance.isPending}>
            +1 день
          </Button>
          <Button variant="secondary" size="sm" onClick={() => run(7)} loading={advance.isPending && advance.variables === 7} disabled={advance.isPending}>
            +7 дней
          </Button>
          <Button
            variant="ghost"
            size="sm"
            icon={RotateCcw}
            aria-label="Сбросить демо"
            onClick={() => {
              // Пошаговое демо сбрасывается в пошаговое: у него есть выданный логин.
              const mode = loadDemoLogin(me.tenant.id) ? 'guided' : 'ready'
              reset.mutate(mode, { onSuccess: () => window.location.reload() })
            }}
            loading={reset.isPending}
          >
            <span className="hidden sm:inline">Сбросить</span>
          </Button>
        </div>

        {lastResult && (
          <p className="w-full text-[13px] text-slate-500" role="status">
            {lastResult}
          </p>
        )}
      </div>
    </div>
  )
}

interface SwitchProps {
  label: string
  /** Подпись для широкого экрана; на телефоне — короткая. */
  longLabel?: string
  checked: boolean
  disabled?: boolean
  onChange: (checked: boolean) => void
}

/** Переключатель: состояние видно по положению, а не только по цвету. */
function Switch({ label, longLabel, checked, disabled, onChange }: SwitchProps) {
  return (
    <label className="flex min-h-9 cursor-pointer items-center gap-2.5 text-[13px] font-medium text-slate-700 select-none">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors duration-150 disabled:opacity-60 ${
          checked ? 'bg-brand-600' : 'bg-slate-300'
        }`}
      >
        <span
          aria-hidden="true"
          className={`inline-block size-4 rounded-full bg-white shadow-card transition-transform duration-150 ${
            checked ? 'translate-x-[18px]' : 'translate-x-0.5'
          }`}
        />
      </button>
      {longLabel ? (
        <>
          <span className="sm:hidden">{label}</span>
          <span className="hidden sm:inline">{longLabel}</span>
        </>
      ) : (
        label
      )}
    </label>
  )
}
