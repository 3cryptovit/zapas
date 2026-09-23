/**
 * Клиент API (§11).
 *
 * Сессия живёт в httpOnly-cookie, поэтому токен здесь не хранится
 * и не передаётся — браузер шлёт cookie сам. Из JavaScript читается
 * только CSRF-токен: его копия лежит в обычной cookie, и её нужно
 * положить в заголовок (double-submit, ADR-007).
 */

const BASE = '/api/v1'

/** ApiError несёт разобранный ответ RFC 9457. */
export class ApiError extends Error {
  readonly status: number
  readonly type: string
  readonly title: string
  readonly detail: string
  readonly fields: FieldError[]
  /** extensions — дополнительные поля ответа, например on_hand при 409. */
  readonly extensions: Record<string, unknown>

  constructor(status: number, problem: ProblemLike) {
    super(problem.detail || problem.title || `Ошибка ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.type = problem.type ?? ''
    this.title = problem.title ?? 'Ошибка'
    this.detail = problem.detail ?? ''
    this.fields = (problem.errors as FieldError[]) ?? []

    const { type, title, detail, status: _s, errors, instance, ...rest } = problem
    void type
    void title
    void detail
    void _s
    void errors
    void instance
    this.extensions = rest
  }

  /** Сообщение для поля формы. */
  fieldError(field: string): string | undefined {
    return this.fields.find((e) => e.field === field)?.message
  }

  /** unauthorized: сессия кончилась — нужно на вход. */
  get unauthorized(): boolean {
    return this.status === 401
  }
}

export interface Problem {
  type: string
  title: string
  status: number
  detail?: string
  instance?: string
  errors?: FieldError[]
}

/**
 * ProblemLike — тело ошибки как оно приходит: обязательные поля RFC 9457
 * плюс произвольные расширения на верхнем уровне (например on_hand при 409).
 */
export type ProblemLike = Partial<Problem> & { [key: string]: unknown }

export interface FieldError {
  field: string
  message: string
}

/** csrfToken достаёт токен из cookie, которую выставил сервер. */
function csrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)zapas_csrf=([^;]+)/)
  return match?.[1] ? decodeURIComponent(match[1]) : ''
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** idempotencyKey обязателен для движений и заказов (§11). */
  idempotencyKey?: string
  /** raw — тело передаётся как есть: так загружается CSV. */
  raw?: { body: string; contentType: string }
  signal?: AbortSignal
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers: Record<string, string> = {}

  if (method !== 'GET' && method !== 'HEAD') {
    headers['X-CSRF-Token'] = csrfToken()
  }
  if (options.idempotencyKey) {
    headers['Idempotency-Key'] = options.idempotencyKey
  }

  let body: BodyInit | undefined
  if (options.raw) {
    headers['Content-Type'] = options.raw.contentType
    body = options.raw.body
  } else if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(options.body)
  }

  const response = await fetch(BASE + path, {
    method,
    headers,
    body,
    // Cookie сессии обязательна: без неё сервер ответит 401.
    credentials: 'same-origin',
    signal: options.signal,
  })

  if (response.status === 204) {
    return undefined as T
  }

  const text = await response.text()
  const parsed: unknown = text ? safeParse(text) : null

  if (!response.ok) {
    throw new ApiError(response.status, (parsed as ProblemLike) ?? { title: response.statusText })
  }
  return parsed as T
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

/** newIdempotencyKey — ключ на одну попытку отправки формы (FR-10). */
export function newIdempotencyKey(): string {
  return crypto.randomUUID()
}
