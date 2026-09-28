/**
 * Статусы позиции (§5.3).
 *
 * Цвет здесь — второй канал, а не единственный: подпись есть всегда.
 * Так статус читается и при дальтонизме, и на чёрно-белой распечатке.
 *
 * Палитра намеренно узкая: красный — критично, жёлтый — внимание,
 * зелёный — в порядке, серый — второстепенное. «Закончилось» и
 * «Критично» оба красные, но первое залито сплошным цветом: это самое
 * тяжёлое пятно на экране, и глаз находит его первым.
 */

export type ItemStatus =
  | 'out_of_stock'
  | 'critical'
  | 'order_today'
  | 'ok'
  | 'no_forecast'

export type StatusTone = 'red' | 'amber' | 'green' | 'grey'

export interface StatusPresentation {
  /** Короткая подпись для таблицы и фильтров. */
  label: string
  /** Фон и цвет текста бейджа. */
  badge: string
  /** Цвет точки рядом с подписью. */
  dot: string
  /** Тон для счётчиков и других мест, где нужен только оттенок. */
  tone: StatusTone
  /** Порядок сортировки: тревожные сверху (§6.1). */
  order: number
}

export const STATUS: Record<ItemStatus, StatusPresentation> = {
  out_of_stock: {
    label: 'Закончилось',
    badge: 'bg-red-600 text-white',
    dot: 'bg-white',
    tone: 'red',
    order: 0,
  },
  critical: {
    label: 'Критично',
    badge: 'bg-red-50 text-red-700 ring-1 ring-inset ring-red-100',
    dot: 'bg-red-500',
    tone: 'red',
    order: 1,
  },
  order_today: {
    label: 'Заказать сегодня',
    badge: 'bg-amber-50 text-amber-700 ring-1 ring-inset ring-amber-100',
    dot: 'bg-amber-500',
    tone: 'amber',
    order: 2,
  },
  no_forecast: {
    label: 'Нет прогноза',
    badge: 'bg-slate-100 text-slate-600',
    dot: 'bg-slate-400',
    tone: 'grey',
    order: 3,
  },
  ok: {
    label: 'Хватает',
    badge: 'bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-100',
    dot: 'bg-emerald-500',
    tone: 'green',
    order: 4,
  },
}

/**
 * Sortable — минимум, который нужен сортировке.
 *
 * Описан структурно, а не по конкретному типу ответа: одни и те же
 * правила применяются и к строкам дашборда, и к рекомендациям.
 */
export interface Sortable {
  name: string
  status: ItemStatus
  stockout_date?: string | null
}

/**
 * Сортировка таблицы по умолчанию: сначала красные, дальше по дате «хватит до»
 * (§6.1). Позиции без даты уходят в конец своей группы.
 */
export function sortItems<T extends Sortable>(items: readonly T[]): T[] {
  return [...items].sort((a, b) => {
    const byStatus = STATUS[a.status].order - STATUS[b.status].order
    if (byStatus !== 0) return byStatus

    if (a.stockout_date !== b.stockout_date) {
      if (!a.stockout_date) return 1
      if (!b.stockout_date) return -1
      return a.stockout_date < b.stockout_date ? -1 : 1
    }
    return a.name.localeCompare(b.name, 'ru')
  })
}
