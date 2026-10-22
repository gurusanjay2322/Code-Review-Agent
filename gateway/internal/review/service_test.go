package review

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"code-review-agent/gateway/internal/config"
	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/jobs"
	"code-review-agent/gateway/internal/testfixtures"
)

type fakeGitHub struct {
	files      []githubapp.File
	compare    *githubapp.Comparison
	contents   map[string]string
	comments   []githubapp.ReviewComment
	reviews    []githubapp.CreateReviewRequest
	single     []githubapp.DraftComment
	rejectLine int
	checks     []githubapp.CheckRun
}

func (f *fakeGitHub) ListPullRequestFiles(context.Context, string, string, int) ([]githubapp.File, error) {
	return f.files, nil
}
func (f *fakeGitHub) Compare(context.Context, string, string, string, string) (*githubapp.Comparison, error) {
	if f.compare == nil {
		return nil, &githubapp.APIError{StatusCode: http.StatusNotFound}
	}
	return f.compare, nil
}
func (f *fakeGitHub) GetRawContent(_ context.Context, _, _, path, _ string) ([]byte, bool, error) {
	c, ok := f.contents[path]
	return []byte(c), ok, nil
}
func (f *fakeGitHub) ListReviewComments(context.Context, string, string, int) ([]githubapp.ReviewComment, error) {
	return f.comments, nil
}
func (f *fakeGitHub) ListReviews(context.Context, string, string, int) ([]githubapp.Review, error) {
	return nil, nil
}
func (f *fakeGitHub) CreateReview(_ context.Context, _, _ string, _ int, req githubapp.CreateReviewRequest) error {
	for _, c := range req.Comments {
		if c.Line == f.rejectLine {
			return &githubapp.APIError{StatusCode: http.StatusUnprocessableEntity, Message: "line could not be resolved"}
		}
	}
	f.reviews = append(f.reviews, req)
	return nil
}
func (f *fakeGitHub) CreateReviewComment(_ context.Context, _, _ string, _ int, _ string, c githubapp.DraftComment) error {
	if c.Line == f.rejectLine {
		return &githubapp.APIError{StatusCode: http.StatusUnprocessableEntity}
	}
	f.single = append(f.single, c)
	return nil
}
func (f *fakeGitHub) CreateCheckRun(_ context.Context, _, _ string, run githubapp.CheckRun) (*githubapp.CheckRun, error) {
	run.ID = 1
	f.checks = append(f.checks, run)
	return &run, nil
}
func (f *fakeGitHub) UpdateCheckRun(_ context.Context, _, _ string, _ int64, run githubapp.CheckRun) error {
	f.checks = append(f.checks, run)
	return nil
}

type fakeEngine struct {
	findings []engine.Finding
	requests []engine.ReviewRequest
}

func (e *fakeEngine) Review(_ context.Context, req engine.ReviewRequest) (*engine.ReviewResponse, error) {
	e.requests = append(e.requests, req)
	return &engine.ReviewResponse{Findings: e.findings}, nil
}
func (e *fakeEngine) Verify(_ context.Context, _ string, items []engine.VerifyItem) (*engine.VerifyResponse, error) {
	out := &engine.VerifyResponse{}
	for _, it := range items {
		out.Results = append(out.Results, engine.VerifyResult{ID: it.ID, Supported: true})
	}
	return out, nil
}

