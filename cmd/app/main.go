// Команда app — единственный бинарник проекта. Режим выбирается первым
// аргументом: api | worker | migrate | seed (ADR-001).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	// Встроенная база часовых поясов: в distroless-образе /usr/share/zoneinfo
	// нет, а «день» тенанта считается в его поясе (§12).
	_ "time/tzdata"

	"github.com/vostapenko/zapas/internal/platform/config"
	"github.com/vostapenko/zapas/internal/platform/logging"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/migrations"
)

func main() {
	if err := run(); err != nil {
		// Логгер может быть ещё не собран — пишем в stderr напрямую.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("укажите режим: api | worker | migrate | seed")
	}
	mode := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(cfg.Observ.LogLevel, os.Stdout)
	slog.SetDefault(log)

	// Завершение по SIGINT/SIGTERM: docker compose down не должен рвать запросы.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch mode {
	case "migrate":
		return runMigrate(ctx, cfg, log)
	case "api":
		return runAPI(ctx, cfg, log)
	case "worker":
		return runWorker(ctx, cfg, log)
	case "seed":
		return runSeed(ctx, cfg, log)
	default:
		return fmt.Errorf("неизвестный режим %q: ожидается api | worker | migrate | seed", mode)
	}
}

func runMigrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	sub := "up"
	if len(os.Args) > 2 {
		sub = os.Args[2]
	}
	switch sub {
	case "up":
		if err := postgres.Migrate(ctx, cfg.DB.MigrateURL, migrations.FS); err != nil {
			return err
		}
		log.Info("миграции применены")
		return nil
	case "status":
		return postgres.MigrateStatus(ctx, cfg.DB.MigrateURL, migrations.FS)
	case "down":
		if cfg.App.IsProd() {
			return errors.New("migrate down в проде запрещён: откат делается новой миграцией")
		}
		return postgres.MigrateDown(ctx, cfg.DB.MigrateURL, migrations.FS)
	default:
		return fmt.Errorf("неизвестная подкоманда migrate %q: up | down | status", sub)
	}
}
