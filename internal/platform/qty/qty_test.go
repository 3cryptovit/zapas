package qty_test

import (
	"encoding/json"
	"testing"

	"github.com/vostapenko/zapas/internal/platform/qty"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "12.5", want: "12.500"},
		{in: "1,5", want: "1.500"}, // запятая с телефона
		{in: "24", want: "24.000"},
		{in: "-0.8", want: "-0.800"},
		{in: "0.001", want: "0.001"},
		{in: "0.0001", wantErr: true}, // точнее numeric(14,3) не храним
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range tests {
		got, err := qty.Parse(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %s, ожидалась ошибка", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("Parse(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestArithmetic_БезПотериТочности(t *testing.T) {
	// Классическая ловушка float: 0.1 + 0.2 != 0.3.
	got := qty.MustParse("0.1").Add(qty.MustParse("0.2"))
	if got.String() != "0.300" {
		t.Fatalf("0.1 + 0.2 = %s, want 0.300", got)
	}
	if !got.Equal(qty.MustParse("0.3")) {
		t.Fatalf("сравнение с 0.3 провалилось")
	}
}

func TestCeilTo_КратностьУпаковки(t *testing.T) {
	tests := []struct {
		name string
		need string
		pack string
		want string
	}{
		{"ровно коробка", "12", "12", "12.000"},
		{"чуть больше коробки — берём две", "12.001", "12", "24.000"},
		{"меньше коробки — всё равно коробка", "3", "12", "12.000"},
		{"дробная кратность", "1.1", "0.5", "1.500"},
		{"кратность не задана", "7.3", "0", "7.300"},
		{"ноль к заказу", "0", "12", "0.000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := qty.MustParse(tc.need).CeilTo(qty.MustParse(tc.pack))
			if got.String() != tc.want {
				t.Errorf("CeilTo(%s, %s) = %s, want %s", tc.need, tc.pack, got, tc.want)
			}
		})
	}
}

func TestClampZero(t *testing.T) {
	if got := qty.MustParse("-3.5").ClampZero(); got.String() != "0.000" {
		t.Errorf("ClampZero(-3.5) = %s, want 0.000", got)
	}
	if got := qty.MustParse("3.5").ClampZero(); got.String() != "3.500" {
		t.Errorf("ClampZero(3.5) = %s, want 3.500", got)
	}
}

func TestJSON_КоличестваСтроками(t *testing.T) {
	type payload struct {
		Qty qty.Qty `json:"qty"`
	}
	out, err := json.Marshal(payload{Qty: qty.MustParse("16.5")})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"qty":"16.500"}` {
		t.Fatalf("Marshal = %s, want {\"qty\":\"16.500\"}", out)
	}

	var in payload
	if err := json.Unmarshal([]byte(`{"qty":"1.5"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Qty.String() != "1.500" {
		t.Fatalf("Unmarshal = %s, want 1.500", in.Qty)
	}
	// Число тоже принимаем — послабление для внешних клиентов.
	if err := json.Unmarshal([]byte(`{"qty":2.25}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.Qty.String() != "2.250" {
		t.Fatalf("Unmarshal числа = %s, want 2.250", in.Qty)
	}
}
