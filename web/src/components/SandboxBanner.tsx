import { useState } from 'react'

import { formatDay } from '@/lib/format'
import { useAdvanceSandbox, useMe, useResetSandbox, useSetAutopilot } from '@/lib/queries'
import { Button } from './ui'

/**
 * Плашка демо (§7.4): виртуальная дата, промотка времени, автопилот
 * и сброс. Держится сверху, чтобы посетитель не забыл, что смотрит демо.
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
    <div className="bg-slate-900 text-white">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2 text-sm">
        <span className="font-medium">Это демо.</span>
        {hoursLeft !== null && (
          <span className="text-slate-300">Данные удалятся через {hoursLeft} ч</span>
        )}

        <span className="text-slate-300">
          Сегодня в демо: <strong className="text-white">{formatDay(me.tenant.today, me.tenant.today)}</strong>
        </span>

        <div className="ml-auto flex flex-wrap items-center gap-2">
          <label className="flex cursor-pointer items-center gap-2 text-slate-300">
            <input
              type="checkbox"
              className="h-4 w-4 rounded"
              onChange={(e) => autopilot.mutate(e.target.checked)}
              disabled={autopilot.isPending}
            />
            Автопилот заказов
          </label>

          <Button
            variant="secondary"
            className="min-h-9 px-3 text-xs"
            onClick={() => run(1)}
            loading={advance.isPending}
          >
            +1 день
          </Button>
          <Button
            variant="secondary"
            className="min-h-9 px-3 text-xs"
            onClick={() => run(7)}
            loading={advance.isPending}
          >
            +7 дней
          </Button>
          <Button
            variant="ghost"
            className="min-h-9 px-3 text-xs text-slate-300 hover:bg-slate-800"
            onClick={() => {
              reset.mutate(undefined, { onSuccess: () => window.location.reload() })
            }}
            loading={reset.isPending}
          >
            Сбросить демо
          </Button>
        </div>

        {lastResult && (
          <p className="w-full text-xs text-slate-400">{lastResult}</p>
        )}
      </div>
    </div>
  )
}
