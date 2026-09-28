/**
 * Единственная логика лендинга: кнопки демо (§8).
 *
 * До клика запросов к API нет вообще — это требование раздела 8
 * и заодно половина оценки Lighthouse.
 */

// '/zapas/': лендинг, кабинет и API живут под одним префиксом.
const ROOT = import.meta.env.BASE_URL

// Логин пошагового демо: сервер отдаёт пароль один раз, кабинет покажет
// его посетителю из этого ключа (web/src/lib/demoLogin.ts).
const LOGIN_KEY = 'zapas:demo-login'

const buttons = document.querySelectorAll('[data-demo]')
const note = document.querySelector('[data-demo-note]')

const preparing = {
  guided: 'Заводим пустую кофейню и ваш логин…',
  ready: 'Готовим демо: 38 позиций и 90 дней истории…',
}

let pending = false

async function openDemo(mode) {
  if (pending) return
  pending = true

  const original = note?.textContent ?? ''
  setButtons(true)
  if (note) note.textContent = preparing[mode]

  try {
    const response = await fetch(`${ROOT}api/v1/sandbox`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      // Пустое поле-ловушка: браузер его не трогает, бот заполнит.
      body: JSON.stringify({ website: '', mode }),
      credentials: 'same-origin',
    })

    if (response.ok) {
      const data = await response.json()
      if (data.login) saveLogin(data.tenant_id, data.login)
      window.location.href = data.redirect_to || `${ROOT}app/`
      return
    }

    const problem = await response.json().catch(() => null)
    if (note) {
      note.textContent =
        problem?.detail ||
        'Не получилось открыть демо. Попробуйте ещё раз через пару минут.'
      note.classList.add('hero__note--error')
    }
  } catch {
    if (note) {
      note.textContent = 'Сервер не отвечает. Попробуйте ещё раз через минуту.'
      note.classList.add('hero__note--error')
    }
  } finally {
    pending = false
    setButtons(false)
    // Возвращаем исходную подсказку, если ошибки не было.
    if (note && !note.classList.contains('hero__note--error')) {
      note.textContent = original
    }
  }
}

function saveLogin(tenantId, login) {
  try {
    localStorage.setItem(LOGIN_KEY, JSON.stringify({ tenant_id: tenantId, ...login }))
  } catch {
    // Приватное окно: кабинет просто не покажет логин.
  }
}

function setButtons(loading) {
  for (const button of buttons) {
    button.disabled = loading
    button.dataset.loading = loading ? 'true' : 'false'
  }
}

for (const button of buttons) {
  const mode = button.dataset.demo === 'guided' ? 'guided' : 'ready'
  button.addEventListener('click', () => openDemo(mode))
}
