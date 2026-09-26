/**
 * Пути приложения. Кабинет живёт в <префикс>/app/, API — в <префикс>/api/,
 * лендинг — в <префикс>/. Префикс задаёт base в vite.config.ts.
 */

/** Кабинет: '/zapas/app/'. */
export const APP_BASE = import.meta.env.BASE_URL

/** Корень приложения, он же лендинг: '/zapas/'. */
export const ROOT = APP_BASE.replace(/app\/$/, '')

export const API_BASE = ROOT + 'api/v1'

/** Для BrowserRouter: без слеша на конце. */
export const ROUTER_BASENAME = APP_BASE.replace(/\/$/, '')
