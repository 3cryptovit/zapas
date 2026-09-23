// Package qty — количества в базовой единице позиции.
//
// float для количеств запрещён (CONVENTIONS.md): 0,1 + 0,2 не должно давать
// 0,30000000000000004 в остатке, который потом сверяется с журналом.
// В БД это numeric(14,3), в JSON — строка "12.500" (§11).
package qty

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/shopspring/decimal"
)

// Scale — число знаков после запятой, как в numeric(14,3).
const Scale int32 = 3

// Qty — количество. Нулевое значение — ноль, пользоваться им безопасно.
type Qty struct {
	d decimal.Decimal
}

// Zero — нулевое количество.
func Zero() Qty { return Qty{} }

// FromDecimal оборачивает decimal, округляя до складской точности.
func FromDecimal(d decimal.Decimal) Qty { return Qty{d: d.Round(Scale)} }

// FromInt строит количество из целого числа.
func FromInt(n int64) Qty { return Qty{d: decimal.NewFromInt(n)} }

// MustParse разбирает строку и паникует на мусоре. Только для тестов и констант.
func MustParse(s string) Qty {
	q, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return q
}

// Parse разбирает количество из строки. Запятая как разделитель тоже принимается:
// пользователь вводит «1,5» с телефона.
func Parse(s string) (Qty, error) {
	normalized := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case ',':
			normalized = append(normalized, '.')
		case ' ', ' ':
			// пробелы-разделители разрядов игнорируем
		default:
			normalized = append(normalized, r)
		}
	}
	d, err := decimal.NewFromString(string(normalized))
	if err != nil {
		return Qty{}, fmt.Errorf("qty: не число: %q", s)
	}
	if d.Exponent() < -Scale {
		return Qty{}, fmt.Errorf("qty: больше %d знаков после запятой: %q", Scale, s)
	}
	return Qty{d: d}, nil
}

// Decimal отдаёт нижележащее значение.
func (q Qty) Decimal() decimal.Decimal { return q.d }

// String — каноническая запись с фиксированной точностью: "12.500".
// В таком виде количества уходят в JSON и сравниваются в тестах.
func (q Qty) String() string { return q.d.StringFixed(Scale) }

// nbsp — неразрывный пробел: разделитель разрядов в ru-RU.
const nbsp = '\u00a0'

// Human — запись для человека: "24", "1,5", "1 500".
//
// Нужна там, где текст читают глазами, а не парсят: заявка поставщику,
// сообщение в Telegram, письмо. Незначащие нули убираются, разделитель
// дробной части — запятая, как принято в ru-RU.
func (q Qty) Human() string {
	trimmed := q.d.Round(Scale).String()

	if i := strings.IndexByte(trimmed, '.'); i >= 0 {
		trimmed = strings.TrimRight(trimmed, "0")
		trimmed = strings.TrimSuffix(trimmed, ".")
		trimmed = strings.Replace(trimmed, ".", ",", 1)
	}

	// Разряды целой части разделяются неразрывным пробелом.
	intPart, frac, hasFrac := strings.Cut(trimmed, ",")
	sign := ""
	if strings.HasPrefix(intPart, "-") {
		sign, intPart = "-", intPart[1:]
	}
	var b strings.Builder
	b.WriteString(sign)
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteRune(nbsp)
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteByte(',')
		b.WriteString(frac)
	}
	return b.String()
}

// Add, Sub, Neg — арифметика без потери точности.
func (q Qty) Add(other Qty) Qty { return Qty{d: q.d.Add(other.d)} }
func (q Qty) Sub(other Qty) Qty { return Qty{d: q.d.Sub(other.d)} }
func (q Qty) Neg() Qty          { return Qty{d: q.d.Neg()} }

// MulInt умножает на целое (коэффициент упаковки, число дней).
func (q Qty) MulInt(n int64) Qty { return Qty{d: q.d.Mul(decimal.NewFromInt(n))} }

// MulFloat умножает на коэффициент из прогноза и округляет до складской точности.
// Прогноз — оценка, поэтому здесь float допустим; результат сразу фиксируется.
func (q Qty) MulFloat(f float64) Qty {
	return Qty{d: q.d.Mul(decimal.NewFromFloat(f)).Round(Scale)}
}

// Float64 — приближение для формул прогноза; в учёт не возвращается.
func (q Qty) Float64() float64 {
	f, _ := q.d.Float64()
	return f
}

func (q Qty) IsZero() bool           { return q.d.IsZero() }
func (q Qty) IsNegative() bool       { return q.d.IsNegative() }
func (q Qty) IsPositive() bool       { return q.d.IsPositive() }
func (q Qty) Equal(other Qty) bool   { return q.d.Equal(other.d) }
func (q Qty) LessThan(o Qty) bool    { return q.d.LessThan(o.d) }
func (q Qty) GreaterThan(o Qty) bool { return q.d.GreaterThan(o.d) }

// Abs — модуль количества.
func (q Qty) Abs() Qty { return Qty{d: q.d.Abs()} }

// Max возвращает большее из двух количеств.
func Max(a, b Qty) Qty {
	if a.LessThan(b) {
		return b
	}
	return a
}

// ClampZero обрезает отрицательные значения до нуля: max(0, x) из формулы
// целевого уровня запаса (§5.2).
func (q Qty) ClampZero() Qty {
	if q.IsNegative() {
		return Zero()
	}
	return q
}

// CeilTo округляет вверх до кратности step — упаковка поставщика (§5.2).
// Нулевой или отрицательный шаг означает «кратность не задана».
func (q Qty) CeilTo(step Qty) Qty {
	if !step.IsPositive() {
		return q
	}
	ratio := q.d.Div(step.d)
	return Qty{d: ratio.Ceil().Mul(step.d).Round(Scale)}
}

// Sum складывает список количеств.
func Sum(qs ...Qty) Qty {
	total := Zero()
	for _, q := range qs {
		total = total.Add(q)
	}
	return total
}

// MarshalJSON пишет количество строкой, чтобы JavaScript не терял точность.
func (q Qty) MarshalJSON() ([]byte, error) { return json.Marshal(q.String()) }

// UnmarshalJSON принимает и строку, и число: число — послабление для внешних
// клиентов, собственный фронтенд всегда шлёт строку.
func (q *Qty) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := Parse(s)
		if err != nil {
			return err
		}
		*q = parsed
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("qty: ожидается число или строка")
	}
	parsed, err := Parse(n.String())
	if err != nil {
		return err
	}
	*q = parsed
	return nil
}

// Rat отдаёт точное рациональное значение — нужно драйверу БД.
func (q Qty) Rat() *big.Rat { return q.d.Rat() }
