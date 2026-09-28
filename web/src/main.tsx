import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MutationCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

import { App } from './App'
import { ROUTER_BASENAME } from './lib/paths'
import { keys } from './lib/queries'
import './styles.css'

const queryClient: QueryClient = new QueryClient({
  // Шаг демо засчитывается по данным, а данные меняет любое действие:
  // поставщик в настройках, пересчёт, прочитанная сводка. Перечитываем
  // шаги после каждой успешной мутации, а не вписываем это в каждый хук.
  mutationCache: new MutationCache({
    onSuccess: (_data, _variables, _context, mutation) => {
      // Вход и выход меняют сессию, а не данные: после выхода запрос шагов
      // ушёл бы уже без cookie и вернул 401.
      if (mutation.meta?.session) return
      void queryClient.invalidateQueries({ queryKey: keys.guide })
    },
  }),
  defaultOptions: {
    queries: {
      // Данные обновляются после каждого действия и при возврате во вкладку (§6.3).
      refetchOnWindowFocus: true,
      staleTime: 10_000,
      retry: 1,
    },
  },
})

const root = document.getElementById('root')
if (!root) throw new Error('не найден #root')

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter basename={ROUTER_BASENAME}>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
