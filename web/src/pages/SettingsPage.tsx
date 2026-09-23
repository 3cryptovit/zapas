import { useState } from 'react'

import { Alert, Button, Card, EmptyState, Field, Input, Select, Skeleton } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { formatQtyWithUnit } from '@/lib/format'
import {
  useCategories,
  useCreateItem,
  useCreateSupplier,
  useImportCSV,
  useItems,
  useLinkTelegram,
  useMe,
  useSuppliers,
} from '@/lib/queries'

/** Настройки: номенклатура, поставщики, импорт CSV (§6). */
export function SettingsPage() {
  const { data: me } = useMe()

  return (
    <div className="space-y-5">
      <h1 className="text-2xl font-semibold text-slate-900">Настройки</h1>

      {me?.tenant.is_sandbox && (
        <Alert kind="info">
          В демо отключены внешние каналы, приглашение сотрудников и смена
          пароля — чтобы демо нельзя было использовать для рассылок.
        </Alert>
      )}

      <ImportCard />
      <ItemsCard />
      <SuppliersCard />
      <ChannelsCard />
    </div>
  )
}

const csvTemplate = [
  'Название;Категория;Единица;Поставщик;Остаток;Единица закупки;Коэффициент;Кратность',
  'Молоко 3,2%;Молочка;л;Молочная ферма;24;кор.;12;12',
  'Зерно Бразилия;Кофе;кг;Кофе-Импорт;8;уп.;1;1',
].join('\n')

/** Импорт CSV: сначала предпросмотр с ошибками, потом импорт (FR-4). */
function ImportCard() {
  const [csv, setCsv] = useState('')
  const importCSV = useImportCSV()
  const result = importCSV.data

  const readFile = async (file: File) => {
    setCsv(await file.text())
    importCSV.reset()
  }

  return (
    <Card title="Импорт номенклатуры из CSV">
      <div className="space-y-3">
        <p className="text-sm text-slate-600">
          Файл с колонками «Название» и «Единица» — остальные необязательны.
          Разделитель определяется сам: подойдёт и CSV из русского Excel.
        </p>

        <input
          type="file"
          accept=".csv,text/csv"
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (file) void readFile(file)
          }}
          className="block w-full text-sm file:mr-3 file:min-h-11 file:rounded-lg file:border-0 file:bg-slate-100 file:px-4 file:text-sm file:font-medium"
        />

        <Field label="Или вставьте содержимое">
          <textarea
            rows={5}
            value={csv}
            onChange={(e) => {
              setCsv(e.target.value)
              importCSV.reset()
            }}
            placeholder={csvTemplate}
            className="block w-full rounded-lg border-0 p-3 font-mono text-sm ring-1 ring-inset ring-slate-300 focus:ring-2 focus:ring-inset focus:ring-brand-500"
          />
        </Field>

        <div className="flex flex-wrap gap-2">
          <Button
            variant="secondary"
            disabled={!csv.trim()}
            loading={importCSV.isPending}
            onClick={() => importCSV.mutate({ csv, dryRun: true })}
          >
            Предпросмотр
          </Button>
          <Button
            disabled={!csv.trim() || !result || !result.dry_run || Boolean(result.errors?.length)}
            loading={importCSV.isPending}
            onClick={() => importCSV.mutate({ csv, dryRun: false })}
          >
            Импортировать
          </Button>
        </div>

        {importCSV.isError && (
          <Alert kind="error">
            {importCSV.error instanceof ApiError
              ? importCSV.error.detail || importCSV.error.message
              : importCSV.error.message}
          </Alert>
        )}

        {result && (
          <div className="space-y-2">
            {result.errors?.length ? (
              <Alert kind="error" title={`Ошибок в файле: ${result.errors.length}`}>
                <ul className="mt-1 space-y-0.5">
                  {result.errors.slice(0, 20).map((e, i) => (
                    <li key={i}>
                      Строка {e.line}
                      {e.column ? `, «${e.column}»` : ''}: {e.message}
                    </li>
                  ))}
                </ul>
                <p className="mt-2">
                  Импорт не выполнен: либо всё, либо ничего. Поправьте файл
                  и попробуйте снова.
                </p>
              </Alert>
            ) : result.dry_run ? (
              <Alert kind="success" title="Файл разобран">
                Позиций: {result.items}, с начальным остатком: {result.with_opening}.
                Нажмите «Импортировать», чтобы записать.
              </Alert>
            ) : (
              <Alert kind="success" title="Импорт завершён">
                Позиций: {result.items}, категорий заведено: {result.categories},
                поставщиков: {result.suppliers}.
              </Alert>
            )}
          </div>
        )}
      </div>
    </Card>
  )
}

