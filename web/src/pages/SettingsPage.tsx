import { useState, type ReactNode } from 'react'
import { ExternalLink, Package, Plus, Send, Truck, Upload } from 'lucide-react'

import {
  Alert,
  Badge,
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageHeader,
  Select,
  Skeleton,
  Table,
  Td,
  Textarea,
  Th,
} from '@/components/ui'
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
    <div>
      <PageHeader title="Настройки" description="Номенклатура, поставщики и каналы уведомлений." />

      {me?.tenant.is_sandbox && (
        <div className="mb-8">
          <Alert kind="info">
            В демо отключены внешние каналы, приглашение сотрудников и смена пароля — чтобы демо нельзя
            было использовать для рассылок.
          </Alert>
        </div>
      )}

      <div className="divide-y divide-slate-200">
        <Section title="Импорт из CSV" hint="Быстрый старт: вся номенклатура одним файлом из Excel.">
          <ImportCard />
        </Section>
        <Section title="Позиции" hint="Что лежит на складе и в каких единицах считается.">
          <ItemsCard />
        </Section>
        <Section title="Поставщики" hint="Срок и дни доставки нужны, чтобы система знала, когда напоминать о заказе.">
          <SuppliersCard />
        </Section>
        <Section title="Уведомления" hint="Сводка и срочные алерты в Telegram.">
          <ChannelsCard />
        </Section>
      </div>
    </div>
  )
}

