/**
 * Статусы позиции (§5.3).
 *
 * Интерфейс монохромный, поэтому цвета как канала нет вовсе. Статус
 * несут три независимых признака: плотность заливки, подпись и
 * насыщенность шрифта. Побочная польза — такое кодирование переживает
 * дальтонизм и чёрно-белую печать, чего §6.3 и требовал.
 */

export type ItemStatus =
  | 'out_of_stock'
  | 'critical'
  | 'order_today'
  | 'ok'
  | 'no_forecast'

export interface StatusPresentation {
  /** Короткая подпись для таблицы и фильтров. */
  label: string
  /**
   * Заливка квадрата-образца. Плотность штриховки заменяет цвет:
   * от сплошной чёрной у «закончилось» до тонкого контура у «хватает».
   */
  fill: string
  /** Насыщенность подписи — третий признак. */
  weight: string
  /** Порядок сортировки: тревожные сверху (§6.1). */
  order: number
}

export const STATUS: Record<ItemStatus, StatusPresentation> = {
  // Сплошная чёрная плашка: самое тяжёлое пятно на экране.
  out_of_stock: {
    label: 'Закончилось',
    fill: 'fill-solid border-slate-900',
    weight: 'font-bold',
    order: 0,
  },
  // Плотная штриховка: тёмное, но уже различимое от сплошного.
  critical: {
    label: 'Критично',
    fill: 'fill-dense border-slate-900',
    weight: 'font-bold',
    order: 1,
  },
  // Редкая штриховка: заметно, но не кричит.
  order_today: {
    label: 'Заказать сегодня',
    fill: 'fill-sparse border-slate-700',
    weight: 'font-semibold',
    order: 2,
  },
  // Пунктир: система честно говорит, что данных мало.
  no_forecast: {
    label: 'Нет прогноза',
    fill: 'border-slate-400 border-dashed',
    weight: 'font-normal',
    order: 3,
  },
  // Тонкий контур: спокойное уходит на задний план.
  ok: {
    label: 'Хватает',
    fill: 'border-slate-300',
    weight: 'font-normal',
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
