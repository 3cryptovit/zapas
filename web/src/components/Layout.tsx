import { NavLink, Outlet, useNavigate } from 'react-router-dom'

import { useLogout, useMe, useNotifications } from '@/lib/queries'
import { SandboxBanner } from './SandboxBanner'

/** Пункты навигации. Заказы и настройки доступны только владельцу (§6). */
const nav = [
  { to: '/', label: 'Дашборд', end: true, ownerOnly: false },
  { to: '/movements', label: 'Движения', end: false, ownerOnly: false },
  { to: '/counts', label: 'Инвентаризация', end: false, ownerOnly: false },
  { to: '/orders', label: 'Заказы', end: false, ownerOnly: false },
  { to: '/notifications', label: 'Уведомления', end: false, ownerOnly: false },
  { to: '/settings', label: 'Настройки', end: false, ownerOnly: true },
]

export function Layout() {
  const { data: me } = useMe()
  const { data: feed } = useNotifications()
  const logout = useLogout()
  const navigate = useNavigate()

  const isOwner = me?.user.role === 'owner'

  return (
    <div className="min-h-screen bg-paper">
      {me?.tenant.is_sandbox && <SandboxBanner />}

      <header className="border-b border-slate-300 bg-white">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-5 gap-y-2 px-4 py-4">
          <span className="display text-xl text-slate-900">Zapas</span>
          <span className="eyebrow">{me?.tenant.name}</span>

          <div className="ml-auto flex items-center gap-4">
            <span className="hidden text-xs text-slate-500 sm:inline">
              {me?.user.name || me?.user.email} · {me?.user.role_label}
            </span>
            <button
              type="button"
              onClick={() => {
                logout.mutate(undefined, { onSuccess: () => navigate('/login') })
              }}
              className="eyebrow min-h-11 px-2 hover:text-slate-900"
            >
              Выйти
            </button>
          </div>
        </div>

        <nav className="mx-auto max-w-6xl overflow-x-auto px-4">
          <ul className="flex gap-1 whitespace-nowrap">
            {nav
              .filter((item) => !item.ownerOnly || isOwner)
              .map((item) => (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    end={item.end}
                    className={({ isActive }) =>
                      `inline-flex min-h-11 items-center gap-2 border-b-2 px-3 text-[0.6875rem] font-semibold uppercase tracking-[0.14em] transition ${
                        isActive
                          ? 'border-slate-900 text-slate-900'
                          : 'border-transparent text-slate-500 hover:text-slate-900'
                      }`
                    }
                  >
                    {item.label}
                    {item.to === '/notifications' && feed && feed.unread > 0 && (
                      <span className="bg-slate-900 px-1.5 py-0.5 text-[0.625rem] font-bold text-white">
                        {feed.unread}
                      </span>
                    )}
                  </NavLink>
                </li>
              ))}
          </ul>
        </nav>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  )
}
