import { STATUS, type ItemStatus } from '@/lib/status'

interface Props {
  status: ItemStatus
}

/**
 * Бейдж статуса: мягкая подложка, точка и подпись.
 *
 * Подпись несёт смысл, цвет помогает найти глазами. Точка спрятана от
 * скринридера — текст её уже дублирует.
 */
export function StatusBadge({ status }: Props) {
  const { label, badge, dot } = STATUS[status]

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium whitespace-nowrap ${badge}`}
      title={label}
    >
      <span aria-hidden="true" className={`size-1.5 shrink-0 rounded-full ${dot}`} />
      {label}
    </span>
  )
}
