import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { keys, usePostCount, useSendOrder } from './queries'

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  return { client, wrapper }
}

function stubFetch(body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })),
  )
}

describe('после изменения карточка перечитывается', () => {
  afterEach(() => vi.unstubAllGlobals())

  // Раньше обновлялся только список ['orders', …], а карточка ['order', id]
  // оставалась черновиком: кнопка «Отправить поставщику» не исчезала, хотя
  // сервер уже ответил «отправлен».
  it('отправка заказа помечает карточку заказа устаревшей', async () => {
    const { client, wrapper } = setup()
    client.setQueryData(keys.order('o-1'), { id: 'o-1', status: 'draft' })
    stubFetch({ id: 'o-1', status: 'sent' })

    const { result } = renderHook(() => useSendOrder(), { wrapper })
    await act(() => result.current.mutateAsync('o-1'))

    await waitFor(() => expect(client.getQueryState(keys.order('o-1'))?.isInvalidated).toBe(true))
  })

  it('проведение пересчёта помечает карточку пересчёта устаревшей', async () => {
    const { client, wrapper } = setup()
    client.setQueryData(keys.count('c-1'), { id: 'c-1', status: 'draft' })
    stubFetch({ id: 'c-1', status: 'posted' })

    const { result } = renderHook(() => usePostCount(), { wrapper })
    await act(() => result.current.mutateAsync({ countId: 'c-1' }))

    await waitFor(() => expect(client.getQueryState(keys.count('c-1'))?.isInvalidated).toBe(true))
  })
})
