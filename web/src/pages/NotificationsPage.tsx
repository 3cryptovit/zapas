import { Alert, Button, Card, EmptyState, Skeleton } from '@/components/ui'
import { useMarkNotificationsRead, useMe, useNotifications } from '@/lib/queries'
import type { NotificationType } from '@/lib/types'

// Срочность несёт вес левой линейки: цвета в интерфейсе нет, а
// одинаковые серые плашки различались бы только текстом.
const tone: Record<NotificationType, string> = {
  critical: 'border-l-4 border-slate-900 bg-slate-100 ring-slate-300',
  order_late: 'border-l-4 border-slate-500 bg-slate-50 ring-slate-200',
  cutoff_reminder: 'border-l-2 border-slate-500 bg-slate-50 ring-slate-200',
  receipt_mismatch: 'border-l-2 border-slate-400 bg-white ring-slate-200',
  daily_digest: 'border-l border-slate-300 bg-white ring-slate-200',
}

/** Лента уведомлений (§6). */
export function NotificationsPage() {
  const { data, isLoading } = useNotifications()
  const { data: me } = useMe()
  const markRead = useMarkNotificationsRead()

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold text-slate-900">
          Уведомления
          {data && data.unread > 0 && (
            <span className="ml-2 rounded-full bg-red-100 px-2.5 py-1 text-sm font-medium text-red-800">
              {data.unread}
            </span>
          )}
        </h1>
        {data && data.unread > 0 && (
          <Button
            variant="secondary"
            onClick={() => markRead.mutate(undefined)}
            loading={markRead.isPending}
          >
            Прочитать всё
          </Button>
        )}
      </div>

      {me?.tenant.is_sandbox && (
        <Alert kind="info">
          В демо внешние каналы отключены: всё приходит сюда. Так выглядело бы
          сообщение в Telegram.
        </Alert>
      )}

      {isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : !data?.items.length ? (
        <EmptyState
          title="Уведомлений пока нет"
          hint="Здесь появятся сводка, срочные алерты и сообщения об опозданиях поставок."
        />
      ) : (
        <ul className="space-y-3">
          {data.items.map((n) => (
            <li key={n.id}>
              <Card
                className={`ring-1 ring-inset ${tone[n.type] ?? tone.daily_digest} ${
                  n.read ? 'opacity-70' : ''
                }`}
              >
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <p className="font-medium text-slate-900">{n.payload.title}</p>
                    <p className="text-xs text-slate-500">
                      {n.type_label} · {new Date(n.created_at).toLocaleString('ru-RU')}
                    </p>
                  </div>
                  {!n.read && (
                    <Button
                      variant="ghost"
                      className="min-h-9 px-2 text-xs"
                      onClick={() => markRead.mutate([n.id])}
                    >
                      Прочитано
                    </Button>
                  )}
                </div>

                {/* Текст приходит готовым — тем же, что уходит в Telegram. */}
                <pre className="mt-2 whitespace-pre-wrap font-sans text-sm text-slate-800">
                  {n.payload.body}
                </pre>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
