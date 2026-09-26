import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { Alert, Button, Field, Input } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { ROOT } from '@/lib/paths'
import { useLogin } from '@/lib/queries'

export function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  // Honeypot: настоящий браузер это поле не заполняет (§7.5).
  const [website, setWebsite] = useState('')
  const login = useLogin()
  const navigate = useNavigate()

  const error = login.error instanceof ApiError ? login.error : null

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4">
      <div className="w-full max-w-sm">
        <h1 className="text-2xl font-semibold text-slate-900">Zapas</h1>
        <p className="mt-1 text-sm text-slate-500">
          Склад, прогноз спроса и напоминания о заказе
        </p>

        <form
          className="mt-6 space-y-4 rounded-xl border border-slate-200 bg-white p-5 shadow-sm"
          onSubmit={(e) => {
            e.preventDefault()
            if (website) return
            login.mutate({ email, password }, { onSuccess: () => navigate('/') })
          }}
        >
          <Field label="Email" error={error?.fieldError('email')}>
            <Input
              type="email"
              autoComplete="username"
              required
              autoFocus
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </Field>

          <Field label="Пароль" error={error?.fieldError('password')}>
            <Input
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>

          {/* Ловушка для ботов: скрыта от людей, но видна скриптам. */}
          <input
            type="text"
            name="website"
            tabIndex={-1}
            autoComplete="off"
            value={website}
            onChange={(e) => setWebsite(e.target.value)}
            aria-hidden="true"
            className="hidden"
          />

          {error && !error.fields.length && (
            <Alert kind="error" title={error.title}>
              {error.detail || error.message}
            </Alert>
          )}

          <Button type="submit" className="w-full" loading={login.isPending}>
            Войти
          </Button>
        </form>

        <p className="mt-4 text-center text-sm text-slate-500">
          Нет аккаунта?{' '}
          <a href={ROOT} className="font-medium text-brand-700 hover:underline">
            Посмотрите демо без регистрации
          </a>
        </p>
      </div>
    </main>
  )
}
