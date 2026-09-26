import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// Zapas живёт под префиксом на общем домене: vitalness.ru/zapas.
// Кабинет — <префикс>/app/, лендинг — <префикс>/, API — <префикс>/api/.
const prefix = '/zapas'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: `${prefix}/app/`,
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    // В деве фронт ходит в Go напрямую, cookie сессии остаётся same-origin.
    // Префикс срезается так же, как на проде его срезает nginx.
    proxy: {
      [`${prefix}/api`]: {
        target: 'http://localhost:8080',
        changeOrigin: false,
        rewrite: (path) => path.slice(prefix.length),
      },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
  },
})
