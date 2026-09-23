package sandbox

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/vostapenko/zapas/internal/platform/metrics"
	"github.com/vostapenko/zapas/internal/platform/postgres"
	"github.com/vostapenko/zapas/internal/platform/sqlc"
)

// CleanupBatch — размер пачки удаления. Пачками, чтобы не блокировать
// базу надолго (§7.5).
const CleanupBatch = 20

// Cleanup удаляет истёкшие песочницы каскадом.
//
// Задача идёт раз в час. Удаление тенанта уносит всё его содержимое:
// внешние ключи объявлены с ON DELETE CASCADE.
func (s *Service) Cleanup(ctx context.Context) (int, error) {
	now := s.clock.Now()
	deleted := 0

	for {
		var batch []uuid.UUID

		err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
			ids, err := sqlc.New(tx).ListExpiredSandboxes(ctx, sqlc.ListExpiredSandboxesParams{
				ExpiresAt: postgres.Time(now),
				Limit:     CleanupBatch,
			})
			if err != nil {
				return fmt.Errorf("sandbox: истёкшие тенанты: %w", err)
			}
			batch = ids
			return nil
		})
		if err != nil {
			return deleted, err
		}
		if len(batch) == 0 {
			break
		}

		for _, id := range batch {
			err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
				return sqlc.New(tx).DeleteTenant(ctx, id)
			})
			if err != nil {
				// Одна неудача не должна останавливать очистку:
				// иначе очередь истёкших тенантов растёт бесконечно.
				s.log.ErrorContext(ctx, "не удалось удалить песочницу",
					slog.String("tenant_id", id.String()),
					slog.String("err", err.Error()),
				)
				continue
			}
			deleted++
		}

		// Пачка меньше лимита — значит это была последняя.
		if len(batch) < CleanupBatch {
			break
		}
	}

	if deleted > 0 {
		s.log.InfoContext(ctx, "удалены истёкшие песочницы", slog.Int("count", deleted))
	}
	if err := s.refreshActiveMetric(ctx); err != nil {
		s.log.WarnContext(ctx, "не удалось обновить метрику песочниц",
			slog.String("err", err.Error()))
	}
	return deleted, nil
}

// ActiveCount — сколько живых песочниц сейчас.
func (s *Service) ActiveCount(ctx context.Context) (int, error) {
	var active int
	err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		n, err := sqlc.New(tx).CountActiveSandboxes(ctx, postgres.Time(s.clock.Now()))
		if err != nil {
			return fmt.Errorf("sandbox: счётчик песочниц: %w", err)
		}
		active = int(n)
		return nil
	})
	return active, err
}

func (s *Service) refreshActiveMetric(ctx context.Context) error {
	active, err := s.ActiveCount(ctx)
	if err != nil {
		return err
	}
	metrics.SandboxActive.Set(float64(active))
	return nil
}

// Reset возвращает песочницу в исходное состояние: сносит данные и
// создаёт их заново с тем же seed (§7.4, кнопка «Сбросить демо»).
func (s *Service) Reset(ctx context.Context, tenantID uuid.UUID, seed int64) (Created, error) {
	err := s.maint.InTx(ctx, func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).DeleteTenant(ctx, tenantID)
	})
	if err != nil {
		return Created{}, fmt.Errorf("sandbox: сброс: %w", err)
	}
	return s.Create(ctx, seed)
}

// SetAutopilot включает и выключает автозаказ в демо (§7.3).
func (s *Service) SetAutopilot(ctx context.Context, tenantID uuid.UUID, on bool) error {
	return s.db.InTenantTx(ctx, tenantID.String(), func(ctx context.Context, tx postgres.Tx) error {
		return sqlc.New(tx).SetTenantAutopilot(ctx, sqlc.SetTenantAutopilotParams{
			ID: tenantID, Autopilot: on,
		})
	})
}
