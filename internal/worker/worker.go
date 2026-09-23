package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// Расписание периодических задач.
//
// Прогноз и сводка запускаются каждый час, а нужный тенант задача выбирает
// сама: тенанты живут в разных поясах, и «03:00» у каждого своё (§4.4).
var schedule = []struct {
	spec string
	task string
}{
	{"@every 30s", TaskOutbox},
	{"0 * * * *", TaskForecast},
	{"5 * * * *", TaskDigest},
	{"*/15 * * * *", TaskCutoff},
	{"10 * * * *", TaskLate},
	{"20 4 * * *", TaskReconcile},
	{"30 * * * *", TaskSandboxGC},
}

// Runner — воркер: сервер задач плюс планировщик (§9).
type Runner struct {
	tasks     *Tasks
	sandboxGC func(context.Context) (int, error)
	redis     asynq.RedisConnOpt
	log       *slog.Logger
}

// NewRunner собирает воркер. sandboxGC передаётся функцией, чтобы пакет
// не зависел от sandbox: тот и так тянет за собой половину системы.
func NewRunner(tasks *Tasks, sandboxGC func(context.Context) (int, error), redisAddr string, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		tasks:     tasks,
		sandboxGC: sandboxGC,
		redis:     asynq.RedisClientOpt{Addr: redisAddr},
		log:       log,
	}
}

// Run поднимает сервер и планировщик и держит их до отмены контекста.
func (r *Runner) Run(ctx context.Context) error {
	srv := asynq.NewServer(r.redis, asynq.Config{
		Concurrency: 4,
		// Ретраи самих задач: отдельная от outbox история. Упавшая ночная
		// задача должна попробовать ещё раз, а не ждать сутки.
		RetryDelayFunc: asynq.RetryDelayFunc(func(n int, _ error, _ *asynq.Task) time.Duration {
			return time.Duration(1<<n) * time.Minute
		}),
		Logger: asynqLogger{log: r.log},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskOutbox, r.handle(r.tasks.RunOutbox))
	mux.HandleFunc(TaskForecast, r.handle(r.tasks.RunForecast))
	mux.HandleFunc(TaskDigest, r.handle(r.tasks.RunDigest))
	mux.HandleFunc(TaskCutoff, r.handle(r.tasks.RunCutoffReminders))
	mux.HandleFunc(TaskLate, r.handle(r.tasks.RunLateAlerts))
	mux.HandleFunc(TaskReconcile, r.handle(r.tasks.RunReconcile))
	mux.HandleFunc(TaskSandboxGC, r.handle(r.runSandboxGC))

	if err := srv.Start(mux); err != nil {
		return fmt.Errorf("worker: запуск сервера задач: %w", err)
	}

	scheduler := asynq.NewScheduler(r.redis, &asynq.SchedulerOpts{
		Logger: asynqLogger{log: r.log},
	})
	for _, entry := range schedule {
		if _, err := scheduler.Register(entry.spec, asynq.NewTask(entry.task, nil)); err != nil {
			srv.Shutdown()
			return fmt.Errorf("worker: расписание %s: %w", entry.task, err)
		}
	}
	if err := scheduler.Start(); err != nil {
		srv.Shutdown()
		return fmt.Errorf("worker: запуск планировщика: %w", err)
	}

	r.log.Info("worker запущен", slog.Int("tasks", len(schedule)))

	<-ctx.Done()

	r.log.Info("остановка worker")
	scheduler.Shutdown()
	srv.Shutdown()
	return nil
}

// handle приводит задачу к сигнатуре asynq.
func (r *Runner) handle(fn func(context.Context) error) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, task *asynq.Task) error {
		if err := fn(ctx); err != nil {
			r.log.ErrorContext(ctx, "задача завершилась с ошибкой",
				slog.String("task", task.Type()),
				slog.String("err", err.Error()),
			)
			return err
		}
		return nil
	}
}

func (r *Runner) runSandboxGC(ctx context.Context) error {
	if r.sandboxGC == nil {
		return nil
	}
	start := time.Now()
	_, err := r.sandboxGC(ctx)
	observeTask(TaskSandboxGC, start, err)
	return err
}

// asynqLogger переводит логи asynq в общий slog, чтобы в stdout был
// один формат JSON (§9.1).
type asynqLogger struct{ log *slog.Logger }

func (l asynqLogger) Debug(args ...any) { l.log.Debug(join(args)) }
func (l asynqLogger) Info(args ...any)  { l.log.Info(join(args)) }
func (l asynqLogger) Warn(args ...any)  { l.log.Warn(join(args)) }
func (l asynqLogger) Error(args ...any) { l.log.Error(join(args)) }
func (l asynqLogger) Fatal(args ...any) { l.log.Error(join(args)) }

func join(args []any) string { return fmt.Sprint(args...) }
