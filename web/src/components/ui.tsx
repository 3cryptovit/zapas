import { forwardRef, type ReactNode } from 'react'

/**
 * Базовые элементы интерфейса.
 *
 * Все размеры рассчитаны на экран от 360 px (§6.3): кнопки и поля
 * не меньше 44 px по высоте, чтобы в них попадали пальцем.
 */

interface CardProps {
  title?: ReactNode
  action?: ReactNode
  children: ReactNode
  className?: string
}

export function Card({ title, action, children, className = '' }: CardProps) {
  return (
    <section
      className={`border border-slate-300 bg-white ${className}`}
    >
      {(title || action) && (
        <header className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-300 px-5 py-4">
          {typeof title === 'string' ? (
            <h2 className="eyebrow text-slate-900">{title}</h2>
          ) : (
            title
          )}
          {action}
        </header>
      )}
      <div className="px-5 py-4">{children}</div>
    </section>
  )
}

type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'ghost'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  loading?: boolean
}

// В монохроме роль кнопки несёт вес границы и заливка, а не оттенок.
// Опасное действие отличается удвоенной рамкой: она читается как
// «остановись», не требуя красного.
const buttonStyles: Record<ButtonVariant, string> = {
  primary:
    'bg-slate-900 text-white hover:bg-slate-700 disabled:bg-slate-300 disabled:text-slate-500',
  secondary:
    'bg-white text-slate-900 ring-1 ring-inset ring-slate-400 hover:bg-slate-100 disabled:text-slate-400',
  danger:
    'bg-white text-slate-900 ring-2 ring-inset ring-slate-900 hover:bg-slate-900 hover:text-white disabled:ring-slate-300 disabled:text-slate-400',
  ghost: 'text-slate-600 hover:bg-slate-100 disabled:text-slate-300',
}

export function Button({
  variant = 'primary',
  loading = false,
  disabled,
  children,
  className = '',
  ...rest
}: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      // min-h-11 — палец попадает, даже если кнопка узкая.
      className={`inline-flex min-h-11 items-center justify-center gap-2 px-5 text-[0.6875rem] font-semibold uppercase tracking-[0.14em] transition disabled:cursor-not-allowed ${buttonStyles[variant]} ${className}`}
    >
      {loading && <Spinner />}
      {children}
    </button>
  )
}

export function Spinner() {
  return (
    <span
      aria-hidden="true"
      className="h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent"
    />
  )
}

interface FieldProps {
  label: string
  hint?: string
  error?: string
  children: ReactNode
}

export function Field({ label, hint, error, children }: FieldProps) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-medium text-slate-700">{label}</span>
      {children}
      {error ? (
        <span className="mt-1 block text-sm text-red-600">{error}</span>
      ) : hint ? (
        <span className="mt-1 block text-sm text-slate-500">{hint}</span>
      ) : null}
    </label>
  )
}

// forwardRef нужен форме пересчёта: она переводит фокус к следующей
// позиции по Enter (FR-15).
export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  function Input(props, ref) {
    return (
      <input
        {...props}
        ref={ref}
        className={`block min-h-11 w-full border-0 bg-white px-3 text-slate-900 ring-1 ring-inset ring-slate-400 placeholder:text-slate-400 focus:ring-2 focus:ring-inset focus:ring-slate-900 ${props.className ?? ''}`}
      />
    )
  },
)

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      {...props}
      className={`block min-h-11 w-full border-0 bg-white px-3 text-slate-900 ring-1 ring-inset ring-slate-400 focus:ring-2 focus:ring-inset focus:ring-slate-900 ${props.className ?? ''}`}
    />
  )
}

interface EmptyStateProps {
  title: string
  hint?: string
  action?: ReactNode
}

/** EmptyState — пустое состояние с подсказкой, что делать (§6.3). */
export function EmptyState({ title, hint, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center gap-3 px-4 py-10 text-center">
      <p className="text-base font-medium text-slate-900">{title}</p>
      {hint && <p className="max-w-sm text-sm text-slate-500">{hint}</p>}
      {action}
    </div>
  )
}

interface AlertProps {
  kind?: 'error' | 'warning' | 'info' | 'success'
  title?: string
  children: ReactNode
}

// Без цвета важность несёт вес левой линейки: чем толще, тем срочнее.
// Одинаковые серые плашки различались бы только текстом, а его читают
// вторым.
const alertStyles = {
  error: 'border-l-4 border-slate-900 bg-slate-100 text-slate-900 ring-slate-300',
  warning: 'border-l-4 border-slate-500 bg-slate-50 text-slate-900 ring-slate-200',
  success: 'border-l-2 border-slate-400 bg-white text-slate-800 ring-slate-200',
  info: 'border-l border-slate-300 bg-slate-50 text-slate-700 ring-slate-200',
}

export function Alert({ kind = 'info', title, children }: AlertProps) {
  return (
    <div className={`px-4 py-3 text-sm ring-1 ring-inset ${alertStyles[kind]}`}>
      {title && <p className="font-medium">{title}</p>}
      <div className={title ? 'mt-1' : ''}>{children}</div>
    </div>
  )
}

/** Skeleton — заглушка на время загрузки. */
export function Skeleton({ className = '' }: { className?: string }) {
  return <div className={`animate-pulse bg-slate-200 ${className}`} />
}

interface ModalProps {
  open: boolean
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
}

export function Modal({ open, title, onClose, children, footer }: ModalProps) {
  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/40 p-0 sm:items-center sm:p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onClose}
    >
      <div
        // На телефоне лист выезжает снизу: так до него дотягивается палец.
        className="max-h-[92vh] w-full overflow-y-auto border border-slate-900 bg-white sm:max-w-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="sticky top-0 flex items-center justify-between border-b border-slate-100 bg-white px-4 py-3">
          <h2 className="text-base font-semibold text-slate-900">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Закрыть"
            className="min-h-11 min-w-11 text-slate-500 hover:bg-slate-100"
          >
            ✕
          </button>
        </header>
        <div className="px-4 py-4">{children}</div>
        {footer && (
          <footer className="sticky bottom-0 flex flex-wrap justify-end gap-2 border-t border-slate-100 bg-white px-4 py-3">
            {footer}
          </footer>
        )}
      </div>
    </div>
  )
}
