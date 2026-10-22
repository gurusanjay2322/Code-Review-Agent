package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/hibiken/asynq"

	"code-review-agent/gateway/internal/config"
	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/jobs"
	"code-review-agent/gateway/internal/queue"
	"code-review-agent/gateway/internal/review"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireWorker(); err != nil {
		return err
	}
	app, err := githubapp.NewApp(cfg.GitHubAppID, cfg.GitHubPrivateKeyPath, cfg.GitHubAPIURL)
	if err != nil {
		return err
	}
	asynqOpt, _, err := queue.RedisOpt(cfg.RedisURL)
	if err != nil {
		return err
	}

	svc := &review.Service{
		Cfg:    cfg,
		GitHub: func(id int64) review.GitHub { return app.Installation(id) },
		Engine: engine.NewClient(cfg.EngineURL),
		Log:    log,
	}

	srv := asynq.NewServer(asynqOpt, asynq.Config{
		Concurrency: cfg.WorkerConcurrency,
		Logger:      asynqLogger{log},
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(jobs.TypeReviewPR, func(ctx context.Context, t *asynq.Task) error {
		var job jobs.ReviewJob
		if err := json.Unmarshal(t.Payload(), &job); err != nil {
			return fmt.Errorf("decode job: %v: %w", err, asynq.SkipRetry)
		}
		_, err := svc.Run(ctx, job)
		return err
	})

	log.Info("worker started", "concurrency", cfg.WorkerConcurrency, "engine", cfg.EngineURL)
	return srv.Run(mux)
}

type asynqLogger struct{ l *slog.Logger }

func (a asynqLogger) Debug(args ...any) { a.l.Debug(fmt.Sprint(args...)) }
func (a asynqLogger) Info(args ...any)  { a.l.Info(fmt.Sprint(args...)) }
func (a asynqLogger) Warn(args ...any)  { a.l.Warn(fmt.Sprint(args...)) }
func (a asynqLogger) Error(args ...any) { a.l.Error(fmt.Sprint(args...)) }
func (a asynqLogger) Fatal(args ...any) { a.l.Error(fmt.Sprint(args...)); os.Exit(1) }
