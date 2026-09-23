package qty_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/vostapenko/zapas/internal/platform/qty"
)

func TestАрифметикаИПредикаты(t *testing.T) {
	a := qty.MustParse("12.5")
	b := qty.MustParse("2.5")

	if got := a.Sub(b).String(); got != "10.000" {
		t.Errorf("Sub = %s, want 10.000", got)
	}
	if got := b.Neg().String(); got != "-2.500" {
		t.Errorf("Neg = %s, want -2.500", got)
	}
	if got := b.Neg().Abs().String(); got != "2.500" {
		t.Errorf("Abs = %s, want 2.500", got)
	}
	if got := b.MulInt(4).String(); got != "10.000" {
		t.Errorf("MulInt = %s, want 10.000", got)
	}
	if got := a.MulFloat(0.5).String(); got != "6.250" {
		t.Errorf("MulFloat = %s, want 6.250", got)
	}
	if !a.GreaterThan(b) || a.LessThan(b) {
		t.Error("сравнение работает неверно")
	}
	if !qty.Zero().IsZero() || a.IsZero() {
		t.Error("IsZero работает неверно")
	}
	if !a.IsPositive() || a.IsNegative() {
		t.Error("знак определяется неверно")
	}
	if got := a.Float64(); got != 12.5 {
		t.Errorf("Float64 = %v, want 12.5", got)
	}
}

func TestSumИMax(t *testing.T) {
	got := qty.Sum(qty.MustParse("1.1"), qty.MustParse("2.2"), qty.MustParse("3.3"))
	if got.String() != "6.600" {
		t.Errorf("Sum = %s, want 6.600", got)
	}
	if qty.Sum().String() != "0.000" {
		t.Errorf("Sum без аргументов должен давать ноль")
	}

	if got := qty.Max(qty.MustParse("3"), qty.MustParse("7")).String(); got != "7.000" {
		t.Errorf("Max = %s, want 7.000", got)
	}
	if got := qty.Max(qty.MustParse("7"), qty.MustParse("3")).String(); got != "7.000" {
		t.Errorf("Max = %s, want 7.000", got)
	}
}

func TestКонструкторы(t *testing.T) {
	if got := qty.FromInt(24).String(); got != "24.000" {
		t.Errorf("FromInt = %s, want 24.000", got)
	}
	// Значение точнее трёх знаков округляется до складской точности.
	d := decimal.RequireFromString("1.23456")
	if got := qty.FromDecimal(d).String(); got != "1.235" {
		t.Errorf("FromDecimal = %s, want 1.235", got)
	}
	if got := qty.MustParse("2.5").Decimal().String(); got != "2.5" {
		t.Errorf("Decimal = %s, want 2.5", got)
	}
	if got := qty.MustParse("0.5").Rat().RatString(); got != "1/2" {
		t.Errorf("Rat = %s, want 1/2", got)
	}
}

func TestParse_ПробелыРазрядов(t *testing.T) {
	// Владелец копирует «1 500» из накладной.
	got, err := qty.Parse("1 500,5")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "1500.500" {
		t.Errorf("Parse = %s, want 1500.500", got)
	}
}

func TestMustParse_ПаникаНаМусоре(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParse должен паниковать на мусоре")
		}
	}()
	qty.MustParse("не число")
}

// nbsp — неразрывный пробел: им Human разделяет разряды, как принято в ru-RU.
const nbsp = "\u00a0"

func TestHuman_ФорматДляЧеловека(t *testing.T) {
	// Заявку поставщику и сообщение в Telegram читают глазами:
	// «24 л», а не «24.000 л».
	tests := []struct {
		in   string
		want string
	}{
		{"24.000", "24"},
		{"1.500", "1,5"},
		{"0.001", "0,001"},
		{"2.000", "2"},
		{"0", "0"},
		{"-3.250", "-3,25"},
		{"1500", "1" + nbsp + "500"},
		{"1500.5", "1" + nbsp + "500,5"},
		{"1234567", "1" + nbsp + "234" + nbsp + "567"},
		{"-1500", "-1" + nbsp + "500"},
	}
	for _, tc := range tests {
		if got := qty.MustParse(tc.in).Human(); got != tc.want {
			t.Errorf("Human(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHuman_НеЛоматСтрогийФормат(t *testing.T) {
	// String остаётся каноническим: по нему сравнивают и его шлют в JSON.
	q := qty.MustParse("24")
	if q.String() != "24.000" {
		t.Errorf("String = %s, want 24.000", q)
	}
	if q.Human() != "24" {
		t.Errorf("Human = %s, want 24", q.Human())
	}
}
