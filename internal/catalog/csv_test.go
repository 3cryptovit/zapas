package catalog_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"

	"github.com/vostapenko/zapas/internal/catalog"
)

func parse(t *testing.T, text string) catalog.ParseResult {
	t.Helper()
	got, err := catalog.ParseCSV(strings.NewReader(text))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	return got
}

func TestParseCSV_Минимальный(t *testing.T) {
	got := parse(t, strings.Join([]string{
		"Название,Единица",
		"Молоко 3.2%,л",
		"Зерно Бразилия,кг",
		"Стаканы 350 мл,шт",
	}, "\n"))

	if len(got.Errors) != 0 {
		t.Fatalf("ошибки на корректном файле: %v", got.Errors)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("строк %d, want 3", len(got.Rows))
	}
	if got.Rows[0].BaseUnit != catalog.UnitLiter {
		t.Errorf("единица = %s, want l", got.Rows[0].BaseUnit)
	}
	// Уровень сервиса по умолчанию — 95% (§5.2).
	if got.Rows[0].ServiceLevel != 95 {
		t.Errorf("уровень сервиса = %d, want 95", got.Rows[0].ServiceLevel)
	}
	if !got.OK() {
		t.Error("файл без ошибок должен считаться пригодным")
	}
}

// TestParseCSV_РусскийExcel: точка с запятой, BOM и запятая в числах.
func TestParseCSV_РусскийExcel(t *testing.T) {
	// BOM в начале файла — так сохраняет Excel.
	text := "\ufeff" + strings.Join([]string{
		"Название;Категория;Единица;Поставщик;Остаток;Кратность",
		"Молоко 3,2%;Молочка;л;Молочная ферма;24,5;12",
	}, "\r\n")

	got := parse(t, text)

	if len(got.Errors) != 0 {
		t.Fatalf("ошибки: %v", got.Errors)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("строк %d, want 1", len(got.Rows))
	}

	row := got.Rows[0]
	// Название содержит запятую — разделитель определился как «;».
	if row.Name != "Молоко 3,2%" {
		t.Errorf("название = %q", row.Name)
	}
	if row.Category != "Молочка" {
		t.Errorf("категория = %q", row.Category)
	}
	if row.Supplier != "Молочная ферма" {
		t.Errorf("поставщик = %q", row.Supplier)
	}
	if row.OpeningQty.String() != "24.500" {
		t.Errorf("остаток = %s, want 24.500", row.OpeningQty)
	}
	if row.PackMultiple.String() != "12.000" {
		t.Errorf("кратность = %s, want 12.000", row.PackMultiple)
	}
}

func TestParseCSV_ОшибкиПоСтрокам(t *testing.T) {
	got := parse(t, strings.Join([]string{
		"Название,Единица,Остаток,Уровень сервиса",
		"Молоко,л,24,95",
		",л,10,95",             // нет названия
		"Зерно,тонна,5,95",     // неизвестная единица
		"Сахар,кг,не число,95", // остаток не число
		"Сливки,л,-5,95",       // отрицательный остаток
		"Сироп,л,3,80",         // недопустимый уровень сервиса
		"Молоко,л,7,95",        // дубль названия
	}, "\n"))

	if len(got.Rows) != 1 {
		t.Errorf("корректных строк %d, want 1", len(got.Rows))
	}
	if len(got.Errors) != 6 {
		t.Fatalf("ошибок %d, want 6: %v", len(got.Errors), got.Errors)
	}

	// Номер строки должен указывать на строку в файле, включая заголовок.
	byLine := map[int]catalog.RowError{}
	for _, e := range got.Errors {
		byLine[e.Line] = e
	}
	for _, line := range []int{3, 4, 5, 6, 7, 8} {
		if _, ok := byLine[line]; !ok {
			t.Errorf("нет ошибки для строки %d", line)
		}
	}
	if !strings.Contains(byLine[8].Message, "строке 2") {
		t.Errorf("дубль должен указывать на первую строку: %q", byLine[8].Message)
	}
	if got.OK() {
		t.Error("файл с ошибками не должен считаться пригодным")
	}
}

func TestParseCSV_ПустыеСтрокиПропускаются(t *testing.T) {
	got := parse(t, strings.Join([]string{
		"Название,Единица",
		"Молоко,л",
		"",
		"   ,   ",
		"Зерно,кг",
	}, "\n"))

	if len(got.Errors) != 0 {
		t.Fatalf("ошибки: %v", got.Errors)
	}
	if len(got.Rows) != 2 {
		t.Errorf("строк %d, want 2", len(got.Rows))
	}
}

