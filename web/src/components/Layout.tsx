import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import {
  ArrowLeftRight,
  Bell,
  ClipboardCheck,
  LayoutDashboard,
  LogOut,
  Menu,
  Settings,
  Truck,
  X,
  type LucideIcon,
} from 'lucide-react'

import { useLogout, useMe, useNotifications } from '@/lib/queries'
import { GuidePanel } from './GuidePanel'
import { SandboxBanner } from './SandboxBanner'

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  end: boolean
  ownerOnly: boolean
}

/** Пункты навигации. Настройки доступны только владельцу (§6). */
const nav: NavItem[] = [
  { to: '/', label: 'Дашборд', icon: LayoutDashboard, end: true, ownerOnly: false },
  { to: '/movements', label: 'Движения', icon: ArrowLeftRight, end: false, ownerOnly: false },
  { to: '/counts', label: 'Инвентаризация', icon: ClipboardCheck, end: false, ownerOnly: false },
  { to: '/orders', label: 'Заказы', icon: Truck, end: false, ownerOnly: false },
  { to: '/notifications', label: 'Уведомления', icon: Bell, end: false, ownerOnly: false },
  { to: '/settings', label: 'Настройки', icon: Settings, end: false, ownerOnly: true },
]

export function Layout() {
  const { data: me } = useMe()
  const [menuOpen, setMenuOpen] = useState(false)
  const location = useLocation()

  // Переход по ссылке закрывает меню на телефоне.
  useEffect(() => setMenuOpen(false), [location.pathname])

  useEffect(() => {
    if (!menuOpen) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setMenuOpen(false)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [menuOpen])

  const sandbox = Boolean(me?.tenant.is_sandbox)

  return (
    <div className="min-h-screen bg-slate-50 lg:pl-60">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-60 border-r border-slate-200 bg-white lg:block">
        <Sidebar />
      </aside>

      {/* Телефон и планшет: шапка с кнопкой меню. */}
      <div className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b border-slate-200 bg-white px-2 lg:hidden">
        <button
          type="button"
          onClick={() => setMenuOpen(true)}
          aria-label="Открыть меню"
          aria-expanded={menuOpen}
          className="flex size-11 items-center justify-center rounded-lg text-slate-600 hover:bg-slate-100"
        >
          <Menu aria-hidden="true" className="size-5" strokeWidth={1.75} />
        </button>
        <Logo />
      </div>

      {menuOpen && (
        <div className="fixed inset-0 z-40 lg:hidden" role="dialog" aria-modal="true" aria-label="Меню">
          <div className="absolute inset-0 bg-slate-900/30" onClick={() => setMenuOpen(false)} />
          <aside className="animate-rise absolute inset-y-0 left-0 w-72 max-w-[85vw] bg-white shadow-pop">
            <button
              type="button"
              onClick={() => setMenuOpen(false)}
              aria-label="Закрыть меню"
              className="absolute top-3 right-3 flex size-9 items-center justify-center rounded-lg text-slate-400 hover:bg-slate-100 hover:text-slate-700"
            >
              <X aria-hidden="true" className="size-4" strokeWidth={2} />
            </button>
            <Sidebar />
          </aside>
        </div>
      )}

      {sandbox && <SandboxBanner />}

      <main className="mx-auto max-w-6xl px-4 py-6 sm:px-6 sm:py-8 lg:px-10 lg:py-10">
        {sandbox && <GuidePanel />}
        <div className="animate-rise" key={location.pathname}>
          <Outlet />
        </div>
      </main>
    </div>
  )
}

function Logo() {
  return (
    <span className="flex items-center gap-2">
      <span
        aria-hidden="true"
        className="flex size-6 items-center justify-center rounded-md bg-brand-600 text-[13px] font-semibold text-white"
      >
        Z
      </span>
      <span className="text-[15px] font-semibold tracking-tight text-slate-900">Zapas</span>
    </span>
  )
}

function Sidebar() {
  const { data: me } = useMe()
  const { data: feed } = useNotifications()
  const logout = useLogout()
  const navigate = useNavigate()

  const isOwner = me?.user.role === 'owner'
  const name = me?.user.name || me?.user.email || ''

  return (
    <div className="flex h-full flex-col">
      <div className="px-5 pt-5 pb-6">
        <Logo />
        {me && (
          <p className="mt-3 truncate text-[13px] text-slate-500">{me.tenant.name}</p>
        )}
      </div>

      <nav aria-label="Разделы" className="flex-1 px-3">
        <ul className="space-y-0.5">
          {nav
            .filter((item) => !item.ownerOnly || isOwner)
            .map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.end}
                  className={({ isActive }) =>
                    `group flex min-h-10 items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors ${
                      isActive
                        ? 'bg-brand-50 text-brand-700'
                        : 'text-slate-600 hover:bg-slate-50 hover:text-slate-900'
                    }`
                  }
                >
                  {({ isActive }) => (
                    <>
                      <item.icon
                        aria-hidden="true"
                        className={`size-[18px] shrink-0 ${
                          isActive ? 'text-brand-600' : 'text-slate-400 group-hover:text-slate-600'
                        }`}
                        strokeWidth={1.75}
                      />
                      <span className="flex-1">{item.label}</span>
                      {item.to === '/notifications' && feed && feed.unread > 0 && (
                        <span className="min-w-5 rounded-full bg-brand-600 px-1.5 text-center text-[11px] leading-5 font-semibold text-white tabular-nums">
                          {feed.unread}
                        </span>
                      )}
                    </>
                  )}
                </NavLink>
              </li>
            ))}
        </ul>
      </nav>

      <div className="border-t border-slate-100 p-3">
        <div className="flex items-center gap-3 rounded-lg px-2 py-2">
          <span
            aria-hidden="true"
            className="flex size-8 shrink-0 items-center justify-center rounded-full bg-slate-100 text-[13px] font-semibold text-slate-600"
          >
            {initials(name)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[13px] font-medium text-slate-900">{name}</span>
            <span className="block truncate text-xs text-slate-500">{me?.user.role_label}</span>
          </span>
          <button
            type="button"
            onClick={() => logout.mutate(undefined, { onSuccess: () => navigate('/login') })}
            aria-label="Выйти"
            title="Выйти"
            className="flex size-8 shrink-0 items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700"
          >
            <LogOut aria-hidden="true" className="size-4" strokeWidth={1.75} />
          </button>
        </div>
      </div>
    </div>
  )
}

/** Инициалы для аватара: «Иван Петров» → «ИП», почта → первая буква. */
function initials(name: string): string {
  const parts = name.replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean)
  const letters = parts.slice(0, 2).map((p) => p[0]!.toUpperCase())
  return letters.join('') || '·'
}
