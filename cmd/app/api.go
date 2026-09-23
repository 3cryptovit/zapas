package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/vostapenko/zapas/internal/api"
	"github.com/vostapenko/zapas/internal/platform/config"
)

func runAPI(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	app, err := api.Build(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer app.Close()

	srv := &http.Server{
		Addr:              cfg.App.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	// Метрики слушают отдельный порт: наружу /metrics не публикуется (§11).
	metricsSrv := &http.Server{
		Addr:              cfg.Observ.MetricsAddr,
		Handler:           api.MetricsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 2)
	go func() {
		log.Info("api слушает", slog.String("addr", cfg.App.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	go func() {
		log.Info("метрики слушают", slog.String("addr", cfg.Observ.MetricsAddr))
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("остановка api")
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
	return srv.Shutdown(shutdownCtx)
}
