package catalog

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/vostapenko/zapas/internal/platform/qty"
)

// Ограничения импорта (FR-4).
const (
	// MaxCSVSize — файл до 5 МБ.
	MaxCSVSize = 5 << 20
	// MaxCSVRows — потолок строк: дальше это уже не «завести номенклатуру».
	MaxCSVRows = 2000
)

// ErrNoRows — в файле нет ни одной строки данных.
var ErrNoRows = errors.New("catalog: в файле нет данных")

// ErrNotText — файл не текстовый: двоичные данные или кодировка,
// которую разобрать не получилось.
var ErrNotText = errors.New("catalog: файл не похож на текстовый")

// ImportRow — разобранная строка файла.
type ImportRow struct {
	// Line — номер строки в файле, включая заголовок: по нему владелец
	// ищет ошибку в своём Excel.
	Line int `json:"line"`

	Name         string   `json:"name"`
	Category     string   `json:"category,omitempty"`
	BaseUnit     BaseUnit `json:"base_unit"`
	Supplier     string   `json:"supplier,omitempty"`
	ServiceLevel int      `json:"service_level"`
	ManualMinQty qty.Qty  `json:"manual_min_qty"`

	// Условия закупки; заполняются, только если указан поставщик.
	PurchaseUnit string   `json:"purchase_unit,omitempty"`
	UnitFactor   qty.Qty  `json:"unit_factor"`
	PackMultiple qty.Qty  `json:"pack_multiple"`
	MinOrderQty  qty.Qty  `json:"min_order_qty"`
	Price        *qty.Qty `json:"price,omitempty"`

	// OpeningQty — начальный остаток. Пишется движением типа opening
	// и в расход для прогноза не идёт (§3.3).
	OpeningQty qty.Qty `json:"opening_qty"`
}

