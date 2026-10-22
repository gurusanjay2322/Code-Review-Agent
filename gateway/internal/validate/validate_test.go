package validate

import (
	"context"
	"strings"
	"testing"

	"code-review-agent/gateway/internal/diff"
	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/testfixtures"
)

func files(t *testing.T) map[string]File {
	t.Helper()
	fd, err := diff.Parse(testfixtures.Path, testfixtures.Patch)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]File{testfixtures.Path: {Diff: fd, Lines: strings.Split(testfixtures.Content, "\n")}}
}

func sqlFinding() engine.Finding {
	return engine.Finding{
		Path: testfixtures.Path, Line: 9, Severity: "critical", Category: "security",
		Title:       "SQL injection via fmt.Sprintf",
		Explanation: "name is interpolated into the query.",
		Evidence:    `q := fmt.Sprintf("SELECT id, name FROM users WHERE name = '%s'", name)`,
		Suggestion:  "\trow := db.QueryRow(\"SELECT id, name FROM users WHERE name = $1\", name)",
		Confidence:  0.95,
	}
}

var defaultOpts = Options{MinConfidence: 0.6, MinSeverity: "low"}

func TestCheckAcceptsSupportedFinding(t *testing.T) {
	res := Check([]engine.Finding{sqlFinding()}, files(t), nil, defaultOpts)
	if len(res.Accepted) != 1 {
		t.Fatalf("accepted %d, rejected %+v", len(res.Accepted), res.Rejected)
	}
	if res.Accepted[0].Fingerprint == "" {
		t.Error("missing fingerprint")
	}
}

func TestCheckRelocatesToEvidence(t *testing.T) {
	f := sqlFinding()
	f.Line = 11 // model was off by two
	res := Check([]engine.Finding{f}, files(t), nil, defaultOpts)
	if len(res.Accepted) != 1 || res.Accepted[0].Line != 9 {
		t.Fatalf("expected relocation to line 9, got %+v / %+v", res.Accepted, res.Rejected)
	}
}

func TestCheckMultiLineEvidence(t *testing.T) {
	f := sqlFinding()
	f.Evidence = "q := fmt.Sprintf(\"SELECT id, name FROM users WHERE name = '%s'\", name)\n    row := db.QueryRow(q)"
	res := Check([]engine.Finding{f}, files(t), nil, defaultOpts)
	if len(res.Accepted) != 1 || res.Accepted[0].Line != 9 || res.Accepted[0].EndLine != 10 {
		t.Fatalf("got %+v / %+v", res.Accepted, res.Rejected)
	}
}

func TestCheckRejections(t *testing.T) {
	cases := map[string]func(*engine.Finding){
		"hallucinated evidence": func(f *engine.Finding) { f.Evidence = "db.Exec(userInput)" },
		"empty evidence":        func(f *engine.Finding) { f.Evidence = "  " },
		"unknown path":          func(f *engine.Finding) { f.Path = "other.go" },
		"low confidence":        func(f *engine.Finding) { f.Confidence = 0.2 },
		"outside diff": func(f *engine.Finding) {
			f.Line = 15
			f.Evidence = "return &u, nil"
		},
		"unchanged context line": func(f *engine.Finding) {
			f.Line = 11
			f.Evidence = "var u User"
		},
	}
	for name, mutate := range cases {
		f := sqlFinding()
		mutate(&f)
		res := Check([]engine.Finding{f}, files(t), nil, defaultOpts)
		if len(res.Accepted) != 0 || len(res.Rejected) != 1 {
			t.Errorf("%s: accepted=%d rejected=%d", name, len(res.Accepted), len(res.Rejected))
		}
	}
}

func TestCheckSeverityThreshold(t *testing.T) {
	f := sqlFinding()
	f.Severity = "low"
	res := Check([]engine.Finding{f}, files(t), nil, Options{MinSeverity: "medium"})
	if len(res.Accepted) != 0 {
		t.Fatal("low finding should be filtered at medium threshold")
	}
}

