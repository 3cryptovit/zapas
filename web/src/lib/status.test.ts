import { describe, expect, it } from 'vitest'

import { STATUS, sortItems, type Sortable } from './status'

describe('STATUS', () => {
  it('у каждого статуса есть подпись: цвет — второй канал, не единственный', () => {
    for (const [code, presentation] of Object.entries(STATUS)) {
      expect(presentation.label, code).toBeTruthy()
      expect(presentation.badge, code).toBeTruthy()
      expect(presentation.dot, code).toBeTruthy()
    }
  })

  it('бейджи у всех статусов разные — иначе они сольются', () => {
    const badges = Object.values(STATUS).map((s) => s.badge)
    expect(new Set(badges).size).toBe(badges.length)
  })

  it('тревожные статусы красные, спокойный — зелёный', () => {
    expect(STATUS.out_of_stock.tone).toBe('red')
    expect(STATUS.critical.tone).toBe('red')
    expect(STATUS.order_today.tone).toBe('amber')
    expect(STATUS.ok.tone).toBe('green')
    expect(STATUS.no_forecast.tone).toBe('grey')
  })
})

describe('sortItems', () => {
  const item = (
    name: string,
    status: Sortable['status'],
    stockout_date: string | null = null,
  ): Sortable => ({ name, status, stockout_date })

  it('поднимает красные наверх, дальше сортирует по дате «хватит до»', () => {
    const got = sortItems([
      item('Сахар', 'ok', '2026-10-01'),
      item('Молоко', 'order_today', '2026-09-26'),
      item('Стаканы', 'critical', '2026-09-24'),
      item('Сливки', 'out_of_stock'),
      item('Сиропы', 'order_today', '2026-09-25'),
    ])

    expect(got.map((i) => i.name)).toEqual([
      'Сливки',  // закончилось
      'Стаканы', // критично
      'Сиропы',  // заказать сегодня, раньше кончится
      'Молоко',  // заказать сегодня
      'Сахар',   // хватает
    ])
  })

  it('позиции без даты уходят в конец своей группы', () => {
    const got = sortItems([
      item('Без даты', 'ok', null),
      item('С датой', 'ok', '2026-10-01'),
    ])
    expect(got.map((i) => i.name)).toEqual(['С датой', 'Без даты'])
  })

  it('не меняет исходный массив', () => {
    const input = [item('Б', 'ok'), item('А', 'critical')]
    sortItems(input)
    expect(input[0]!.name).toBe('Б')
  })
})