/** Раздел настроек: слева заголовок и пояснение, справа содержимое. */
function Section({ title, hint, children }: { title: string; hint: string; children: ReactNode }) {
  return (
    <section className="grid gap-4 py-8 first:pt-0 lg:grid-cols-3 lg:gap-10">
      <div>
        <h2 className="text-[15px] font-semibold text-slate-900">{title}</h2>
        <p className="mt-1 text-sm leading-relaxed text-slate-500">{hint}</p>
      </div>
      <div className="min-w-0 lg:col-span-2">{children}</div>
    </section>
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
  const [fileName, setFileName] = useState('')
  const importCSV = useImportCSV()
  const result = importCSV.data

  const readFile = async (file: File) => {
    setFileName(file.name)
    setCsv(await file.text())
    importCSV.reset()
  }

  return (
    <Card>
      <div className="space-y-5">
        <p className="text-sm leading-relaxed text-slate-600">
          Обязательны колонки «Название» и «Единица», остальные — по желанию. Разделитель определяется сам:
          подойдёт и CSV из русского Excel.
        </p>

        <label className="flex cursor-pointer flex-col items-center gap-2 rounded-lg border border-dashed border-slate-300 px-4 py-6 text-center transition-colors hover:border-brand-300 hover:bg-brand-50/40">
          <Upload aria-hidden="true" className="size-5 text-slate-400" strokeWidth={1.75} />
          <span className="text-sm font-medium text-slate-700">{fileName || 'Выберите файл CSV'}</span>
          <span className="text-[13px] text-slate-500">или вставьте содержимое ниже</span>
          <input
            type="file"
            accept=".csv,text/csv"
            className="sr-only"
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) void readFile(file)
            }}
          />
        </label>

        <Field label="Содержимое файла">
          <Textarea
            rows={5}
            value={csv}
            onChange={(e) => {
              setCsv(e.target.value)
              setFileName('')
              importCSV.reset()
            }}
            placeholder={csvTemplate}
            className="font-mono sm:text-[13px]"
          />
        </Field>

        {importCSV.isError && (
          <Alert kind="error">
            {importCSV.error instanceof ApiError
              ? importCSV.error.detail || importCSV.error.message
              : importCSV.error.message}
          </Alert>
        )}

        {result &&
          (result.errors?.length ? (
            <Alert kind="error" title={`Ошибок в файле: ${result.errors.length}`}>
              <ul className="mt-1 space-y-0.5">
                {result.errors.slice(0, 20).map((e, i) => (
                  <li key={i}>
                    Строка {e.line}
                    {e.column ? `, «${e.column}»` : ''}: {e.message}
                  </li>
                ))}
              </ul>
              <p className="mt-2">Импорт не выполнен: либо всё, либо ничего. Поправьте файл и попробуйте снова.</p>
            </Alert>
          ) : result.dry_run ? (
            <Alert kind="success" title="Файл разобран">
              Позиций: {result.items}, с начальным остатком: {result.with_opening}. Нажмите «Импортировать»,
              чтобы записать.
            </Alert>
          ) : (
            <Alert kind="success" title="Импорт завершён">
              Позиций: {result.items}, категорий заведено: {result.categories}, поставщиков: {result.suppliers}.
            </Alert>
          ))}

        <div className="flex flex-wrap gap-2">
          <Button
            variant="secondary"
            disabled={!csv.trim()}
            loading={importCSV.isPending && importCSV.variables?.dryRun === true}
            onClick={() => importCSV.mutate({ csv, dryRun: true })}
          >
            Предпросмотр
          </Button>
          <Button
            disabled={!csv.trim() || !result || !result.dry_run || Boolean(result.errors?.length)}
            loading={importCSV.isPending && importCSV.variables?.dryRun === false}
            onClick={() => importCSV.mutate({ csv, dryRun: false })}
          >
            Импортировать
          </Button>
        </div>
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
    <Card flush>
      <form onSubmit={submit} className="grid gap-4 px-5 pt-5 pb-6 sm:grid-cols-2 sm:px-6">
        <div className="sm:col-span-2">
          <Field label="Название">
            <Input required value={name} onChange={(e) => setName(e.target.value)} placeholder="Молоко 3,2%" />
          </Field>
        </div>
        <Field label="Единица учёта">
          <Select value={unit} onChange={(e) => setUnit(e.target.value)}>
            <option value="l">литры</option>
            <option value="kg">килограммы</option>
            <option value="pcs">штуки</option>
          </Select>
        </Field>
        <Field label="Минимальный остаток" hint="Пока нет прогноза">
          <Input
            inputMode="decimal"
            value={minQty}
            onChange={(e) => setMinQty(e.target.value.replace(',', '.'))}
          />
        </Field>
        <Field label="Категория">
          <Select value={categoryId} onChange={(e) => setCategoryId(e.target.value)}>
            <option value="">Без категории</option>
            {categories?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Поставщик">
          <Select value={supplierId} onChange={(e) => setSupplierId(e.target.value)}>
            <option value="">Без поставщика</option>
            {suppliers?.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        {create.isError && (
          <div className="sm:col-span-2">
            <Alert kind="error">{create.error.message}</Alert>
          </div>
        )}
        <div className="sm:col-span-2">
          <Button type="submit" icon={Plus} loading={create.isPending}>
            Добавить позицию
          </Button>
        </div>
      </form>

      <div className="border-t border-slate-100">
        {isLoading ? (
          <div className="p-6">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : !items?.length ? (
          <EmptyState icon={Package} title="Позиций пока нет" hint="Добавьте первую или импортируйте CSV." />
        ) : (
          <div className="pt-5">
            <Table>
              <thead>
                <tr>
                  <Th>Позиция</Th>
                  <Th>Поставщик</Th>
                  <Th align="right">Мин. остаток</Th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id} className="[&:last-child>td]:border-0">
                    <Td>
                      <span className="font-medium text-slate-900">{item.name}</span>
                      <span className="block text-[13px] text-slate-500">
                        {[item.category_name, item.unit_label].filter(Boolean).join(' · ')}
                      </span>
                    </Td>
                    <Td className="text-slate-600">{item.supplier_name ?? <span className="text-slate-300">—</span>}</Td>
                    <Td align="right" className="text-slate-600">
                      {Number(item.manual_min_qty) > 0 ? (
                        formatQtyWithUnit(item.manual_min_qty, item.base_unit)
                      ) : (
                        <span className="text-slate-300">—</span>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </div>
        )}
      </div>
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
    <Card flush>
      <form
        className="grid gap-4 px-5 pt-5 pb-6 sm:grid-cols-2 sm:px-6"
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
        <Field label="Название">
          <Input required value={name} onChange={(e) => setName(e.target.value)} placeholder="Молочная ферма" />
        </Field>
        <Field label="Контакт" hint="Телеграм, почта или телефон">
          <Input value={contact} onChange={(e) => setContact(e.target.value)} />
        </Field>
        <Field label="Срок поставки, дней">
          <Input type="number" min="0" max="60" value={lead} onChange={(e) => setLead(e.target.value)} />
        </Field>
        <Field label="Принимает заказы до">
          <Input type="time" value={cutoff} onChange={(e) => setCutoff(e.target.value)} />
        </Field>

        <fieldset className="sm:col-span-2">
          <legend className="mb-1.5 text-[13px] font-medium text-slate-700">Дни доставки</legend>
          <div className="flex flex-wrap gap-1.5">
            {weekdayNames.map((label, index) => {
              const day = index + 1
              const on = days.includes(day)
              return (
                <button
                  key={day}
                  type="button"
                  onClick={() => toggle(day)}
                  aria-pressed={on}
                  className={`min-h-11 min-w-11 rounded-lg text-sm font-medium transition-colors sm:min-h-10 sm:min-w-10 ${
                    on
                      ? 'bg-brand-50 text-brand-700 ring-1 ring-inset ring-brand-200'
                      : 'bg-white text-slate-600 ring-1 ring-inset ring-slate-300 hover:bg-slate-50'
                  }`}
                >
                  {label}
                </button>
              )
            })}
          </div>
        </fieldset>

        {create.isError && (
          <div className="sm:col-span-2">
            <Alert kind="error">{create.error.message}</Alert>
          </div>
        )}
        <div className="sm:col-span-2">
          <Button type="submit" icon={Plus} loading={create.isPending} disabled={days.length === 0}>
            Добавить поставщика
          </Button>
        </div>
      </form>

      <div className="border-t border-slate-100">
        {isLoading ? (
          <div className="p-6">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : !suppliers?.length ? (
          <EmptyState icon={Truck} title="Поставщиков пока нет" hint="Добавьте первого — форма выше." />
        ) : (
          <ul className="divide-y divide-slate-100">
            {suppliers.map((s) => (
              <li key={s.id} className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-5 py-4 sm:px-6">
                <span className="min-w-0">
                  <span className="block font-medium text-slate-900">{s.name}</span>
                  <span className="block text-[13px] text-slate-500">
                    срок {s.lead_time_days} дн. · заказ до {String(s.order_cutoff.Hour).padStart(2, '0')}:
                    {String(s.order_cutoff.Minute).padStart(2, '0')}
                    {s.contact ? ` · ${s.contact}` : ''}
                  </span>
                </span>
                <span className="flex gap-1" aria-label={`Дни доставки: ${s.delivery_weekdays.map((d) => weekdayNames[d - 1]).join(', ')}`}>
                  {weekdayNames.map((label, index) => (
                    <span
                      key={label}
                      aria-hidden="true"
                      className={`flex size-7 items-center justify-center rounded-md text-[11px] font-medium ${
                        s.delivery_weekdays.includes(index + 1) ? 'bg-brand-50 text-brand-700' : 'text-slate-300'
                      }`}
                    >
                      {label}
                    </span>
                  ))}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Card>
  )
}

function ChannelsCard() {
  const { data: me } = useMe()
  const link = useLinkTelegram()
  const linked = Boolean(me?.user.telegram_linked)

  return (
    <Card>
      <div className="space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <span className="flex size-9 items-center justify-center rounded-full bg-slate-100 text-slate-500">
              <Send aria-hidden="true" className="size-4" strokeWidth={1.75} />
            </span>
            <div>
              <p className="font-medium text-slate-900">Telegram</p>
              <p className="text-[13px] text-slate-500">Сводка в 09:00 и срочные алерты</p>
            </div>
          </div>
          {linked ? (
            <Badge className="bg-emerald-50 text-emerald-700">Привязан</Badge>
          ) : (
            <Badge>Не привязан</Badge>
          )}
        </div>

        {!me?.tenant.is_sandbox && (
          <div className="space-y-3">
            <Button variant="secondary" loading={link.isPending} onClick={() => link.mutate()}>
              {linked ? 'Привязать другой чат' : 'Привязать Telegram'}
            </Button>

            {link.data && (
              <Alert kind="success" title="Ссылка готова">
                <a
                  href={link.data.url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 font-medium underline underline-offset-2"
                >
                  Открыть бота и подтвердить
                  <ExternalLink aria-hidden="true" className="size-3.5" strokeWidth={2} />
                </a>
                <p className="mt-1">Ссылка одноразовая и действует 15 минут.</p>
              </Alert>
            )}
            {link.isError && <Alert kind="error">{link.error.message}</Alert>}
          </div>
        )}

        <p className="border-t border-slate-100 pt-4 text-[13px] leading-relaxed text-slate-500">
          Ежедневная сводка уходит в 09:00 по времени организации. Срочные алерты приходят сразу, но в тихие
          часы (22:00–08:00) откладываются до утра.
        </p>
      </div>
    </Card>
  )
}
