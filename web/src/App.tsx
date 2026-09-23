import { Suspense, lazy } from 'react'
import { Navigate, Outlet, Route, Routes } from 'react-router-dom'

import { Layout } from '@/components/Layout'
import { Skeleton } from '@/components/ui'
import { useMe } from '@/lib/queries'
import { CountPage, CountsPage } from '@/pages/CountsPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { LoginPage } from '@/pages/LoginPage'
import { MovementsPage } from '@/pages/MovementsPage'
import { NotificationsPage } from '@/pages/NotificationsPage'
import { OrderPage, OrdersPage } from '@/pages/OrdersPage'
import { SettingsPage } from '@/pages/SettingsPage'

// Карточка позиции тянет за собой графики (Recharts) — половину бандла.
// Дашборд должен открываться быстро, поэтому грузим её отдельно.
const ItemPage = lazy(() =>
  import('@/pages/ItemPage').then((m) => ({ default: m.ItemPage })),
)

/** Кабинет (§6). Без действующей сессии всё ведёт на вход. */
export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />

      <Route element={<RequireAuth />}>
        <Route element={<Layout />}>
          <Route index element={<DashboardPage />} />
          <Route
            path="items/:id"
            element={
              <Suspense fallback={<Skeleton className="h-96 w-full" />}>
                <ItemPage />
              </Suspense>
            }
          />
          <Route path="movements" element={<MovementsPage />} />
          <Route path="counts" element={<CountsPage />} />
          <Route path="counts/:id" element={<CountPage />} />
          <Route path="orders" element={<OrdersPage />} />
          <Route path="orders/:id" element={<OrderPage />} />
          <Route path="notifications" element={<NotificationsPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}

/**
 * RequireAuth пускает дальше только с действующей сессией.
 *
 * Сессия проверяется запросом GET /me: cookie httpOnly, и прочитать её
 * из JavaScript нельзя (ADR-007).
 */
function RequireAuth() {
  const { data, isLoading, isError } = useMe()

  if (isLoading) {
    return (
      <div className="mx-auto max-w-6xl space-y-4 px-4 py-8">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }
  if (isError || !data) {
    return <Navigate to="/login" replace />
  }
  return <Outlet />
}

function NotFound() {
  return (
    <div className="py-10 text-center">
      <h1 className="text-2xl font-semibold text-slate-900">Страница не найдена</h1>
      <p className="mt-2 text-slate-500">Проверьте адрес или вернитесь на дашборд.</p>
    </div>
  )
}