func TestCheckDedupe(t *testing.T) {
	f := sqlFinding()
	weaker := f
	weaker.Severity = "high"
	weaker.Title = "Unsafe query construction"
	res := Check([]engine.Finding{weaker, f, f}, files(t), nil, defaultOpts)
	if len(res.Accepted) != 1 || res.Accepted[0].Severity != "critical" {
		t.Fatalf("expected one critical finding, got %+v", res.Accepted)
	}

	existing := map[string]bool{Fingerprint(f): true}
	res = Check([]engine.Finding{f}, files(t), existing, defaultOpts)
	if len(res.Accepted) != 0 {
		t.Fatal("finding already posted on the PR should be dropped")
	}
}

func TestCheckRestrictsToIncrementalScope(t *testing.T) {
	fs := files(t)
	file := fs[testfixtures.Path]
	file.InScope = func(l int) bool { return l == 5 }
	fs[testfixtures.Path] = file
	res := Check([]engine.Finding{sqlFinding()}, fs, nil, defaultOpts)
	if len(res.Accepted) != 0 {
		t.Fatal("line 9 was reviewed previously and is out of scope")
	}
}

func TestCheckDropsRangeAcrossHunks(t *testing.T) {
	fd, _ := diff.Parse("f.txt", testfixtures.SecondHunkPatch)
	fs := map[string]File{"f.txt": {Diff: fd}}
	f := engine.Finding{Path: "f.txt", Line: 2, EndLine: 52, Severity: "high", Category: "bug", Title: "t", Evidence: "b", Suggestion: "bb", Confidence: 0.9}
	res := Check([]engine.Finding{f}, fs, nil, defaultOpts)
	if len(res.Accepted) != 1 {
		t.Fatalf("rejected: %+v", res.Rejected)
	}
	got := res.Accepted[0]
	if got.EndLine != 0 || got.Suggestion != "" {
		t.Fatalf("range across hunks should collapse to one line without suggestion: %+v", got)
	}
}

func TestCapAndOrdering(t *testing.T) {
	var in []Finding
	for i, sev := range []string{"low", "critical", "medium", "high"} {
		in = append(in, Finding{Finding: engine.Finding{Severity: sev, Line: i + 1}})
	}
	inline, overflow := Cap(in, 3)
	if len(inline) != 3 || len(overflow) != 1 {
		t.Fatalf("cap: %d/%d", len(inline), len(overflow))
	}
}

func TestExistingFingerprints(t *testing.T) {
	fp := Fingerprint(sqlFinding())
	got := ExistingFingerprints([]string{"text\n" + Marker(fp), "no marker"})
	if !got[fp] || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

type fakeVerifier struct{ unsupported map[string]bool }

func (f fakeVerifier) Verify(_ context.Context, _ string, items []engine.VerifyItem) (*engine.VerifyResponse, error) {
	var out engine.VerifyResponse
	for _, it := range items {
		out.Results = append(out.Results, engine.VerifyResult{ID: it.ID, Supported: !f.unsupported[it.Finding.Title]})
	}
	return &out, nil
}

func TestVerifyDropsUnsupportedHighSeverity(t *testing.T) {
	fs := files(t)
	keep := Finding{Finding: sqlFinding()}
	bad := Finding{Finding: sqlFinding()}
	bad.Title = "Imagined race"
	low := Finding{Finding: sqlFinding()}
	low.Severity, low.Title = "low", "Imagined race"

	kept, rejected, err := Verify(context.Background(), fakeVerifier{map[string]bool{"Imagined race": true}}, "o/r", []Finding{keep, bad, low}, fs, "high")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || len(rejected) != 1 || rejected[0].Finding.Title != "Imagined race" {
		t.Fatalf("kept=%d rejected=%+v", len(kept), rejected)
	}
}
