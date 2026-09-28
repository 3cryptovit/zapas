import { forwardRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { AlertCircle, AlertTriangle, ArrowLeft, CheckCircle2, Info, X, type LucideIcon } from 'lucide-react'

import { formatQty } from '@/lib/format'

/**
 * Базовые элементы интерфейса.
 *
 * Сетка 8 px: отступы кратны 4 и 8. Карточки скруглены на 12 px,
 * кнопки и поля — на 8 px. На телефоне кнопки и поля не ниже 44 px,
 * чтобы в них попадали пальцем; на десктопе — 40 px и компактнее.
 */

/* ——— Заголовок страницы ——— */

interface PageHeaderProps {
  title: ReactNode
  description?: ReactNode
  /** Ссылка «назад» над заголовком. */
  back?: ReactNode
  actions?: ReactNode
}

export function PageHeader({ title, description, back, actions }: PageHeaderProps) {
  return (
    <header className="mb-6 flex flex-wrap items-end justify-between gap-4 sm:mb-8">
      <div className="min-w-0">
        {back && <div className="mb-2">{back}</div>}
        <h1 className="text-2xl font-semibold tracking-tight text-slate-900">{title}</h1>
        {description && <p className="mt-1 text-sm text-slate-500">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}

/* ——— Карточка ——— */

interface CardProps {
  title?: ReactNode
  description?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
  /** Содержимое без внутренних отступов: таблица до краёв. */
  flush?: boolean
}

export function Card({ title, description, action, children, className = '', flush = false }: CardProps) {
  return (
    <section className={`rounded-xl border border-slate-200 bg-white shadow-card ${className}`}>
      {(title || action) && (
        <header className="flex flex-wrap items-start justify-between gap-3 px-5 pt-5 sm:px-6">
          <div className="min-w-0">
            {typeof title === 'string' ? (
              <h2 className="text-[15px] font-semibold text-slate-900">{title}</h2>
            ) : (
              title
            )}
            {description && <p className="mt-0.5 text-sm text-slate-500">{description}</p>}
          </div>
          {action}
        </header>
      )}
      <div className={flush ? (title || action ? 'pt-4' : '') : 'px-5 py-5 sm:px-6'}>{children}</div>
    </section>
  )
}

/* ——— Кнопки ——— */

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
type ButtonSize = 'md' | 'sm'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
  loading?: boolean
  icon?: LucideIcon
}

const buttonVariants: Record<ButtonVariant, string> = {
  primary:
    'bg-brand-600 text-white hover:bg-brand-700 disabled:bg-slate-200 disabled:text-slate-400',
  secondary:
    'bg-white text-slate-700 ring-1 ring-inset ring-slate-300 hover:bg-slate-50 hover:text-slate-900 disabled:text-slate-400',
  ghost: 'text-slate-600 hover:bg-slate-100 hover:text-slate-900 disabled:text-slate-400',
  danger:
    'bg-white text-red-700 ring-1 ring-inset ring-red-200 hover:bg-red-50 disabled:text-slate-400 disabled:ring-slate-200',
}

const buttonSizes: Record<ButtonSize, string> = {
  md: 'min-h-11 px-4 text-sm sm:min-h-10',
  sm: 'min-h-9 px-3 text-[13px] sm:min-h-8',
}

export function Button({
  variant = 'primary',
  size = 'md',
  loading = false,
  icon: Icon,
  disabled,
  children,
  className = '',
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={`inline-flex items-center justify-center gap-2 rounded-lg font-medium whitespace-nowrap transition-colors duration-150 disabled:cursor-not-allowed ${buttonVariants[variant]} ${buttonSizes[size]} ${className}`}
    >
      {loading ? <Spinner /> : Icon ? <Icon aria-hidden="true" className="size-4 shrink-0" strokeWidth={1.75} /> : null}
      {children}
    </button>
  )
}

export function Spinner() {
  return (
    <span
      aria-hidden="true"
      className="size-4 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent opacity-70"
    />
  )
}

/* ——— Поля ——— */

interface FieldProps {
  label: string
  hint?: string
  error?: string
  children: ReactNode
}

export function Field({ label, hint, error, children }: FieldProps) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-[13px] font-medium text-slate-700">{label}</span>
      {children}
      {error ? (
        <span className="mt-1.5 block text-[13px] text-red-700">{error}</span>
      ) : hint ? (
        <span className="mt-1.5 block text-[13px] text-slate-500">{hint}</span>
      ) : null}
    </label>
  )
}

