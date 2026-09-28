import { loadDemoLogin, saveDemoLogin } from './demoLogin'

// Пароль пошагового демо сервер отдаёт один раз, дальше его хранит браузер.
describe('логин пошагового демо', () => {
  beforeEach(() => localStorage.clear())

  it('показывается в своём демо', () => {
    saveDemoLogin('t-1', { email: 'demo-abcd2345@sandbox.local', password: 'p' })
    expect(loadDemoLogin('t-1')).toEqual({ email: 'demo-abcd2345@sandbox.local', password: 'p' })
  })

  it('не показывается в чужом: после сброса демо у тенанта новый id', () => {
    saveDemoLogin('t-1', { email: 'demo-abcd2345@sandbox.local', password: 'p' })
    expect(loadDemoLogin('t-2')).toBeNull()
  })

  it('испорченная запись не роняет кабинет', () => {
    localStorage.setItem('zapas:demo-login', '{не json')
    expect(loadDemoLogin('t-1')).toBeNull()
  })
})
