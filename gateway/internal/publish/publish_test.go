package publish

import (
	"strings"
	"testing"

	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/validate"
)

func finding() validate.Finding {
	return validate.Finding{
		Finding: engine.Finding{
			Path: "a.go", Line: 9, EndLine: 10, Severity: "high", Category: "bug",
			Title: "Nil dereference", Explanation: "x may be nil.", Suggestion: "if x != nil {\n\tx.Run()\n}", Confidence: 0.8,
		},
		Fingerprint: "0123456789abcdef",
	}
}

func TestDraftCommentRange(t *testing.T) {
	c := DraftComment(finding())
	if c.StartLine != 9 || c.Line != 10 || c.Side != "RIGHT" || c.StartSide != "RIGHT" {
		t.Fatalf("range comment: %+v", c)
	}
	single := finding()
	single.EndLine = 0
	if c := DraftComment(single); c.StartLine != 0 || c.Line != 9 {
		t.Fatalf("single-line comment: %+v", c)
	}
}

func TestCommentBody(t *testing.T) {
	body := CommentBody(finding())
	for _, want := range []string{"**[High] Bug:** Nil dereference", "```suggestion\nif x != nil {", "Confidence: 80%", validate.Marker("0123456789abcdef")} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	f := finding()
	f.Suggestion = "s := \"```\""
	if !strings.Contains(CommentBody(f), "````suggestion") {
		t.Error("suggestions containing backticks need a longer fence")
	}
}

func TestSummary(t *testing.T) {
	over := finding()
	over.Severity = "low"
	s := Summary(Input{
		FilesReviewed: 3, Incremental: true, Inline: []validate.Finding{finding()}, Overflow: []validate.Finding{over},
		Skipped: []SkippedFile{{"go.sum", "lockfile"}}, Rejected: 2,
	}, nil)
	for _, want := range []string{"Reviewed 3 file(s) (only changes since the last review)", "2 issue(s): 1 high, 1 low", "Additional findings", "`a.go:9-10`", "`go.sum`: lockfile", "2 low-confidence"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q:\n%s", want, s)
		}
	}
}
