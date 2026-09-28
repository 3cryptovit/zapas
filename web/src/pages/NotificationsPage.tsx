import { AlertTriangle, Bell, CheckCheck, Clock, FileText, PackageX, Timer, type LucideIcon } from 'lucide-react'

import { Alert, Button, Card, EmptyState, PageHeader, Skeleton } from '@/components/ui'
import { useMarkNotificationsRead, useMe, useNotifications } from '@/lib/queries'
import type { NotificationType } from '@/lib/types'

// Цвет — только у того, что требует действия. Сводка и напоминание
// об отсечке — будни, они нейтральные.
const kinds: Record<NotificationType, { icon: LucideIcon; tone: string }> = {
  critical: { icon: AlertTriangle, tone: 'bg-red-50 text-red-600' },
  order_late: { icon: Clock, tone: 'bg-amber-50 text-amber-700' },
  receipt_mismatch: { icon: PackageX, tone: 'bg-amber-50 text-amber-700' },
  cutoff_reminder: { icon: Timer, tone: 'bg-brand-50 text-brand-600' },
  daily_digest: { icon: FileText, tone: 'bg-slate-100 text-slate-500' },
}

/** Лента уведомлений (§6). */
export function NotificationsPage() {
  const { data, isLoading } = useNotifications()
  const { data: me } = useMe()
  const markRead = useMarkNotificationsRead()

  const unread = data?.unread ?? 0

  return (
    <div>
      <PageHeader
        title="Уведомления"
        description={unread > 0 ? `Непрочитанных: ${unread}` : 'Всё прочитано'}
        actions={
          unread > 0 && (
            <Button
              variant="secondary"
              icon={CheckCheck}
              onClick={() => markRead.mutate(undefined)}
              loading={markRead.isPending && markRead.variables === undefined}
            >
              Прочитать всё
            </Button>
          )
        }
      />

      {me?.tenant.is_sandbox && (
        <div className="mb-6">
          <Alert kind="info">
            В демо внешние каналы отключены: всё приходит сюда. Так выглядело бы сообщение в Telegram.
          </Alert>
        </div>
      )}

      {isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : !data?.items.length ? (
        <Card>
          <EmptyState
            icon={Bell}
            title="Уведомлений пока нет"
            hint="Здесь появятся сводка, срочные алерты и сообщения об опозданиях поставок."
          />
        </Card>
      ) : (
        <Card flush>
          <ul className="divide-y divide-slate-100">
            {data.items.map((n) => {
              const kind = kinds[n.type] ?? kinds.daily_digest
              const Icon = kind.icon
              return (
                <li key={n.id} className="flex gap-4 px-5 py-5 sm:px-6">
                  <span
                    aria-hidden="true"
                    className={`flex size-9 shrink-0 items-center justify-center rounded-full ${kind.tone} ${
                      n.read ? 'opacity-60' : ''
                    }`}
                  >
                    <Icon className="size-4" strokeWidth={1.75} />
                  </span>

                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
                      <div className="min-w-0">
                        <p className={`flex items-center gap-2 font-medium ${n.read ? 'text-slate-600' : 'text-slate-900'}`}>
                          {!n.read && (
                            <span aria-label="не прочитано" className="size-1.5 shrink-0 rounded-full bg-brand-600" />
                          )}
                          {n.payload.title}
                        </p>
                        <p className="mt-0.5 text-[13px] text-slate-500">
                          {n.type_label} ·{' '}
                          {new Date(n.created_at).toLocaleString('ru-RU', { dateStyle: 'medium', timeStyle: 'short' })}
                        </p>
                      </div>
                      {!n.read && (
                        <Button variant="ghost" size="sm" onClick={() => markRead.mutate([n.id])}>
                          Прочитано
                        </Button>
                      )}
                    </div>

                    {/* Текст приходит готовым — тем же, что уходит в Telegram. */}
                    <pre
                      className={`mt-3 font-sans text-sm leading-relaxed whitespace-pre-wrap ${
                        n.read ? 'text-slate-500' : 'text-slate-700'
                      }`}
                    >
                      {n.payload.body}
                    </pre>
                  </div>
                </li>
              )
            })}
          </ul>
        </Card>
      )}
    </div>
  )
}
