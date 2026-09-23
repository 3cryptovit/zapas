/**
 * Единственная логика лендинга: кнопка демо (§8).
 *
 * До клика запросов к API нет вообще — это требование раздела 8
 * и заодно половина оценки Lighthouse.
 */

const buttons = document.querySelectorAll('[data-demo]')
const note = document.querySelector('[data-demo-note]')

let pending = false

async function openDemo() {
  if (pending) return
  pending = true

  const original = note?.textContent ?? ''
  setButtons(true)
  if (note) note.textContent = 'Готовим демо: 40 позиций и 90 дней истории…'

  try {
    const response = await fetch('/api/v1/sandbox', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      // Пустое поле-ловушка: браузер его не трогает, бот заполнит.
      body: JSON.stringify({ website: '' }),
      credentials: 'same-origin',
    })

    if (response.ok) {
      const data = await response.json()
      window.location.href = data.redirect_to || '/app/'
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

function setButtons(loading) {
  for (const button of buttons) {
    button.disabled = loading
    button.dataset.loading = loading ? 'true' : 'false'
  }
}

for (const button of buttons) {
  button.addEventListener('click', openDemo)
}
