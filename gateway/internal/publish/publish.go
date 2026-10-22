package publish

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/validate"
)

// GitHub caps review and comment bodies at 65536 characters.
const maxBodyChars = 60000

type Poster interface {
	CreateReview(ctx context.Context, owner, repo string, number int, req githubapp.CreateReviewRequest) error
	CreateReviewComment(ctx context.Context, owner, repo string, number int, commitID string, c githubapp.DraftComment) error
}

type SkippedFile struct {
	Path   string
	Reason string
}

type Input struct {
	Owner, Repo   string
	Number        int
	HeadSHA       string
	Inline        []validate.Finding
	Overflow      []validate.Finding
	Skipped       []SkippedFile
	FilesReviewed int
	Rejected      int
	EngineErrors  int
	Incremental   bool
}

type Outcome struct {
	InlinePosted int
	// Unanchored are inline findings GitHub refused; they are moved into the summary.
	Unanchored []validate.Finding
}

// Publish posts one review with a summary body and inline comments. If GitHub rejects the batch
// (422, usually a line it will not anchor), comments are posted one by one and the rejected ones
// are listed in the summary instead.
func Publish(ctx context.Context, gh Poster, in Input) (*Outcome, error) {
	comments := make([]githubapp.DraftComment, len(in.Inline))
	for i, f := range in.Inline {
		comments[i] = DraftComment(f)
	}

	req := githubapp.CreateReviewRequest{CommitID: in.HeadSHA, Event: "COMMENT", Body: Summary(in, nil), Comments: comments}
	err := gh.CreateReview(ctx, in.Owner, in.Repo, in.Number, req)
	if err == nil {
		return &Outcome{InlinePosted: len(comments)}, nil
	}
	if githubapp.StatusCode(err) != http.StatusUnprocessableEntity || len(comments) == 0 {
		return nil, err
	}

	out := &Outcome{}
	for i, c := range comments {
		if err := gh.CreateReviewComment(ctx, in.Owner, in.Repo, in.Number, in.HeadSHA, c); err != nil {
			if githubapp.StatusCode(err) != http.StatusUnprocessableEntity {
				return nil, err
			}
			out.Unanchored = append(out.Unanchored, in.Inline[i])
			continue
		}
		out.InlinePosted++
	}
	req = githubapp.CreateReviewRequest{CommitID: in.HeadSHA, Event: "COMMENT", Body: Summary(in, out.Unanchored)}
	if err := gh.CreateReview(ctx, in.Owner, in.Repo, in.Number, req); err != nil {
		return nil, err
	}
	return out, nil
}

func DraftComment(f validate.Finding) githubapp.DraftComment {
	c := githubapp.DraftComment{Path: f.Path, Line: f.Line, Side: "RIGHT", Body: CommentBody(f)}
	if f.EndLine > f.Line {
		c.StartLine = f.Line
		c.StartSide = "RIGHT"
		c.Line = f.EndLine
	}
	return c
}

func CommentBody(f validate.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**[%s] %s:** %s\n\n%s\n", label(f.Severity), label(f.Category), f.Title, f.Explanation)
	if f.Suggestion != "" {
		fence := "```"
		if strings.Contains(f.Suggestion, "```") {
			fence = "````"
		}
		fmt.Fprintf(&b, "\n%ssuggestion\n%s\n%s\n", fence, f.Suggestion, fence)
	}
	fmt.Fprintf(&b, "\n<sub>Confidence: %.0f%%</sub>\n%s", f.Confidence*100, validate.Marker(f.Fingerprint))
	return truncate(b.String())
}

func Summary(in Input, unanchored []validate.Finding) string {
	var b strings.Builder
	b.WriteString("## AI code review\n\n")

	scope := fmt.Sprintf("Reviewed %d file(s)", in.FilesReviewed)
	if in.Incremental {
		scope += " (only changes since the last review)"
	}
	total := len(in.Inline) + len(in.Overflow)
	if total == 0 {
		fmt.Fprintf(&b, "%s. No issues found.\n", scope)
	} else {
		fmt.Fprintf(&b, "%s and found %d issue(s): %s.\n", scope, total, severityCounts(append(append([]validate.Finding{}, in.Inline...), in.Overflow...)))
	}
	if in.Rejected > 0 {
		fmt.Fprintf(&b, "\n%d low-confidence or unverifiable finding(s) were filtered out.\n", in.Rejected)
	}
	if in.EngineErrors > 0 {
		fmt.Fprintf(&b, "\nNote: %d part(s) of the diff could not be reviewed because of model errors.\n", in.EngineErrors)
	}

	extra := append(append([]validate.Finding{}, unanchored...), in.Overflow...)
	if len(extra) > 0 {
		b.WriteString("\n### Additional findings\n\n")
		for _, f := range extra {
			loc := fmt.Sprintf("%s:%d", f.Path, f.Line)
			if f.EndLine > f.Line {
				loc += fmt.Sprintf("-%d", f.EndLine)
			}
			fmt.Fprintf(&b, "- **[%s] %s** `%s`: %s. %s %s\n", label(f.Severity), label(f.Category), loc, f.Title, oneLine(f.Explanation), validate.Marker(f.Fingerprint))
		}
	}

	if len(in.Skipped) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>%d file(s) not reviewed</summary>\n\n", len(in.Skipped))
		for _, s := range in.Skipped {
			fmt.Fprintf(&b, "- `%s`: %s\n", s.Path, s.Reason)
		}
		b.WriteString("\n</details>\n")
	}
	return truncate(b.String())
}

var severityOrder = []string{"critical", "high", "medium", "low"}

func severityCounts(fs []validate.Finding) string {
	counts := map[string]int{}
	for _, f := range fs {
		counts[strings.ToLower(f.Severity)]++
	}
	var parts []string
	for _, s := range severityOrder {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return strings.Join(parts, ", ")
}

func label(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func truncate(s string) string {
	if len(s) <= maxBodyChars {
		return s
	}
	return s[:maxBodyChars] + "\n\n(truncated)"
}
