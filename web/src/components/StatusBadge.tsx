import { STATUS, type ItemStatus } from '@/lib/status'

interface Props {
  status: ItemStatus
}

/**
 * Бейдж статуса.
 *
 * Цвета в интерфейсе нет, поэтому статус несут три признака:
 * квадрат-образец с заливкой, подпись прописными и насыщенность
 * шрифта. Образец стоит рядом с текстом, а не под ним: штриховка
 * под буквами читается плохо.
 */
export function StatusBadge({ status }: Props) {
  const { label, fill, weight } = STATUS[status]

  return (
    <span
      className={`inline-flex items-center gap-2 text-[0.6875rem] uppercase tracking-[0.14em] text-slate-900 ${weight}`}
      title={label}
    >
      <span
        aria-hidden="true"
        className={`inline-block size-3.5 shrink-0 border ${fill}`}
      />
      {label}
    </span>
  )
}
