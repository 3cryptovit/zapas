import { API_BASE, APP_BASE, ROOT, ROUTER_BASENAME } from './paths'

// Кабинет под префиксом: запрос на /api/v1 от корня домена ушёл бы мимо
// приложения — в портфолио на vitalness.ru, которое ответит 404.
describe('пути приложения под префиксом', () => {
  it('кабинет, корень и API лежат под /zapas', () => {
    expect(APP_BASE).toBe('/zapas/app/')
    expect(ROOT).toBe('/zapas/')
    expect(API_BASE).toBe('/zapas/api/v1')
  })

  it('роутер получает базу без слеша на конце', () => {
    expect(ROUTER_BASENAME).toBe('/zapas/app')
  })
})