// RowError — ошибка в конкретной строке файла.
type RowError struct {
	Line    int    `json:"line"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

func (e RowError) Error() string {
	if e.Column != "" {
		return fmt.Sprintf("строка %d, колонка %q: %s", e.Line, e.Column, e.Message)
	}
	return fmt.Sprintf("строка %d: %s", e.Line, e.Message)
}

// ParseResult — результат разбора файла (FR-4: сначала предпросмотр).
type ParseResult struct {
	Rows   []ImportRow `json:"rows"`
	Errors []RowError  `json:"errors"`
}

// OK сообщает, что файл можно импортировать.
func (r ParseResult) OK() bool { return len(r.Errors) == 0 && len(r.Rows) > 0 }

// Колонки файла. Заголовок ищется по этим именам без учёта регистра
// и лишних пробелов.
var columnAliases = map[string][]string{
	"name":          {"название", "позиция", "name"},
	"category":      {"категория", "category"},
	"base_unit":     {"единица", "ед", "base_unit", "unit"},
	"supplier":      {"поставщик", "supplier"},
	"opening_qty":   {"остаток", "начальный остаток", "opening", "opening_qty"},
	"service_level": {"уровень сервиса", "service_level"},
	"manual_min":    {"минимум", "мин. остаток", "manual_min", "min"},
	"purchase_unit": {"единица закупки", "purchase_unit"},
	"unit_factor":   {"коэффициент", "unit_factor", "factor"},
	"pack_multiple": {"кратность", "упаковка", "pack", "pack_multiple"},
	"min_order":     {"минимальная партия", "min_order", "min_order_qty"},
	"price":         {"цена", "price"},
}

// unitAliases — как единицы пишут в жизни.
var unitAliases = map[string]BaseUnit{
	"кг": UnitKg, "kg": UnitKg, "килограмм": UnitKg,
	"л": UnitLiter, "l": UnitLiter, "литр": UnitLiter,
	"шт": UnitPcs, "pcs": UnitPcs, "штук": UnitPcs, "штука": UnitPcs,
}

// ParseCSV разбирает файл номенклатуры.
//
// Разбор отделён от записи намеренно (FR-4): сначала владелец видит
// предпросмотр с ошибками по строкам, и только потом импорт идёт одной
// транзакцией — либо всё, либо ничего.
// decodeText приводит содержимое файла к UTF-8.
//
// Excel на русской Windows по умолчанию сохраняет CSV в CP1251, и это не
// косметика: такие байты PostgreSQL отвергает, и импорт падал ошибкой
// сервера вместо внятного сообщения. Поэтому UTF-8 пробуется первым,
// а при неудаче файл читается как CP1251.
func decodeText(raw []byte) (string, error) {
	// UTF-16 Excel тоже умеет сохранять, но разбирать его мы не беремся:
	// лучше честно сказать, чем молча импортировать мусор.
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		return "", ErrNotText
	}
	// Нулевой байт в текстовом файле не встречается — значит это не CSV.
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", ErrNotText
	}

	// Excel сохраняет CSV с BOM — иначе первая колонка не найдётся.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	if utf8.Valid(raw) {
		return string(raw), nil
	}

	decoded, err := charmap.Windows1251.NewDecoder().Bytes(raw)
	if err != nil || !utf8.Valid(decoded) {
		return "", ErrNotText
	}
	return string(decoded), nil
}

func ParseCSV(r io.Reader) (ParseResult, error) {
	limited := io.LimitReader(r, MaxCSVSize+1)

	raw, err := io.ReadAll(limited)
	if err != nil {
		return ParseResult{}, fmt.Errorf("catalog: чтение файла: %w", err)
	}
	if len(raw) > MaxCSVSize {
		return ParseResult{}, fmt.Errorf("catalog: файл больше %d МБ", MaxCSVSize>>20)
	}

	text, err := decodeText(raw)
	if err != nil {
		return ParseResult{}, err
	}
	if strings.TrimSpace(text) == "" {
		return ParseResult{}, ErrNoRows
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = detectDelimiter(text)
	// Строки бывают разной длины: недостающие колонки — это ошибка строки,
	// а не всего файла.
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return ParseResult{}, fmt.Errorf("catalog: разбор CSV: %w", err)
	}
	if len(records) < 2 {
		return ParseResult{}, ErrNoRows
	}

	columns, err := mapColumns(records[0])
	if err != nil {
		return ParseResult{}, err
	}

	result := ParseResult{}
	seen := map[string]int{}

	for i, record := range records[1:] {
		line := i + 2 // +1 за заголовок, +1 за нумерацию с единицы

		if isBlank(record) {
			continue
		}
		if len(result.Rows) >= MaxCSVRows {
			result.Errors = append(result.Errors, RowError{
				Line:    line,
				Message: fmt.Sprintf("в файле больше %d строк", MaxCSVRows),
			})
			break
		}

		row, errs := parseRow(line, record, columns)
		if len(errs) > 0 {
			result.Errors = append(result.Errors, errs...)
			continue
		}

		// Дубли внутри файла ловим здесь: иначе импорт упадёт на уникальном
		// индексе, и владелец не поймёт, какая строка виновата.
		key := strings.ToLower(row.Name)
		if first, dup := seen[key]; dup {
			result.Errors = append(result.Errors, RowError{
				Line:    line,
				Column:  "название",
				Message: fmt.Sprintf("позиция %q уже есть в строке %d", row.Name, first),
			})
			continue
		}
		seen[key] = line

		result.Rows = append(result.Rows, row)
	}

	if len(result.Rows) == 0 && len(result.Errors) == 0 {
		return ParseResult{}, ErrNoRows
	}
	return result, nil
}

// detectDelimiter различает запятую и точку с запятой: русский Excel
// сохраняет CSV через точку с запятой.
func detectDelimiter(text string) rune {
	head := text
	if i := strings.IndexByte(head, '\n'); i != -1 {
		head = head[:i]
	}
	if strings.Count(head, ";") > strings.Count(head, ",") {
		return ';'
	}
	return ','
}

// mapColumns сопоставляет заголовок файла с известными колонками.
func mapColumns(header []string) (map[string]int, error) {
	out := map[string]int{}

	for i, cell := range header {
		name := strings.ToLower(strings.TrimSpace(cell))
		for column, aliases := range columnAliases {
			for _, alias := range aliases {
				if name == alias {
					out[column] = i
				}
			}
		}
	}

	if _, ok := out["name"]; !ok {
		return nil, fmt.Errorf("catalog: в заголовке нет колонки «Название»")
	}
	if _, ok := out["base_unit"]; !ok {
		return nil, fmt.Errorf("catalog: в заголовке нет колонки «Единица»")
	}
	return out, nil
}

func parseRow(line int, record []string, columns map[string]int) (ImportRow, []RowError) {
	var errs []RowError

	cell := func(column string) string {
		i, ok := columns[column]
		if !ok || i >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[i])
	}

	row := ImportRow{
		Line:         line,
		Name:         cell("name"),
		Category:     cell("category"),
		Supplier:     cell("supplier"),
		PurchaseUnit: cell("purchase_unit"),
		ServiceLevel: 95,
		UnitFactor:   qty.FromInt(1),
	}

	if row.Name == "" {
		errs = append(errs, RowError{Line: line, Column: "название", Message: "не заполнено"})
	}

	unit, ok := unitAliases[strings.ToLower(cell("base_unit"))]
	if !ok {
		errs = append(errs, RowError{
			Line: line, Column: "единица",
			Message: "допустимы кг, л и шт",
		})
	}
	row.BaseUnit = unit

	// Числовые колонки: пустая ячейка — это ноль, а не ошибка.
	numbers := []struct {
		column string
		label  string
		target *qty.Qty
	}{
		{"opening_qty", "остаток", &row.OpeningQty},
		{"manual_min", "минимум", &row.ManualMinQty},
		{"pack_multiple", "кратность", &row.PackMultiple},
		{"min_order", "минимальная партия", &row.MinOrderQty},
	}
	for _, n := range numbers {
		raw := cell(n.column)
		if raw == "" {
			continue
		}
		value, err := qty.Parse(raw)
		if err != nil {
			errs = append(errs, RowError{Line: line, Column: n.label, Message: "не число"})
			continue
		}
		if value.IsNegative() {
			errs = append(errs, RowError{Line: line, Column: n.label, Message: "не может быть отрицательным"})
			continue
		}
		*n.target = value
	}

	if raw := cell("unit_factor"); raw != "" {
		value, err := qty.Parse(raw)
		if err != nil || !value.IsPositive() {
			errs = append(errs, RowError{
				Line: line, Column: "коэффициент",
				Message: "должен быть числом больше нуля",
			})
		} else {
			row.UnitFactor = value
		}
	}

	if raw := cell("price"); raw != "" {
		value, err := qty.Parse(raw)
		if err != nil || value.IsNegative() {
			errs = append(errs, RowError{Line: line, Column: "цена", Message: "не число"})
		} else {
			row.Price = &value
		}
	}

	if raw := cell("service_level"); raw != "" {
		level, err := parseServiceLevel(raw)
		if err != nil {
			errs = append(errs, RowError{
				Line: line, Column: "уровень сервиса",
				Message: "допустимы 90, 95 и 99",
			})
		} else {
			row.ServiceLevel = level
		}
	}

	return row, errs
}

func parseServiceLevel(raw string) (int, error) {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "%")
	switch raw {
	case "90":
		return 90, nil
	case "95":
		return 95, nil
	case "99":
		return 99, nil
	default:
		return 0, fmt.Errorf("недопустимый уровень сервиса %q", raw)
	}
}

func isBlank(record []string) bool {
	for _, cell := range record {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
