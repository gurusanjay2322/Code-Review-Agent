package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-review-agent/gateway/internal/jobs"
)

var secret = []byte("test-secret")

const openedPayload = `{
  "action": "opened",
  "number": 7,
  "pull_request": {
    "number": 7, "title": "Add user lookup", "body": "desc", "draft": false, "state": "open",
    "head": {"sha": "headsha"}, "base": {"sha": "basesha"}
  },
  "repository": {"name": "api", "owner": {"login": "acme"}},
  "installation": {"id": 42}
}`

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"a":1}`)
	if err := VerifySignature(secret, body, Sign(secret, body)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	cases := map[string]string{
		"missing":      "",
		"wrong prefix": strings.Replace(Sign(secret, body), "sha256=", "sha1=", 1),
		"not hex":      "sha256=zz",
		"wrong secret": Sign([]byte("other"), body),
		"tampered":     Sign(secret, []byte(`{"a":2}`)),
	}
	for name, header := range cases {
		if err := VerifySignature(secret, body, header); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

type fakeDedupe struct{ seen map[string]bool }

func (f *fakeDedupe) Claim(_ context.Context, id string) (bool, error) {
	if f.seen[id] {
		return false, nil
	}
	f.seen[id] = true
	return true, nil
}

func (f *fakeDedupe) Release(_ context.Context, id string) error {
	delete(f.seen, id)
	return nil
}

type fakeQueue struct {
	jobs []jobs.ReviewJob
	err  error
}

func (f *fakeQueue) Enqueue(_ context.Context, j jobs.ReviewJob) error {
	if f.err != nil {
		return f.err
	}
	f.jobs = append(f.jobs, j)
	return nil
}

func newHandler() (*Handler, *fakeQueue, *fakeDedupe) {
	q := &fakeQueue{}
	d := &fakeDedupe{seen: map[string]bool{}}
	return &Handler{Secret: secret, Dedupe: d, Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, q, d
}

func send(h http.Handler, event, delivery, body string, signed bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	if signed {
		req.Header.Set("X-Hub-Signature-256", Sign(secret, []byte(body)))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerQueuesOpenedPR(t *testing.T) {
	h, q, _ := newHandler()
	rec := send(h, "pull_request", "d1", openedPayload, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if len(q.jobs) != 1 {
		t.Fatalf("jobs = %d", len(q.jobs))
	}
	j := q.jobs[0]
	if j.Owner != "acme" || j.Repo != "api" || j.Number != 7 || j.InstallationID != 42 || j.HeadSHA != "headsha" || j.BaseSHA != "basesha" {
		t.Fatalf("unexpected job %+v", j)
	}
}

func TestHandlerRejectsUnsigned(t *testing.T) {
	h, q, _ := newHandler()
	if rec := send(h, "pull_request", "d1", openedPayload, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(q.jobs) != 0 {
		t.Fatal("unsigned request must not enqueue")
	}
}

func TestHandlerDedupesDeliveries(t *testing.T) {
	h, q, _ := newHandler()
	send(h, "pull_request", "same", openedPayload, true)
	rec := send(h, "pull_request", "same", openedPayload, true)
	if rec.Code != http.StatusOK || len(q.jobs) != 1 {
		t.Fatalf("redelivery: status %d, jobs %d", rec.Code, len(q.jobs))
	}
}

func TestHandlerReleasesDeliveryWhenQueueFails(t *testing.T) {
	h, q, d := newHandler()
	q.err = io.ErrUnexpectedEOF
	if rec := send(h, "pull_request", "d1", openedPayload, true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if d.seen["d1"] {
		t.Fatal("failed enqueue must release the delivery so GitHub can redeliver")
	}
}

func TestHandlerIgnoresDraftsAndOtherEvents(t *testing.T) {
	h, q, _ := newHandler()
	draft := strings.Replace(openedPayload, `"draft": false`, `"draft": true`, 1)
	closed := strings.Replace(openedPayload, `"action": "opened"`, `"action": "closed"`, 1)
	for name, tc := range map[string][2]string{
		"draft":  {"pull_request", draft},
		"closed": {"pull_request", closed},
		"push":   {"push", `{}`},
		"ping":   {"ping", `{"zen":"hi"}`},
	} {
		if rec := send(h, tc[0], name, tc[1], true); rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
	if len(q.jobs) != 0 {
		t.Fatalf("jobs = %d, want 0", len(q.jobs))
	}
}

func TestParseSynchronizeKeepsBefore(t *testing.T) {
	body := strings.Replace(openedPayload, `"action": "opened",`, `"action": "synchronize", "before": "oldsha", "after": "headsha",`, 1)
	job, reason, err := ParsePullRequest([]byte(body), "d", true)
	if err != nil || job == nil {
		t.Fatalf("job=%v reason=%q err=%v", job, reason, err)
	}
	if job.BeforeSHA != "oldsha" {
		t.Fatalf("before = %q", job.BeforeSHA)
	}
}

func TestParseInstallationRequirement(t *testing.T) {
	noInstall := strings.Replace(openedPayload, `"installation": {"id": 42}`, `"sender": {"login": "dev"}`, 1)
	if _, _, err := ParsePullRequest([]byte(noInstall), "d", true); err == nil {
		t.Fatal("App webhooks must carry an installation id")
	}
	job, _, err := ParsePullRequest([]byte(noInstall), "", false)
	if err != nil || job == nil || job.InstallationID != 0 || job.Number != 7 {
		t.Fatalf("Actions payload: job=%+v err=%v", job, err)
	}
}
