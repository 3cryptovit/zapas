import { afterEach, describe, expect, it, vi } from 'vitest'

import { newIdempotencyKey } from './api'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

describe('newIdempotencyKey', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('даёт UUID v4', () => {
    expect(newIdempotencyKey()).toMatch(UUID_V4)
  })

  it('работает без crypto.randomUUID — вне HTTPS и в старом Safari', () => {
    // Так браузер ведёт себя в незащищённом контексте: getRandomValues
    // есть, randomUUID нет. Раньше здесь падала запись заказа.
    vi.stubGlobal('crypto', { getRandomValues: crypto.getRandomValues.bind(crypto) })

    const a = newIdempotencyKey()
    const b = newIdempotencyKey()
    expect(a).toMatch(UUID_V4)
    expect(a).not.toBe(b)
  })
})
