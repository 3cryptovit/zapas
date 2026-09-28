import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { saveDemoLogin } from '@/lib/demoLogin'
import type { GuideState, StepID } from '@/lib/types'
import { GuidePanel } from './GuidePanel'

const order: StepID[] = ['suppliers', 'items', 'history', 'count', 'forecast', 'order', 'receive', 'time', 'notifications']

function guideWithDone(doneCount: number): GuideState {
  return {
    current: order[doneCount],
    steps: order.map((id, i) => ({
      id,
      done: i < doneCount,
      count: i < doneCount ? 4 : 0,
      fillable: i < 7,
      ready: i <= doneCount,
    })),
  }
}

function renderPanel(guide: GuideState) {
  const me = {
    user: { id: 'u', email: 'demo@sandbox.local', name: 'Владелец демо', role: 'owner', role_label: 'Владелец', telegram_linked: false },
    tenant: { id: 't-1', name: 'Кофейня', timezone: 'Europe/Moscow', is_sandbox: true, today: '2026-09-26', permissions: {} },
  }
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const body = url.endsWith('/me') ? me : url.endsWith('/sandbox/guide') ? guide : {}
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <GuidePanel />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('панель пошагового демо', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('показывает текущий шаг и выданный логин', async () => {
    saveDemoLogin('t-1', { email: 'demo-abcd2345@sandbox.local', password: 'secret12' })
    renderPanel(guideWithDone(1))

    expect(await screen.findByText('Шаг 2 из 9:')).toBeInTheDocument()
    expect(screen.getByText('demo-abcd2345@sandbox.local')).toBeInTheDocument()
    // Пароль скрыт, пока его не попросили.
    expect(screen.queryByText('secret12')).not.toBeInTheDocument()
  })

  it('действовать можно только в текущем шаге', async () => {
    saveDemoLogin('t-1', { email: 'demo-abcd2345@sandbox.local', password: 'p' })
    renderPanel(guideWithDone(1))

    expect(await screen.findByRole('button', { name: 'Добавить 38 позиций кофейни' })).toBeEnabled()
    // Пройденный и будущий шаги — строка без кнопок.
    expect(screen.queryByRole('button', { name: 'Добавить 4 поставщика' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Загрузить 90 дней истории' })).not.toBeInTheDocument()
    expect(screen.getByText('История расхода')).toBeInTheDocument()
  })

  it('в готовом демо без выданного логина панель свёрнута', async () => {
    renderPanel(guideWithDone(3))

    expect(await screen.findByRole('button', { name: 'Все шаги' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Загрузить 90 дней истории' })).not.toBeInTheDocument()
  })
})
