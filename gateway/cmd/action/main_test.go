package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/testfixtures"
)

const eventJSON = `{
  "action": "opened",
  "number": 7,
  "pull_request": {
    "number": 7, "title": "Add user lookup", "body": "", "draft": false, "state": "open",
    "head": {"sha": "headsha"}, "base": {"sha": "basesha"}
  },
  "repository": {"name": "api", "owner": {"login": "acme"}}
}`

func envMap(m map[string]string) getenv {
	return func(k string) string { return m[k] }
}

func writeEvent(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestLoadJob(t *testing.T) {
	job, skip, err := loadJob(envMap(map[string]string{"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": writeEvent(t, eventJSON)}))
	if err != nil || skip != "" || job.Owner != "acme" || job.Number != 7 || job.HeadSHA != "headsha" {
		t.Fatalf("job=%+v skip=%q err=%v", job, skip, err)
	}
	_, skip, _ = loadJob(envMap(map[string]string{"GITHUB_EVENT_NAME": "push"}))
	if skip == "" {
		t.Fatal("push events should be skipped")
	}
	if _, _, err := loadJob(envMap(map[string]string{"GITHUB_EVENT_NAME": "pull_request_target"})); err == nil {
		t.Fatal("missing GITHUB_EVENT_PATH should be an error")
	}
}

func TestBuildConfigMapsInputs(t *testing.T) {
	cfg, err := buildConfig(envMap(map[string]string{
		"GITHUB_API_URL":         "https://ghe.example.com/api/v3/",
		"INPUT_MAX-COMMENTS":     "5",
		"INPUT_MAX-TOTAL-TOKENS": "60000",
		"INPUT_MIN-CONFIDENCE":   "0.8",
		"INPUT_MIN-SEVERITY":     "High",
		"INPUT_VERIFY":           "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubAPIURL != "https://ghe.example.com/api/v3" || cfg.MaxComments != 5 || cfg.MaxTotalTokens != 60000 ||
		cfg.MinConfidence != 0.8 || cfg.MinSeverity != "high" || cfg.VerifyFindings || cfg.EnableCheckRun {
		t.Fatalf("unexpected config %+v", cfg)
	}
}

func TestBuildConfigRejectsBadInputs(t *testing.T) {
	for name, val := range map[string]string{
		"INPUT_MAX-COMMENTS":   "lots",
		"INPUT_MIN-CONFIDENCE": "2",
		"INPUT_MIN-SEVERITY":   "urgent",
		"INPUT_VERIFY":         "maybe",
	} {
		if _, err := buildConfig(envMap(map[string]string{name: val})); err == nil {
			t.Errorf("%s=%q should be rejected", name, val)
		}
	}
}

func TestParseFailOn(t *testing.T) {
	for in, want := range map[string]int{"": 0, "none": 0, "critical": 4, "Medium": 2} {
		if got, err := parseFailOn(in); err != nil || got != want {
			t.Errorf("parseFailOn(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := parseFailOn("sometimes"); err == nil {
		t.Error("expected error")
	}
}

func TestRunRequiresTokenAndKey(t *testing.T) {
	base := map[string]string{"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": writeEvent(t, eventJSON)}
	if code := run(context.Background(), envMap(base), quiet); code != 1 {
		t.Fatalf("missing token: exit %d", code)
	}
	base["GITHUB_TOKEN"] = "t"
	if code := run(context.Background(), envMap(base), quiet); code != 1 {
		t.Fatalf("missing cursor key: exit %d", code)
	}
}

// TestEngineHelper stands in for `node cli.js` when the full-run test execs it.
func TestEngineHelper(t *testing.T) {
	if os.Getenv("ACTION_ENGINE_HELPER") != "1" {
		return
	}
	defer os.Exit(0)
	if os.Getenv("CURSOR_API_KEY") != "key-123" || os.Getenv("LLM_PROVIDER") != "cursor" {
		fmt.Fprintln(os.Stderr, "engine did not receive the cursor key")
		os.Exit(1)
	}
	io.Copy(io.Discard, os.Stdin)
	switch os.Args[len(os.Args)-1] {
	case "review":
		json.NewEncoder(os.Stdout).Encode(engine.ReviewResponse{Findings: []engine.Finding{{
			Path: testfixtures.Path, Line: 9, Severity: "critical", Category: "security",
			Title: "SQL injection", Explanation: "name is formatted into SQL.",
			Evidence: `q := fmt.Sprintf("SELECT id, name FROM users WHERE name = '%s'", name)`, Confidence: 0.95,
		}}})
	case "verify":
		json.NewEncoder(os.Stdout).Encode(engine.VerifyResponse{Results: []engine.VerifyResult{{ID: "0", Supported: true}}})
	}
}

type fakeAPI struct {
	mu      sync.Mutex
	auth    []string
	reviews []githubapp.CreateReviewRequest
}

func (f *fakeAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/api/pulls/7/files", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		json.NewEncoder(w).Encode([]githubapp.File{{Filename: testfixtures.Path, Status: "modified", Patch: testfixtures.Patch}})
	})
	mux.HandleFunc("GET /repos/acme/api/contents/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if strings.HasSuffix(r.URL.Path, testfixtures.Path) {
			io.WriteString(w, testfixtures.Content)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /repos/acme/api/pulls/7/comments", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		io.WriteString(w, "[]")
	})
	mux.HandleFunc("GET /repos/acme/api/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		io.WriteString(w, "[]")
	})
	mux.HandleFunc("POST /repos/acme/api/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		var req githubapp.CreateReviewRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.reviews = append(f.reviews, req)
		f.mu.Unlock()
		io.WriteString(w, "{}")
	})
	return mux
}

func (f *fakeAPI) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
}

func fullRunEnv(t *testing.T, apiURL string) map[string]string {
	t.Setenv("ACTION_ENGINE_HELPER", "1")
	return map[string]string{
		"GITHUB_EVENT_NAME":    "pull_request",
		"GITHUB_EVENT_PATH":    writeEvent(t, eventJSON),
		"GITHUB_API_URL":       apiURL,
		"INPUT_GITHUB-TOKEN":   "ghs_token",
		"INPUT_CURSOR-API-KEY": "key-123",
		"ENGINE_COMMAND":       os.Args[0] + " -test.run=TestEngineHelper --",
	}
}

func TestRunEndToEnd(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	if code := run(context.Background(), envMap(fullRunEnv(t, srv.URL)), quiet); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(api.reviews) != 1 || len(api.reviews[0].Comments) != 1 || api.reviews[0].Comments[0].Line != 9 {
		t.Fatalf("reviews posted: %+v", api.reviews)
	}
	for _, a := range api.auth {
		if a != "Bearer ghs_token" {
			t.Fatalf("request sent with Authorization %q", a)
		}
	}
}

func TestRunFailOn(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api.handler())
	defer srv.Close()

	env := fullRunEnv(t, srv.URL)
	env["INPUT_FAIL-ON"] = "high"
	if code := run(context.Background(), envMap(env), quiet); code != 1 {
		t.Fatalf("a posted critical finding with fail-on=high should exit 1, got %d", code)
	}
	if len(api.reviews) != 1 {
		t.Fatal("the review is still posted before failing")
	}
}
