/**
 * Форматирование чисел и дат по требованиям §6.3: локаль ru-RU, количество
 * всегда с единицей, даты относительные и точные одновременно.
 *
 * Количества приходят из API строками ("12.500"), чтобы не терять точность
 * в JavaScript. Число из строки делается только здесь и только для показа.
 */

export type BaseUnit = 'kg' | 'l' | 'pcs'

const UNIT_LABEL: Record<BaseUnit, string> = {
  kg: 'кг',
  l: 'л',
  pcs: 'шт',
}

/** Единица измерения по-русски. */
export function unitLabel(unit: BaseUnit): string {
  return UNIT_LABEL[unit]
}

/**
 * Количество без единицы: «1,5», «24», «1 500».
 * Дробная часть показывается только когда она есть — «24 л», а не «24,000 л».
 */
export function formatQty(value: string | number): string {
  const n = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(n)) return '—'

  // Учёт ведётся с точностью до трёх знаков, но показывать три нули незачем.
  const rounded = Math.round(n * 1000) / 1000
  const fractionDigits = Number.isInteger(rounded) ? 0 : decimals(rounded)

  return new Intl.NumberFormat('ru-RU', {
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(rounded)
}

function decimals(n: number): number {
  const s = String(n)
  const dot = s.indexOf('.')
  return dot === -1 ? 0 : Math.min(s.length - dot - 1, 3)
}

/** Количество с единицей: «24 л», «1,5 кг». */
export function formatQtyWithUnit(value: string | number, unit: BaseUnit): string {
  return `${formatQty(value)} ${unitLabel(unit)}`
}

/** Количество в единице закупки: «2 кор. (24 л)». */
export function formatPurchaseQty(
  baseQty: string | number,
  unit: BaseUnit,
  purchaseUnit: string,
  unitFactor: string | number,
): string {
  const factor = typeof unitFactor === 'number' ? unitFactor : Number(unitFactor)
  const base = typeof baseQty === 'number' ? baseQty : Number(baseQty)

  if (!Number.isFinite(factor) || factor <= 0 || !purchaseUnit) {
    return formatQtyWithUnit(baseQty, unit)
  }
  const packs = base / factor
  return `${formatQty(packs)} ${purchaseUnit} (${formatQtyWithUnit(baseQty, unit)})`
}

const WEEKDAY_SHORT = ['вс', 'пн', 'вт', 'ср', 'чт', 'пт', 'сб']
const MONTH_SHORT = [
  'янв', 'фев', 'мар', 'апр', 'мая', 'июн',
  'июл', 'авг', 'сен', 'окт', 'ноя', 'дек',
]

/**
 * Дата одновременно точная и относительная: «чт, 24 сент (через 2 дня)».
 * Без относительной части владельцу приходится считать дни в уме.
 */
export function formatDay(day: string, today: string): string {
  const date = parseDay(day)
  const now = parseDay(today)
  if (!date || !now) return '—'

  const exact = `${WEEKDAY_SHORT[date.getUTCDay()]}, ${date.getUTCDate()} ${MONTH_SHORT[date.getUTCMonth()]}`
  const relative = relativeDay(daysBetween(now, date))

  return relative ? `${exact} (${relative})` : exact
}

/** Только относительная часть: «сегодня», «завтра», «через 3 дня». */
export function relativeDay(diff: number): string {
  if (diff === 0) return 'сегодня'
  if (diff === 1) return 'завтра'
  if (diff === 2) return 'послезавтра'
  if (diff === -1) return 'вчера'
  if (diff < 0) return `${plural(-diff, 'день', 'дня', 'дней')} назад`
  if (diff <= 14) return `через ${plural(diff, 'день', 'дня', 'дней')}`
  return ''
}

/** Русское склонение: 1 день, 2 дня, 5 дней. */
export function plural(n: number, one: string, few: string, many: string): string {
  const abs = Math.abs(n) % 100
  const last = abs % 10

  if (abs > 10 && abs < 20) return `${n} ${many}`
  if (last > 1 && last < 5) return `${n} ${few}`
  if (last === 1) return `${n} ${one}`
  return `${n} ${many}`
}

/** Время отсечки: «до 16:00». */
export function formatCutoff(iso: string, timeZone: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat('ru-RU', {
    hour: '2-digit',
    minute: '2-digit',
    timeZone,
  }).format(date)
}

function parseDay(day: string): Date | null {
  // Дни приходят как YYYY-MM-DD в поясе тенанта; разбираем как UTC, чтобы
  // локальная зона браузера не сдвинула дату на сутки.
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day)
  if (!match) return null
  const [, y, m, d] = match
  return new Date(Date.UTC(Number(y), Number(m) - 1, Number(d)))
}

function daysBetween(from: Date, to: Date): number {
  return Math.round((to.getTime() - from.getTime()) / 86_400_000)
}