const control =
  'block rounded-lg border border-slate-300 bg-white px-3 text-slate-900 transition-colors placeholder:text-slate-400 hover:border-slate-400 focus:border-brand-500 focus:outline-none focus:ring-3 focus:ring-brand-500/15 disabled:bg-slate-50 disabled:text-slate-500'

/**
 * Поле по умолчанию на всю ширину. Если ширину передали — w-full не
 * добавляется: два правила ширины в одном class спорят, и побеждает то,
 * что раньше в собранном CSS, а не то, что написали последним.
 */
function withWidth(className = ''): string {
  return /(^|\s)(w-|flex-1)/.test(className) ? className : `w-full ${className}`
}

// forwardRef нужен форме пересчёта: она переводит фокус к следующей
// позиции по Enter (FR-15).
export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  function Input(props, ref) {
    return <input {...props} ref={ref} className={`${control} min-h-11 sm:min-h-10 ${withWidth(props.className)}`} />
  },
)

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`${control} min-h-11 pr-8 sm:min-h-10 ${withWidth(props.className)}`} />
}

export function Textarea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={`${control} py-2.5 ${withWidth(props.className)}`} />
}

/* ——— Таблица ———
 * Воздух между строками и тихие заголовки: глаз идёт по строке слева
 * направо, а не спотыкается о сетку. */

export function Table({ children }: { children: ReactNode }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">{children}</table>
    </div>
  )
}

export function Th({
  children,
  align = 'left',
  className = '',
}: {
  children?: ReactNode
  align?: 'left' | 'right'
  className?: string
}) {
  return (
    <th
      className={`border-b border-slate-200 px-3 pb-3 text-xs font-medium whitespace-nowrap text-slate-500 first:pl-5 last:pr-5 sm:first:pl-6 sm:last:pr-6 ${
        align === 'right' ? 'text-right' : 'text-left'
      } ${className}`}
    >
      {children}
    </th>
  )
}

export function Td({
  children,
  align = 'left',
  className = '',
}: {
  children?: ReactNode
  align?: 'left' | 'right'
  className?: string
}) {
  return (
    <td
      className={`border-b border-slate-100 px-3 py-3.5 align-middle first:pl-5 last:pr-5 sm:first:pl-6 sm:last:pr-6 ${
        align === 'right' ? 'text-right tabular-nums' : ''
      } ${className}`}
    >
      {children}
    </td>
  )
}

/* ——— Бейдж ——— */

