package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	GatewayAddr          string
	RedisURL             string
	EngineURL            string
	GitHubAppID          int64
	GitHubPrivateKeyPath string
	GitHubWebhookSecret  string
	GitHubAPIURL         string

	MaxFiles         int
	MaxComments      int
	MinConfidence    float64
	MinSeverity      string
	ChunkTokenBudget int
	MaxTotalTokens   int
	MaxPatchLines    int
	ContextLines     int

	VerifyFindings    bool
	EnableCheckRun    bool
	WorkerConcurrency int
}

func Load() (*Config, error) {
	c := &Config{
		GatewayAddr:          env("GATEWAY_ADDR", ":8080"),
		RedisURL:             env("REDIS_URL", "redis://localhost:6379/0"),
		EngineURL:            strings.TrimRight(env("ENGINE_URL", "http://localhost:8090"), "/"),
		GitHubPrivateKeyPath: env("GITHUB_PRIVATE_KEY_PATH", ""),
		GitHubWebhookSecret:  env("GITHUB_WEBHOOK_SECRET", ""),
		GitHubAPIURL:         strings.TrimRight(env("GITHUB_API_URL", "https://api.github.com"), "/"),
		MaxFiles:             envInt("MAX_FILES", 50),
		MaxComments:          envInt("MAX_COMMENTS", 15),
		MinConfidence:        envFloat("MIN_CONFIDENCE", 0.6),
		MinSeverity:          strings.ToLower(env("MIN_SEVERITY", "low")),
		ChunkTokenBudget:     envInt("CHUNK_TOKEN_BUDGET", 12000),
		MaxTotalTokens:       envInt("MAX_TOTAL_TOKENS", 120000),
		MaxPatchLines:        envInt("MAX_PATCH_LINES", 1500),
		ContextLines:         envInt("CONTEXT_LINES", 40),
		VerifyFindings:       envBool("VERIFY_FINDINGS", true),
		EnableCheckRun:       envBool("ENABLE_CHECK_RUN", false),
		WorkerConcurrency:    envInt("WORKER_CONCURRENCY", 4),
	}
	if id := env("GITHUB_APP_ID", ""); id != "" {
		v, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid GITHUB_APP_ID: %w", err)
		}
		c.GitHubAppID = v
	}
	return c, nil
}

func (c *Config) RequireServer() error {
	if c.GitHubWebhookSecret == "" {
		return fmt.Errorf("GITHUB_WEBHOOK_SECRET is required")
	}
	return nil
}

func (c *Config) RequireWorker() error {
	if c.GitHubAppID == 0 {
		return fmt.Errorf("GITHUB_APP_ID is required")
	}
	if c.GitHubPrivateKeyPath == "" {
		return fmt.Errorf("GITHUB_PRIVATE_KEY_PATH is required")
	}
	return nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(env(key, ""))
	if err != nil {
		return def
	}
	return v
}

func envFloat(key string, def float64) float64 {
	v, err := strconv.ParseFloat(env(key, ""), 64)
	if err != nil {
		return def
	}
	return v
}

func envBool(key string, def bool) bool {
	v, err := strconv.ParseBool(env(key, ""))
	if err != nil {
		return def
	}
	return v
}
