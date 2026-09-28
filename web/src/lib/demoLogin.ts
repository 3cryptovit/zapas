/**
 * Логин пошагового демо. Сервер отдаёт пароль один раз — при создании, —
 * а хранит только хеш. Поэтому его держит браузер посетителя, рядом с id
 * демо: чужой логин в чужом демо не показывается.
 */

import type { DemoLogin } from './types'

const KEY = 'zapas:demo-login'

interface Stored extends DemoLogin {
  tenant_id: string
}

export function saveDemoLogin(tenantId: string, login: DemoLogin): void {
  try {
    localStorage.setItem(KEY, JSON.stringify({ tenant_id: tenantId, ...login }))
  } catch {
    // Приватное окно или запрещённое хранилище: логин просто не покажем.
  }
}

export function loadDemoLogin(tenantId: string): DemoLogin | null {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return null
    const stored = JSON.parse(raw) as Partial<Stored>
    if (stored.tenant_id !== tenantId || !stored.email || !stored.password) return null
    return { email: stored.email, password: stored.password }
  } catch {
    return null
  }
}
