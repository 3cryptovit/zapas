package postgres

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Migrate применяет миграции goose от роли владельца схемы.
// Отдельное подключение: роль приложения права на DDL не имеет.
func Migrate(ctx context.Context, url string, files fs.FS) error {
	sqlDB, err := openSQL(url)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(files)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: диалект: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// MigrateStatus печатает текущее состояние миграций.
func MigrateStatus(ctx context.Context, url string, files fs.FS) error {
	sqlDB, err := openSQL(url)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(files)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: диалект: %w", err)
	}
	return goose.StatusContext(ctx, sqlDB, ".")
}

// MigrateDown откатывает последнюю миграцию. В проде не используется:
// откат делается новой миграцией, см. docs/runbooks/rollback.md.
func MigrateDown(ctx context.Context, url string, files fs.FS) error {
	sqlDB, err := openSQL(url)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(files)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: диалект: %w", err)
	}
	return goose.DownContext(ctx, sqlDB, ".")
}
