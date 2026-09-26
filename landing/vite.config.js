import { defineConfig } from 'vite'

// Лендинг — статическая сборка без фреймворка (§8).
//
// React здесь не нужен: страница не меняется, а единственная логика —
// одна кнопка. Без фреймворка Lighthouse проходит с большим запасом,
// и до клика по кнопке демо запросов к API нет вообще.
export default defineConfig({
  // Лендинг — корень Zapas на общем домене: vitalness.ru/zapas/.
  base: '/zapas/',
  build: {
    target: 'es2020',
    cssMinify: true,
  },
})
