import { describe, expect, it } from 'vitest'

import { STATUS, sortItems, type Sortable } from './status'

describe('STATUS', () => {
  it('каждый статус закодирован тремя признаками, не цветом', () => {
    for (const [code, presentation] of Object.entries(STATUS)) {
      expect(presentation.label, code).toBeTruthy()
      expect(presentation.fill, code).toBeTruthy()
      expect(presentation.weight, code).toBeTruthy()
    }
  })

  it('заливки у всех статусов разные — иначе они сольются', () => {
    const fills = Object.values(STATUS).map((s) => s.fill)
    expect(new Set(fills).size).toBe(fills.length)
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
