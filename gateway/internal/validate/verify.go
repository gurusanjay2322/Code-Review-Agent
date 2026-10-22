package validate

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"code-review-agent/gateway/internal/engine"
)

const verifyContextLines = 15

type Verifier interface {
	Verify(ctx context.Context, repo string, items []engine.VerifyItem) (*engine.VerifyResponse, error)
}

// Verify sends findings at or above minSeverity for a second LLM opinion and drops the ones it
// judges unsupported. If the verifier fails, all findings are kept.
func Verify(ctx context.Context, v Verifier, repo string, findings []Finding, files map[string]File, minSeverity string) ([]Finding, []Rejection, error) {
	minRank := SeverityRank(minSeverity)
	var items []engine.VerifyItem
	for i, f := range findings {
		if SeverityRank(f.Severity) < minRank {
			continue
		}
		items = append(items, engine.VerifyItem{
			ID:      strconv.Itoa(i),
			Finding: f.Finding,
			Context: excerpt(files[f.Path], f.Line, max(f.Line, f.EndLine)),
		})
	}
	if len(items) == 0 {
		return findings, nil, nil
	}
	resp, err := v.Verify(ctx, repo, items)
	if err != nil {
		return findings, nil, err
	}
	unsupported := map[int]string{}
	for _, r := range resp.Results {
		if r.Supported {
			continue
		}
		if i, err := strconv.Atoi(r.ID); err == nil {
			unsupported[i] = r.Reason
		}
	}
	var kept []Finding
	var rejected []Rejection
	for i, f := range findings {
		if reason, bad := unsupported[i]; bad {
			rejected = append(rejected, Rejection{f.Finding, "verification: " + reason})
			continue
		}
		kept = append(kept, f)
	}
	return kept, rejected, nil
}

func excerpt(f File, from, to int) string {
	var b strings.Builder
	for l := max(1, from-verifyContextLines); l <= to+verifyContextLines; l++ {
		text, ok := f.text(l)
		if !ok {
			if f.Lines != nil {
				break
			}
			continue
		}
		marker := " "
		if l >= from && l <= to {
			marker = ">"
		}
		fmt.Fprintf(&b, "%5d %s| %s\n", l, marker, text)
	}
	return b.String()
}
