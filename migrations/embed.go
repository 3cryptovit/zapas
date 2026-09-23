// Package migrations хранит SQL-миграции goose внутри бинарника, чтобы режим
// `app migrate` работал в distroless-образе без внешних файлов.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