export function Badge({ children, className = 'bg-slate-100 text-slate-600' }: { children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium whitespace-nowrap ${className}`}>
      {children}
    </span>
  )
}

/* ——— Пустое состояние ——— */

interface EmptyStateProps {
  title: string
  hint?: string
  action?: ReactNode
  icon?: LucideIcon
}

/** EmptyState — пустое состояние с подсказкой, что делать (§6.3). */
export function EmptyState({ title, hint, action, icon: Icon }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center gap-2 px-4 py-12 text-center">
      {Icon && (
        <span className="mb-2 flex size-10 items-center justify-center rounded-full bg-slate-100 text-slate-400">
          <Icon aria-hidden="true" className="size-5" strokeWidth={1.75} />
        </span>
      )}
      <p className="text-[15px] font-medium text-slate-900">{title}</p>
      {hint && <p className="max-w-sm text-sm text-slate-500">{hint}</p>}
      {action && <div className="mt-3">{action}</div>}
    </div>
  )
}

/* ——— Сообщения ——— */

interface AlertProps {
  kind?: 'error' | 'warning' | 'info' | 'success'
  title?: string
  children: ReactNode
}

const alertStyles: Record<NonNullable<AlertProps['kind']>, { box: string; icon: LucideIcon; iconColor: string }> = {
  error: { box: 'bg-red-50 text-red-700 ring-red-100', icon: AlertCircle, iconColor: 'text-red-500' },
  warning: { box: 'bg-amber-50 text-amber-700 ring-amber-100', icon: AlertTriangle, iconColor: 'text-amber-500' },
  success: { box: 'bg-emerald-50 text-emerald-700 ring-emerald-100', icon: CheckCircle2, iconColor: 'text-emerald-500' },
  info: { box: 'bg-slate-50 text-slate-600 ring-slate-200', icon: Info, iconColor: 'text-slate-400' },
}

export function Alert({ kind = 'info', title, children }: AlertProps) {
  const style = alertStyles[kind]
  const Icon = style.icon
  return (
    <div role={kind === 'error' ? 'alert' : undefined} className={`flex gap-3 rounded-lg px-4 py-3 text-sm ring-1 ring-inset ${style.box}`}>
      <Icon aria-hidden="true" className={`mt-0.5 size-4 shrink-0 ${style.iconColor}`} strokeWidth={2} />
      <div className="min-w-0">
        {title && <p className="font-medium">{title}</p>}
        <div className={title ? 'mt-0.5' : ''}>{children}</div>
      </div>
    </div>
  )
}

/** Skeleton — заглушка на время загрузки. */
export function Skeleton({ className = '' }: { className?: string }) {
  return <div className={`animate-pulse rounded-lg bg-slate-200/70 ${className}`} />
}

/* ——— Модальное окно ——— */

interface ModalProps {
  open: boolean
  title: string
  description?: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
}

export function Modal({ open, title, description, onClose, children, footer }: ModalProps) {
  if (!open) return null

  // Портал в body: оверлей с position: fixed внутри анимированного или
  // трансформированного предка позиционируется от него, а не от окна —
  // диалог открывался посреди длинной страницы и уходил за край экрана.
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/30 p-0 sm:items-center sm:p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onClose}
    >
      <div
        // На телефоне лист выезжает снизу: так до него дотягивается палец.
        className="animate-rise max-h-[92vh] w-full overflow-y-auto rounded-t-xl bg-white shadow-pop sm:max-w-md sm:rounded-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="sticky top-0 flex items-start justify-between gap-4 bg-white px-6 pt-5 pb-2">
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-slate-900">{title}</h2>
            {description && <p className="mt-0.5 text-sm text-slate-500">{description}</p>}
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Закрыть"
            className="-mr-2 flex size-9 shrink-0 items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-700"
          >
            <X aria-hidden="true" className="size-4" strokeWidth={2} />
          </button>
        </header>
        <div className="px-6 pt-2 pb-6">{children}</div>
        {footer && (
          <footer className="sticky bottom-0 flex flex-wrap justify-end gap-2 border-t border-slate-100 bg-white px-6 py-4">
            {footer}
          </footer>
        )}
      </div>
    </div>,
    document.body,
  )
}

/* ——— Ссылка «назад» ——— */

export function BackLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link
      to={to}
      className="inline-flex items-center gap-1.5 text-[13px] font-medium text-slate-500 transition-colors hover:text-slate-900"
    >
      <ArrowLeft aria-hidden="true" className="size-3.5" strokeWidth={2} />
      {children}
    </Link>
  )
}

/* ——— Количество со знаком ———
 * Приход зелёный, расход обычным цветом: расход — будни склада, а не
 * тревога, красить его в красный значило бы кричать на каждой строке. */

export function SignedQty({ qty }: { qty: string }) {
  const n = Number(qty)
  return (
    <span className={`shrink-0 text-sm font-semibold whitespace-nowrap tabular-nums ${n > 0 ? 'text-emerald-700' : 'text-slate-900'}`}>
      {n > 0 ? '+' : n < 0 ? '−' : ''}
      {formatQty(Math.abs(n))}
    </span>
  )
}