func TestParseCSV_НеполныеСтроки(t *testing.T) {
	// В Excel часто получаются строки короче заголовка.
	got := parse(t, strings.Join([]string{
		"Название,Единица,Категория,Остаток",
		"Молоко,л",
		"Зерно,кг,Кофе",
	}, "\n"))

	if len(got.Errors) != 0 {
		t.Fatalf("короткие строки не должны быть ошибкой: %v", got.Errors)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("строк %d, want 2", len(got.Rows))
	}
	if !got.Rows[0].OpeningQty.IsZero() {
		t.Errorf("пропущенный остаток = %s, want 0", got.Rows[0].OpeningQty)
	}
	if got.Rows[1].Category != "Кофе" {
		t.Errorf("категория = %q, want Кофе", got.Rows[1].Category)
	}
}

func TestParseCSV_ОбязательныеКолонки(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"нет названия", "Категория,Единица\nМолочка,л"},
		{"нет единицы", "Название,Категория\nМолоко,Молочка"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := catalog.ParseCSV(strings.NewReader(tc.text)); err == nil {
				t.Error("файл без обязательной колонки должен отклоняться целиком")
			}
		})
	}
}

func TestParseCSV_ПустойФайл(t *testing.T) {
	for _, text := range []string{"", "   ", "Название,Единица"} {
		if _, err := catalog.ParseCSV(strings.NewReader(text)); err == nil {
			t.Errorf("пустой файл %q должен давать ошибку", text)
		}
	}
}

func TestParseCSV_УсловияЗакупки(t *testing.T) {
	got := parse(t, strings.Join([]string{
		"Название,Единица,Поставщик,Единица закупки,Коэффициент,Кратность,Минимальная партия,Цена",
		"Молоко,л,Молочная ферма,кор.,12,12,24,780",
	}, "\n"))

	if len(got.Errors) != 0 {
		t.Fatalf("ошибки: %v", got.Errors)
	}
	row := got.Rows[0]
	if row.PurchaseUnit != "кор." {
		t.Errorf("единица закупки = %q", row.PurchaseUnit)
	}
	if row.UnitFactor.String() != "12.000" {
		t.Errorf("коэффициент = %s", row.UnitFactor)
	}
	if row.MinOrderQty.String() != "24.000" {
		t.Errorf("минимальная партия = %s", row.MinOrderQty)
	}
	if row.Price == nil || row.Price.String() != "780.000" {
		t.Errorf("цена = %v", row.Price)
	}
}

func TestParseCSV_НулевойКоэффициентОтклоняется(t *testing.T) {
	got := parse(t, strings.Join([]string{
		"Название,Единица,Коэффициент",
		"Молоко,л,0",
	}, "\n"))

	if len(got.Errors) != 1 {
		t.Fatalf("ошибок %d, want 1", len(got.Errors))
	}
	if got.Errors[0].Column != "коэффициент" {
		t.Errorf("ошибка в колонке %q", got.Errors[0].Column)
	}
}

// TestParseCSV_Кодировки — FR-4: Excel на русской Windows по умолчанию
// сохраняет CSV в CP1251, и такой файл обязан импортироваться.
//
// До исправления эти байты доезжали до PostgreSQL и возвращали 500.
func TestParseCSV_Кодировки(t *testing.T) {
	const utf8CSV = "name,category,unit,supplier,opening_qty\n" +
		"Молоко,Молочка,l,Ферма,24\n"

	tests := []struct {
		name string
		body []byte
		text bool
	}{
		{"UTF-8", []byte(utf8CSV), true},
		{"UTF-8 с BOM", append([]byte{0xEF, 0xBB, 0xBF}, utf8CSV...), true},
		{"CP1251", toCP1251(t, utf8CSV), true},
		{"двоичный файл", []byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x01}, false},
		{"UTF-16", []byte{0xFF, 0xFE, 0x41, 0x00}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := catalog.ParseCSV(bytes.NewReader(tc.body))
			if !tc.text {
				if !errors.Is(err, catalog.ErrNotText) {
					t.Fatalf("ждали ErrNotText, получили %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("разобрано строк %d, ждали 1", len(res.Rows))
			}
			// Кириллица должна дойти целой, а не превратиться в «Ìîëîêî».
			if res.Rows[0].Name != "Молоко" {
				t.Errorf("имя позиции %q, ждали %q", res.Rows[0].Name, "Молоко")
			}
			if res.Rows[0].Category != "Молочка" {
				t.Errorf("категория %q, ждали %q", res.Rows[0].Category, "Молочка")
			}
		})
	}
}

// toCP1251 переводит UTF-8 в CP1251 — так файл выглядит после Excel.
func toCP1251(t *testing.T, s string) []byte {
	t.Helper()
	out, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("перекодировать в CP1251: %v", err)
	}
	return out
}
