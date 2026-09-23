package postgres

import (
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // драйвер database/sql для goose
)

// openSQL открывает соединение через database/sql: goose работает только с ним.
func openSQL(url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("migrate: подключение: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
