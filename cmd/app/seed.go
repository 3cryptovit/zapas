package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/platform/config"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/seed"
)

// runSeed заводит рабочего тенанта с владельцем.
//
// Генератор песочницы (§7.2) — отдельная история, он живёт в internal/sandbox
// и запускается кнопкой «Открыть демо».
//
//	app seed                                  локальная учётка для разработки
//	app seed -email a@b.ru -password '…' -name 'Кофейня'   боевая
func runSeed(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	email := fs.String("email", "", "email владельца")
	password := fs.String("password", "", "пароль владельца")
	name := fs.String("name", "", "название организации")
	timezone := fs.String("tz", "", "часовой пояс, например Europe/Moscow")
	sample := fs.Bool("sample", false, "завести демонстрационные позиции")

	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	// В проде учётка с паролем по умолчанию — это открытая дверь
	// на публичном адресе.
	if cfg.App.IsProd() && (*email == "" || *password == "") {
		return fmt.Errorf("seed в проде требует -email и -password")
	}

	db, err := postgres.Connect(ctx, cfg.DB.URL, cfg.DB.MaxConns)
	if err != nil {
		return err
	}
	defer db.Close()

	maint, err := postgres.Connect(ctx, cfg.DB.MaintURL, 2)
	if err != nil {
		return err
	}
	defer maint.Close()

	result, err := seed.Run(ctx, db, maint, clock.System{}, seed.Options{
		Email:          *email,
		Password:       *password,
		TenantName:     *name,
		Timezone:       *timezone,
		WithSampleData: *sample,
	}, log)
	if err != nil {
		return err
	}

	if !result.Created {
		log.Info("seed: ничего не создано, пользователь уже есть")
		return nil
	}
	// Пароль печатаем только когда он сгенерирован не нами: так его
	// не приходится искать в логах systemd.
	log.Info("seed: готово",
		slog.String("email", result.Email),
		slog.String("tenant_id", result.TenantID.String()),
	)
	return nil
}
