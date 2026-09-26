import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

import { App } from './App'
import { ROUTER_BASENAME } from './lib/paths'
import './styles.css'

const queryClient = new QueryClient({
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