func newService(gh *fakeGitHub, eng *fakeEngine) *Service {
	return &Service{
		Cfg: &config.Config{
			MaxFiles: 50, MaxComments: 15, MinConfidence: 0.6, MinSeverity: "low",
			ChunkTokenBudget: 12000, MaxTotalTokens: 100000, MaxPatchLines: 1500, ContextLines: 40,
			VerifyFindings: true, EnableCheckRun: true,
		},
		GitHub: func(int64) GitHub { return gh },
		Engine: eng,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func fixturePR() *fakeGitHub {
	return &fakeGitHub{
		files: []githubapp.File{
			{Filename: testfixtures.Path, Status: "modified", Patch: testfixtures.Patch},
			{Filename: "package-lock.json", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+b"},
		},
		contents: map[string]string{testfixtures.Path: testfixtures.Content},
	}
}

var job = jobs.ReviewJob{Action: "opened", InstallationID: 1, Owner: "acme", Repo: "api", Number: 7, HeadSHA: "head", BaseSHA: "base", Title: "Add lookup"}

func sqlInjection() engine.Finding {
	return engine.Finding{
		Path: testfixtures.Path, Line: 9, Severity: "critical", Category: "security",
		Title: "SQL injection", Explanation: "User input is formatted into SQL.",
		Evidence: `q := fmt.Sprintf("SELECT id, name FROM users WHERE name = '%s'", name)`, Confidence: 0.9,
	}
}

func TestServiceEndToEnd(t *testing.T) {
	gh := fixturePR()
	hallucinated := sqlInjection()
	hallucinated.Title, hallucinated.Evidence = "Leaks file handle", "os.Open(path)"
	eng := &fakeEngine{findings: []engine.Finding{sqlInjection(), hallucinated}}

	if _, err := newService(gh, eng).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(eng.requests) != 1 || len(eng.requests[0].Chunks) != 1 {
		t.Fatalf("engine requests: %+v", eng.requests)
	}
	if strings.Contains(eng.requests[0].Chunks[0].Files[0].Content, "package-lock") {
		t.Fatal("lockfile should not be sent to the engine")
	}
	if len(gh.reviews) != 1 {
		t.Fatalf("reviews posted = %d", len(gh.reviews))
	}
	rv := gh.reviews[0]
	if rv.CommitID != "head" || rv.Event != "COMMENT" || len(rv.Comments) != 1 {
		t.Fatalf("unexpected review: %+v", rv)
	}
	c := rv.Comments[0]
	if c.Path != testfixtures.Path || c.Line != 9 || c.Side != "RIGHT" || !strings.Contains(c.Body, "ai-review:fp=") {
		t.Fatalf("unexpected comment: %+v", c)
	}
	if !strings.Contains(rv.Body, "package-lock.json") || !strings.Contains(rv.Body, "1 critical") || !strings.Contains(rv.Body, "filtered out") {
		t.Fatalf("summary missing details:\n%s", rv.Body)
	}
	last := gh.checks[len(gh.checks)-1]
	if last.Status != "completed" || last.Conclusion != "neutral" {
		t.Fatalf("check run: %+v", last)
	}
}

func TestServiceSkipsAlreadyPostedFindings(t *testing.T) {
	gh := fixturePR()
	eng := &fakeEngine{findings: []engine.Finding{sqlInjection()}}
	svc := newService(gh, eng)
	if _, err := svc.Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	gh.comments = []githubapp.ReviewComment{{Body: gh.reviews[0].Comments[0].Body}}

	sync := job
	sync.Action, sync.HeadSHA = "synchronize", "head2"
	if _, err := svc.Run(context.Background(), sync); err != nil {
		t.Fatal(err)
	}
	if len(gh.reviews) != 1 {
		t.Fatalf("a push with only known findings should not post again; reviews = %d", len(gh.reviews))
	}
}

func TestServiceFallsBackWhenGitHubRejectsALine(t *testing.T) {
	gh := fixturePR()
	gh.rejectLine = 10
	second := sqlInjection()
	second.Line, second.Severity, second.Category, second.Title = 10, "medium", "bug", "Unchecked query"
	second.Evidence = "row := db.QueryRow(q)"
	eng := &fakeEngine{findings: []engine.Finding{sqlInjection(), second}}

	if _, err := newService(gh, eng).Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if len(gh.single) != 1 || gh.single[0].Line != 9 {
		t.Fatalf("individually posted: %+v", gh.single)
	}
	if len(gh.reviews) != 1 || len(gh.reviews[0].Comments) != 0 || !strings.Contains(gh.reviews[0].Body, "Unchecked query") {
		t.Fatalf("summary should carry the rejected comment: %+v", gh.reviews)
	}
}

func TestServiceIncrementalReview(t *testing.T) {
	gh := fixturePR()
	gh.compare = &githubapp.Comparison{Status: "ahead", Files: []githubapp.File{
		{Filename: testfixtures.Path, Status: "modified", Patch: "@@ -10,1 +10,1 @@\n-\trow := db.QueryRow(old)\n+\trow := db.QueryRow(q)"},
	}}
	eng := &fakeEngine{findings: []engine.Finding{sqlInjection()}}
	sync := job
	sync.Action, sync.BeforeSHA = "synchronize", "prev"

	if _, err := newService(gh, eng).Run(context.Background(), sync); err != nil {
		t.Fatal(err)
	}
	content := eng.requests[0].Chunks[0].Files[0].Content
	if strings.Contains(content, "    9 +|") || !strings.Contains(content, "   10 +|") {
		t.Fatalf("only line 10 changed since last review:\n%s", content)
	}
	if len(gh.reviews) != 0 {
		t.Fatal("line 9 finding is out of incremental scope, nothing should be posted")
	}
}
