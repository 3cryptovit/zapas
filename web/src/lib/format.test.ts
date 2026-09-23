import { describe, expect, it } from 'vitest'

import {
  formatCutoff,
  formatDay,
  formatPurchaseQty,
  formatQty,
  formatQtyWithUnit,
  plural,
  relativeDay,
} from './format'

describe('formatQty', () => {
  it('показывает дробную часть только когда она есть', () => {
    expect(formatQty('24.000')).toBe('24')
    expect(formatQty('1.500')).toBe('1,5')
    expect(formatQty('0.001')).toBe('0,001')
  })

  it('разделяет разряды по-русски', () => {
    // Неразрывный пробел — так Intl форматирует ru-RU.
    expect(formatQty('1500')).toBe('1 500')
  })

  it('не падает на мусоре', () => {
    expect(formatQty('не число')).toBe('—')
  })
})

describe('formatQtyWithUnit', () => {
  it('добавляет единицу', () => {
    expect(formatQtyWithUnit('24.000', 'l')).toBe('24 л')
    expect(formatQtyWithUnit('1.500', 'kg')).toBe('1,5 кг')
    expect(formatQtyWithUnit('12', 'pcs')).toBe('12 шт')
  })
})

describe('formatPurchaseQty', () => {
  it('показывает единицу закупки и базовую: «2 кор. (24 л)»', () => {
    expect(formatPurchaseQty('24.000', 'l', 'кор.', '12')).toBe('2 кор. (24 л)')
  })

  it('без коэффициента показывает только базовую единицу', () => {
    expect(formatPurchaseQty('24.000', 'l', '', '0')).toBe('24 л')
  })
})

describe('formatDay', () => {
  const today = '2026-09-22'

  it('даёт точную и относительную дату сразу', () => {
    expect(formatDay('2026-09-24', today)).toBe('чт, 24 сен (послезавтра)')
    expect(formatDay('2026-09-22', today)).toBe('вт, 22 сен (сегодня)')
    expect(formatDay('2026-09-23', today)).toBe('ср, 23 сен (завтра)')
  })

  it('для далёкой даты обходится без относительной части', () => {
    expect(formatDay('2026-12-31', today)).toBe('чт, 31 дек')
  })

  it('не сдвигает дату из-за часового пояса браузера', () => {
    // Дни приходят как YYYY-MM-DD в поясе тенанта, а не как момент времени.
    expect(formatDay('2026-01-01', '2026-01-01')).toBe('чт, 1 янв (сегодня)')
  })
})

describe('relativeDay', () => {
  it('склоняет дни', () => {
    expect(relativeDay(3)).toBe('через 3 дня')
    expect(relativeDay(5)).toBe('через 5 дней')
    expect(relativeDay(-1)).toBe('вчера')
    expect(relativeDay(-3)).toBe('3 дня назад')
    expect(relativeDay(30)).toBe('')
  })
})

describe('plural', () => {
  it.each([
    [1, '1 день'],
    [2, '2 дня'],
    [5, '5 дней'],
    [11, '11 дней'],
    [21, '21 день'],
    [22, '22 дня'],
    [25, '25 дней'],
    [111, '111 дней'],
  ])('%i → %s', (n, want) => {
    expect(plural(n, 'день', 'дня', 'дней')).toBe(want)
  })
})

describe('formatCutoff', () => {
  it('показывает время отсечки в поясе тенанта', () => {
    expect(formatCutoff('2026-09-24T16:00:00+03:00', 'Europe/Moscow')).toBe('16:00')
  })
})
