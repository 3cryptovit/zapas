package main

import (
	"context"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/vostapenko/zapas/internal/clock"
	"github.com/vostapenko/zapas/internal/forecast/pipeline"
	"github.com/vostapenko/zapas/internal/notify"
	"github.com/vostapenko/zapas/internal/platform/config"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/replenishment"
	"github.com/vostapenko/zapas/internal/sandbox"
	"github.com/vostapenko/zapas/internal/stock"
	"github.com/vostapenko/zapas/internal/worker"
)

// runWorker выполняет периодические задачи (прогноз, сводки, очистка)
// и разгребает очередь уведомлений (§9).
func runWorker(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	db, err := postgres.Connect(ctx, cfg.DB.URL, cfg.DB.MaxConns)
	if err != nil {
		return err
	}
	defer db.Close()

	maint, err := postgres.Connect(ctx, cfg.DB.MaintURL, 4)
	if err != nil {
		return err
	}
	defer maint.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr})
	defer func() { _ = rdb.Close() }()

	cl := clock.System{}

	notifySvc := notify.NewService(db, maint, cl, cfg.App.BaseURL)
	replenishmentSvc := replenishment.NewService(db, cl, notifySvc)
	stockSvc := stock.NewService(db, cl, replenishmentSvc)
	pipelineSvc := pipeline.NewService(db, cl, replenishmentSvc, log)
	sandboxSvc := sandbox.NewService(db, maint, cl, pipelineSvc,
		cfg.Sandbox.TTL, cfg.App.BaseURL, log)

	// Каналы доставки настраиваются переменными окружения; ненастроенный
	// канал просто не регистрируется, и сообщения в нём сразу уходят
	// в failed с понятной причиной (ADR-004).
	senders := map[notify.Channel]notify.Sender{}
	if telegram := notify.NewTelegramSender(cfg.Notify.TelegramBotToken); telegram.Configured() {
		senders[notify.ChannelTelegram] = telegram
	} else {
		log.Warn("Telegram не настроен: TELEGRAM_BOT_TOKEN пуст")
	}
	if email := notify.NewEmailSender(cfg.Notify.SMTPAddr, cfg.Notify.SMTPFrom, "", ""); email.Configured() {
		senders[notify.ChannelEmail] = email
	} else {
		log.Warn("SMTP не настроен")
	}

	dispatcher := notify.NewDispatcher(maint, senders, log)

	tasks := worker.NewTasks(db, maint, cl, pipelineSvc, notifySvc, dispatcher, stockSvc, log)
	runner := worker.NewRunner(tasks, sandboxSvc.Cleanup, cfg.Redis.Addr, log)

	return runner.Run(ctx)
}
