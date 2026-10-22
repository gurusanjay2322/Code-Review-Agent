// Command action runs one PR review inside a GitHub Actions job. It reads the triggering event
// from GITHUB_EVENT_PATH, talks to GitHub with the workflow token, and runs the engine CLI as a
// subprocess, so no App, webhook server or queue is involved.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"code-review-agent/gateway/internal/config"
	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/jobs"
	"code-review-agent/gateway/internal/review"
	"code-review-agent/gateway/internal/validate"
	"code-review-agent/gateway/internal/webhook"
)

const defaultEngineCommand = "node /app/engine/src/cli.js"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	os.Exit(run(context.Background(), os.Getenv, log))
}

type getenv func(string) string

// input reads an action input. Docker actions receive `with:` values as INPUT_<NAME> with the
// name uppercased and hyphens kept, e.g. INPUT_CURSOR-API-KEY.
func input(env getenv, name string) string {
	return strings.TrimSpace(env("INPUT_" + strings.ToUpper(strings.ReplaceAll(name, " ", "_"))))
}

func run(ctx context.Context, env getenv, log *slog.Logger) int {
	job, skip, err := loadJob(env)
	if err != nil {
		log.Error("cannot start review", "err", err)
		return 1
	}
	if skip != "" {
		log.Info("nothing to do", "reason", skip)
		return 0
	}

	cfg, err := buildConfig(env)
	if err != nil {
		log.Error("invalid inputs", "err", err)
		return 1
	}
	token := input(env, "github-token")
	if token == "" {
		token = env("GITHUB_TOKEN")
	}
	if token == "" {
		log.Error("no GitHub token: pass github-token or expose GITHUB_TOKEN")
		return 1
	}
	apiKey := input(env, "cursor-api-key")
	if apiKey == "" {
		log.Error("cursor-api-key input is required (store it as a repository secret)")
		return 1
	}
	failOn, err := parseFailOn(input(env, "fail-on"))
	if err != nil {
		log.Error("invalid inputs", "err", err)
		return 1
	}

	gh := githubapp.NewClient(cfg.GitHubAPIURL, &http.Client{
		Timeout:   60 * time.Second,
		Transport: &tokenTransport{token: token, base: http.DefaultTransport},
	})
	svc := &review.Service{
		Cfg:    cfg,
		GitHub: func(int64) review.GitHub { return gh },
		Engine: engineClient(env, apiKey, cfg),
		Log:    log,
	}

	log.Info("reviewing", "repo", job.FullName(), "pr", job.Number, "head", job.HeadSHA, "action", job.Action)
	sum, err := svc.Run(ctx, *job)
	if err != nil {
		log.Error("review failed", "err", err)
		return 1
	}
	if failOn > 0 && sum.HighestPostedRank() >= failOn {
		log.Error("failing: findings at or above fail-on severity were posted", "fail_on", input(env, "fail-on"))
		return 1
	}
	return 0
}

func loadJob(env getenv) (*jobs.ReviewJob, string, error) {
	event := env("GITHUB_EVENT_NAME")
	if event != "pull_request" && event != "pull_request_target" {
		return nil, "event " + event + " is not a pull request event", nil
	}
	path := env("GITHUB_EVENT_PATH")
	if path == "" {
		return nil, "", fmt.Errorf("GITHUB_EVENT_PATH is not set")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read event: %w", err)
	}
	return webhook.ParsePullRequest(body, env("GITHUB_RUN_ID"), false)
}

// buildConfig starts from the env-based defaults and applies action inputs on top.
func buildConfig(env getenv) (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if v := env("GITHUB_API_URL"); v != "" {
		cfg.GitHubAPIURL = strings.TrimRight(v, "/")
	}
	cfg.EnableCheckRun = false // the workflow job already reports status

	ints := map[string]*int{
		"max-comments":     &cfg.MaxComments,
		"max-files":        &cfg.MaxFiles,
		"max-total-tokens": &cfg.MaxTotalTokens,
	}
	for name, dst := range ints {
		if v := input(env, name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%s must be a non-negative integer, got %q", name, v)
			}
			*dst = n
		}
	}
	if v := input(env, "min-confidence"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return nil, fmt.Errorf("min-confidence must be between 0 and 1, got %q", v)
		}
		cfg.MinConfidence = f
	}
	if v := strings.ToLower(input(env, "min-severity")); v != "" {
		if validate.SeverityRank(v) == 0 {
			return nil, fmt.Errorf("min-severity must be low, medium, high or critical, got %q", v)
		}
		cfg.MinSeverity = v
	}
	if v := input(env, "verify"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("verify must be true or false, got %q", v)
		}
		cfg.VerifyFindings = b
	}
	return cfg, nil
}

func parseFailOn(v string) (int, error) {
	v = strings.ToLower(v)
	if v == "" || v == "none" {
		return 0, nil
	}
	rank := validate.SeverityRank(v)
	if rank == 0 {
		return 0, fmt.Errorf("fail-on must be none, low, medium, high or critical, got %q", v)
	}
	return rank, nil
}

func engineClient(env getenv, apiKey string, cfg *config.Config) *engine.ExecClient {
	command := env("ENGINE_COMMAND")
	if command == "" {
		command = defaultEngineCommand
	}
	engineEnv := []string{"LLM_PROVIDER=cursor", "CURSOR_API_KEY=" + apiKey}
	if m := input(env, "model"); m != "" {
		engineEnv = append(engineEnv, "LLM_MODEL="+m)
	}
	if c := input(env, "concurrency"); c != "" {
		engineEnv = append(engineEnv, "ENGINE_CONCURRENCY="+c)
	}
	return &engine.ExecClient{Command: strings.Fields(command), Env: engineEnv}
}

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}
