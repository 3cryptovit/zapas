// Package postgres — подключение к БД и транзакции с выставленным тенантом.
//
// Ключевая деталь: app.tenant_id ставится через SET LOCAL внутри транзакции.
// SET без LOCAL остался бы на соединении и «протёк» бы в следующий запрос
// другого тенанта через пул (§12.1, п. 2).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB — пул соединений. В приложении их два: под ролью приложения (RLS
// действует) и под обслуживающей ролью (BYPASSRLS), см. ADR-005.
type DB struct {
	pool *pgxpool.Pool
}

// Connect поднимает пул и проверяет связь.
func Connect(ctx context.Context, url string, maxConns int32) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: разбор DSN: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// numeric ↔ decimal.Decimal: количества не проходят через float.
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: создание пула: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: нет связи: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Pool отдаёт пул для запросов вне транзакции. Прямое использование в коде
// модулей нежелательно: тенант выставляется только в транзакции.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Close закрывает пул.
func (db *DB) Close() { db.pool.Close() }

// Ping — проверка готовности для /readyz.
func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

// InTx выполняет fn в транзакции без привязки к тенанту.
// Политики RLS в таком соединении не пропустят ни одной строки, поэтому годится
// только для обслуживающей роли и запросов к таблицам без tenant_id.
func (db *DB) InTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.inTx(ctx, "", fn)
}

// InTenantTx выполняет fn в транзакции, где app.tenant_id равен tenantID.
// Это единственный поддерживаемый способ читать и писать данные клиента.
func (db *DB) InTenantTx(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if tenantID == "" {
		return errors.New("postgres: пустой tenant_id в транзакции")
	}
	return db.inTx(ctx, tenantID, fn)
}

func (db *DB) inTx(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) (err error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// Контекст мог быть уже отменён — откатываем всё равно.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if tenantID != "" {
		// set_config с is_local = true — параметризованный аналог SET LOCAL:
		// подставлять id в текст запроса нельзя.
		if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
			return fmt.Errorf("postgres: установка тенанта: %w", err)
		}
	}

	if err = fn(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// Коды ошибок PostgreSQL, на которые реагирует бизнес-логика.
const (
	CodeUniqueViolation       = "23505"
	CodeForeignKeyViolation   = "23503"
	CodeCheckViolation        = "23514"
	CodeInsufficientPrivilege = "42501"
)

// IsCode сообщает, что ошибка — это конкретное нарушение ограничения.
func IsCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// ConstraintName возвращает имя нарушенного ограничения, если ошибка от PostgreSQL.
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// IsNoRows — удобная обёртка над pgx.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
