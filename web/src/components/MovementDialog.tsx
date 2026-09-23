import { useEffect, useState } from 'react'

import { ApiError } from '@/lib/api'
import { formatQty, formatQtyWithUnit } from '@/lib/format'
import { useCreateMovement } from '@/lib/queries'
import type { BaseUnit, Qty } from '@/lib/types'
import { Alert, Button, Field, Input, Modal, Select } from './ui'

interface DialogItem {
  id: string
  name: string
  base_unit: BaseUnit
  on_hand: Qty
}

interface Props {
  item: DialogItem | null
  onClose: () => void
}

type MovementType = 'receipt' | 'usage' | 'writeoff'

const typeLabels: Record<MovementType, string> = {
  receipt: 'Приход',
  usage: 'Расход',
  writeoff: 'Списание',
}

/** Причины списания (FR-6): без причины списание не принимается. */
const reasons = [
  { value: 'spoiled', label: 'Испортилось' },
  { value: 'broken', label: 'Бой' },
  { value: 'expired', label: 'Истёк срок' },
  { value: 'tasting', label: 'Проливы и дегустации' },
  { value: 'other', label: 'Другое' },
]

/**
 * Форма движения. Количество вводится положительным — знак ставит
 * сервер по типу (§11).
 */
export function MovementDialog({ item, onClose }: Props) {
  const [type, setType] = useState<MovementType>('usage')
  const [qty, setQty] = useState('')
  const [reason, setReason] = useState('spoiled')
  const [comment, setComment] = useState('')
  const create = useCreateMovement()

  useEffect(() => {
    if (item) {
      setType('usage')
      setQty('')
      setReason('spoiled')
      setComment('')
      create.reset()
    }
    // create.reset стабилен между рендерами, в зависимости не нужен.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item])

  if (!item) return null

  const error = create.error instanceof ApiError ? create.error : null
  // 409 при нехватке остатка несёт текущий остаток (FR-8).
  const onHandFromError = error?.extensions?.on_hand as string | undefined

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    create.mutate(
      {
        type,
        item_id: item.id,
        qty,
        reason: type === 'writeoff' ? reason : undefined,
        comment: comment || undefined,
      },
      { onSuccess: onClose },
    )
  }

  return (
    <Modal open title={item.name} onClose={onClose}>
      <form onSubmit={submit} className="space-y-4">
        <p className="text-sm text-slate-600">
          Сейчас на складе {formatQtyWithUnit(item.on_hand, item.base_unit)}
        </p>

        <Field label="Тип движения">
          <Select value={type} onChange={(e) => setType(e.target.value as MovementType)}>
            {(Object.keys(typeLabels) as MovementType[]).map((value) => (
              <option key={value} value={value}>
                {typeLabels[value]}
              </option>
            ))}
          </Select>
        </Field>

        <Field
          label={`Количество, ${item.base_unit === 'kg' ? 'кг' : item.base_unit === 'l' ? 'л' : 'шт'}`}
          hint="Положительное число: знак поставит система"
          error={error?.fieldError('qty')}
        >
          <Input
            // inputMode числовой: на телефоне открывается цифровая клавиатура.
            inputMode="decimal"
            autoFocus
            required
            placeholder="1,5"
            value={qty}
            onChange={(e) => setQty(e.target.value.replace(',', '.'))}
          />
        </Field>

        {type === 'writeoff' && (
          <Field label="Причина" error={error?.fieldError('reason')}>
            <Select value={reason} onChange={(e) => setReason(e.target.value)}>
              {reasons.map((r) => (
                <option key={r.value} value={r.value}>
                  {r.label}
                </option>
              ))}
            </Select>
          </Field>
        )}

        <Field label="Комментарий" hint="Необязательно">
          <Input value={comment} onChange={(e) => setComment(e.target.value)} />
        </Field>

        {error && (
          <Alert kind="error" title={error.title}>
            {error.detail || error.message}
            {onHandFromError && (
              <p className="mt-1">
                Текущий остаток: {formatQty(onHandFromError)}. Исправьте цифру
                пересчётом — тогда остаток сойдётся с журналом.
              </p>
            )}
          </Alert>
        )}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={onClose}>
            Отмена
          </Button>
          <Button type="submit" loading={create.isPending}>
            Записать
          </Button>
        </div>
      </form>
    </Modal>
  )
}
