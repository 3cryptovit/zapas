/**
 * Нагрузочный сценарий: дашборд и запись движений (§12).
 *
 * Цели из ТЗ: GET /dashboard не дольше 300 мс, POST /movements не
 * дольше 150 мс — оба по p95, на базе с 300 позициями и миллионом
 * движений. Фикстура — tests/load/fixture.sql.
 *
 * НЕ ЗАПУСКАТЬ ПРОТИВ ПРОДА. Сервер там общий с пятью другими
 * проектами, у одного из них настоящие посетители. Сценарий пишет
 * движения и нагружает машину — ему место на локальной базе.
 *
 *   k6 run -e BASE_URL=http://localhost:8080 tests/load/api.js
 */

import http from 'k6/http'
import { check, fail } from 'k6'
import { Trend } from 'k6/metrics'

const BASE = __ENV.BASE_URL || 'http://localhost:8080'
const EMAIL = __ENV.EMAIL || 'perf@zapas.local'
const PASSWORD = __ENV.PASSWORD || 'perf-parol-42'

const dashboard = new Trend('zapas_dashboard', true)
const movement = new Trend('zapas_movement', true)

/** session — сессия текущего виртуального пользователя. */
let session = null

export const options = {
  scenarios: {
    // Плавный разгон: интересует поведение под нагрузкой, а не
    // реакция на мгновенный наплыв.
    api: {
      executor: 'ramping-vus',
      startVUs: 1,
      stages: [
        { duration: '20s', target: 10 },
        { duration: '40s', target: 10 },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '10s',
    },
  },
  thresholds: {
    // Пороги из раздела 12 ТЗ. Провал любого — красный прогон.
    zapas_dashboard: ['p(95)<300'],
    zapas_movement: ['p(95)<150'],
    checks: ['rate>0.99'],
    http_req_failed: ['rate<0.01'],
  },
}

/**
 * login заводит сессию и возвращает заголовки для последующих запросов.
 *
 * Cookie передаются заголовком, а не через банку k6: между итерациями
 * k6 её очищает, и сессия терялась бы после первого же запроса.
 */
function login() {
  const res = http.post(
    `${BASE}/api/v1/auth/login`,
    JSON.stringify({ email: EMAIL, password: PASSWORD }),
    { headers: { 'Content-Type': 'application/json' }, tags: { name: 'login' } },
  )
  if (res.status !== 200) {
    fail(`вход не удался: ${res.status} ${res.body}`)
  }

  // Cookie берутся из самого ответа, а не из банки по адресу: у них путь
  // приложения (/zapas/), и банка для корня адреса их не отдаёт.
  const sid = res.cookies.zapas_session && res.cookies.zapas_session[0].value
  // Двойная отправка: токен приходит в читаемой cookie и должен
  // вернуться заголовком (ADR-007).
  const csrf = res.cookies.zapas_csrf && res.cookies.zapas_csrf[0].value
  if (!sid || !csrf) {
    fail('в ответе нет cookie сессии или CSRF-токена')
  }
  return {
    'Content-Type': 'application/json',
    'X-CSRF-Token': csrf,
    Cookie: `zapas_session=${sid}; zapas_csrf=${csrf}`,
  }
}

export function setup() {
  const res = http.get(`${BASE}/readyz`)
  if (res.status !== 200) {
    fail(`сервис не готов: ${res.status} ${res.body}`)
  }
  return {}
}

export default function () {
  // Сессия на виртуального пользователя, а не на итерацию: логин на
  // каждый запрос мерил бы argon2id, а не дашборд. У каждого VU в k6
  // своя копия модуля, поэтому обычной переменной достаточно.
  if (!session) {
    const headers = login()
    const items = http.get(`${BASE}/api/v1/items`, { headers }).json('items')
    if (!items || items.length === 0) {
      fail('позиций нет — фикстура не загружена')
    }
    session = { headers, items }
  }
  const state = session

  const d = http.get(`${BASE}/api/v1/dashboard`, {
    headers: state.headers,
    tags: { name: 'dashboard' },
  })
  dashboard.add(d.timings.duration)
  check(d, { 'дашборд отдан': (r) => r.status === 200 })

  const item = state.items[Math.floor(Math.random() * state.items.length)]
  const m = http.post(
    `${BASE}/api/v1/movements`,
    JSON.stringify({ type: 'usage', item_id: item.id, qty: '1.000' }),
    {
      headers: {
        ...state.headers,
        // Ключ на попытку: повтор не должен создать второе движение.
        'Idempotency-Key': uuid(),
      },
      tags: { name: 'movement' },
    },
  )
  movement.add(m.timings.duration)
  check(m, { 'движение записано': (r) => r.status === 201 })
}

function uuid() {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}