function ItemsCard() {
  const { data: items, isLoading } = useItems()
  const { data: categories } = useCategories()
  const { data: suppliers } = useSuppliers()
  const create = useCreateItem()

  const [name, setName] = useState('')
  const [unit, setUnit] = useState('l')
  const [categoryId, setCategoryId] = useState('')
  const [supplierId, setSupplierId] = useState('')
  const [minQty, setMinQty] = useState('')

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    create.mutate(
      {
        name,
        base_unit: unit,
        category_id: categoryId || undefined,
        default_supplier_id: supplierId || undefined,
        manual_min_qty: minQty || undefined,
      },
      {
        onSuccess: () => {
          setName('')
          setMinQty('')
        },
      },
    )
  }

  return (
    <Card title={`Позиции (${items?.length ?? 0})`}>
      <form onSubmit={submit} className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <Field label="Название">
          <Input required value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Единица">
          <Select value={unit} onChange={(e) => setUnit(e.target.value)}>
            <option value="l">л</option>
            <option value="kg">кг</option>
            <option value="pcs">шт</option>
          </Select>
        </Field>
        <Field label="Категория">
          <Select value={categoryId} onChange={(e) => setCategoryId(e.target.value)}>
            <option value="">—</option>
            {categories?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Поставщик">
          <Select value={supplierId} onChange={(e) => setSupplierId(e.target.value)}>
            <option value="">—</option>
            {suppliers?.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Мин. остаток" hint="пока нет прогноза">
          <Input inputMode="decimal" value={minQty} onChange={(e) => setMinQty(e.target.value.replace(',', '.'))} />
        </Field>
        <div className="sm:col-span-2 lg:col-span-5">
          <Button type="submit" loading={create.isPending}>
            Добавить позицию
          </Button>
        </div>
      </form>

      {create.isError && <Alert kind="error">{create.error.message}</Alert>}

      {isLoading ? (
        <Skeleton className="h-24 w-full" />
      ) : !items?.length ? (
        <EmptyState title="Позиций пока нет" hint="Добавьте первую или импортируйте CSV." />
      ) : (
        <ul className="divide-y divide-slate-100 text-sm">
          {items.map((item) => (
            <li key={item.id} className="flex flex-wrap items-center gap-3 py-2">
              <span className="flex-1 font-medium text-slate-900">{item.name}</span>
              <span className="text-slate-500">{item.unit_label}</span>
              <span className="text-slate-500">{item.category_name ?? '—'}</span>
              <span className="text-slate-500">{item.supplier_name ?? 'без поставщика'}</span>
              {Number(item.manual_min_qty) > 0 && (
                <span className="text-slate-500">
                  мин. {formatQtyWithUnit(item.manual_min_qty, item.base_unit)}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

const weekdayNames = ['пн', 'вт', 'ср', 'чт', 'пт', 'сб', 'вс']

function SuppliersCard() {
  const { data: suppliers, isLoading } = useSuppliers()
  const create = useCreateSupplier()

  const [name, setName] = useState('')
  const [contact, setContact] = useState('')
  const [lead, setLead] = useState('1')
  const [cutoff, setCutoff] = useState('16:00')
  const [days, setDays] = useState<number[]>([1, 4])

  const toggle = (day: number) =>
    setDays((prev) => (prev.includes(day) ? prev.filter((d) => d !== day) : [...prev, day].sort()))

  return (
    <Card title={`Поставщики (${suppliers?.length ?? 0})`}>
      <form
        className="mb-4 space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          create.mutate(
            {
              name,
              contact: contact || undefined,
              lead_time_days: Number(lead),
              delivery_weekdays: days,
              order_cutoff: cutoff,
            },
            { onSuccess: () => setName('') },
          )
        }}
      >
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Название">
            <Input required value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Контакт" hint="телеграм, почта или телефон">
            <Input value={contact} onChange={(e) => setContact(e.target.value)} />
          </Field>
          <Field label="Срок поставки, дней">
            <Input type="number" min="0" max="60" value={lead} onChange={(e) => setLead(e.target.value)} />
          </Field>
          <Field label="Время отсечки">
            <Input type="time" value={cutoff} onChange={(e) => setCutoff(e.target.value)} />
          </Field>
        </div>

        <Field label="Дни доставки">
          <div className="flex flex-wrap gap-2">
            {weekdayNames.map((label, index) => {
              const day = index + 1
              const on = days.includes(day)
              return (
                <button
                  key={day}
                  type="button"
                  onClick={() => toggle(day)}
                  aria-pressed={on}
                  className={`min-h-11 min-w-11 rounded-lg text-sm font-medium ring-1 ring-inset ${
                    on
                      ? 'bg-brand-700 text-white ring-brand-700'
                      : 'bg-white text-slate-700 ring-slate-300'
                  }`}
                >
                  {label}
                </button>
              )
            })}
          </div>
        </Field>

        <Button type="submit" loading={create.isPending} disabled={days.length === 0}>
          Добавить поставщика
        </Button>
      </form>

      {create.isError && <Alert kind="error">{create.error.message}</Alert>}

      {isLoading ? (
        <Skeleton className="h-24 w-full" />
      ) : !suppliers?.length ? (
        <EmptyState
          title="Поставщиков пока нет"
          hint="Срок поставки и дни доставки нужны, чтобы система знала, когда напоминать о заказе."
        />
      ) : (
        <ul className="divide-y divide-slate-100 text-sm">
          {suppliers.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 py-2">
              <span className="flex-1 font-medium text-slate-900">{s.name}</span>
              <span className="text-slate-500">
                возит {s.delivery_weekdays.map((d) => weekdayNames[d - 1]).join(', ')}
              </span>
              <span className="text-slate-500">срок {s.lead_time_days} дн.</span>
              <span className="text-slate-500">
                до {String(s.order_cutoff.Hour).padStart(2, '0')}:
                {String(s.order_cutoff.Minute).padStart(2, '0')}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

function ChannelsCard() {
  const { data: me } = useMe()
  const link = useLinkTelegram()

  return (
    <Card title="Уведомления">
      <div className="space-y-3 text-sm text-slate-700">
        <p>
          Telegram:{' '}
          {me?.user.telegram_linked ? (
            <span className="font-medium text-emerald-700">привязан</span>
          ) : (
            <span className="text-slate-500">не привязан</span>
          )}
        </p>

        {!me?.tenant.is_sandbox && (
          <div className="space-y-2">
            <Button
              variant="secondary"
              loading={link.isPending}
              onClick={() => link.mutate()}
            >
              {me?.user.telegram_linked ? 'Привязать другой чат' : 'Привязать Telegram'}
            </Button>

            {link.data && (
              <Alert kind="success" title="Ссылка готова">
                <a
                  href={link.data.url}
                  target="_blank"
                  rel="noreferrer"
                  className="font-medium underline"
                >
                  Открыть бота и подтвердить
                </a>
                <p className="mt-1 text-slate-600">
                  Ссылка одноразовая и действует 15 минут.
                </p>
              </Alert>
            )}
            {link.isError && <Alert kind="error">{link.error.message}</Alert>}
          </div>
        )}

        <p className="text-slate-500">
          Ежедневная сводка уходит в 09:00 по времени организации. Срочные
          алерты приходят сразу, но в тихие часы (22:00–08:00) откладываются
          до утра.
        </p>
      </div>
    </Card>
  )
}
