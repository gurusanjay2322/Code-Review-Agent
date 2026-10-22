package validate

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"code-review-agent/gateway/internal/diff"
	"code-review-agent/gateway/internal/engine"
)

var severityRank = map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}

func SeverityRank(s string) int { return severityRank[strings.ToLower(s)] }

// relocateWindow is how far (in lines) a finding may be moved to where its evidence actually is.
const relocateWindow = 5

// File is what the validator knows about one reviewed file.
type File struct {
	Diff *diff.FileDiff
	// Lines is the head version of the file, or nil when only the diff is available.
	Lines []string
	// InScope reports whether a line is part of what was reviewed (added lines, or the
	// incremental subset). nil means every added line.
	InScope func(line int) bool
}

func (f File) text(line int) (string, bool) {
	if f.Lines != nil {
		if line < 1 || line > len(f.Lines) {
			return "", false
		}
		return f.Lines[line-1], true
	}
	return f.Diff.LineText(line)
}

func (f File) inScope(line int) bool {
	if f.InScope != nil {
		return f.InScope(line)
	}
	return f.Diff.IsAdded(line)
}

type Finding struct {
	engine.Finding
	Fingerprint string
}

type Rejection struct {
	Finding engine.Finding
	Reason  string
}

type Options struct {
	MinConfidence float64
	MinSeverity   string
}

type Result struct {
	Accepted []Finding
	Rejected []Rejection
}

// Check applies every deterministic rule: known path, thresholds, evidence present in the code,
// line inside a RIGHT-side hunk, changed-code scope, and fingerprint dedupe.
// Accepted findings are sorted most severe first.
func Check(findings []engine.Finding, files map[string]File, existing map[string]bool, opts Options) Result {
	var res Result
	seen := map[string]int{}
	minRank := SeverityRank(opts.MinSeverity)

	for _, f := range findings {
		reject := func(reason string) { res.Rejected = append(res.Rejected, Rejection{f, reason}) }

		file, ok := files[f.Path]
		if !ok {
			reject("path not among reviewed files")
			continue
		}
		rank := SeverityRank(f.Severity)
		if rank == 0 {
			reject("unknown severity")
			continue
		}
		if rank < minRank {
			reject("below severity threshold")
			continue
		}
		if f.Confidence < opts.MinConfidence {
			reject("below confidence threshold")
			continue
		}

		start, end, ok := locateEvidence(file, f)
		if !ok {
			reject("evidence not found near cited line")
			continue
		}
		span := 0
		if f.EndLine > f.Line {
			span = f.EndLine - f.Line
		}
		if end == start {
			end = start + span
		}
		f.Line = start
		f.EndLine = 0
		if end > start {
			f.EndLine = end
		}

		if !file.Diff.Commentable(f.Line) {
			reject("line is outside the diff")
			continue
		}
		if f.EndLine > 0 && !(file.Diff.Commentable(f.EndLine) && file.Diff.SameHunk(f.Line, f.EndLine)) {
			f.EndLine = 0
			f.Suggestion = ""
		}
		if !anyInScope(file, f.Line, max(f.Line, f.EndLine)) {
			reject("not on a changed line")
			continue
		}
		if f.Suggestion != "" && normalize(f.Suggestion) == normalize(f.Evidence) {
			f.Suggestion = ""
		}

		fp := Fingerprint(f)
		if existing[fp] {
			reject("already reported on this pull request")
			continue
		}
		key := fmt.Sprintf("%s:%d:%s", f.Path, f.Line, f.Category)
		if _, dup := seen[fp]; dup {
			res.Rejected = append(res.Rejected, Rejection{f, "duplicate finding"})
			continue
		}
		if idx, dup := seen[key]; dup {
			prev := res.Accepted[idx]
			if better(f, prev.Finding) {
				res.Rejected = append(res.Rejected, Rejection{prev.Finding, "duplicate finding"})
				res.Accepted[idx] = Finding{f, fp}
				seen[fp] = idx
			} else {
				res.Rejected = append(res.Rejected, Rejection{f, "duplicate finding"})
			}
			continue
		}
		seen[fp] = len(res.Accepted)
		seen[key] = len(res.Accepted)
		res.Accepted = append(res.Accepted, Finding{f, fp})
	}

	sort.SliceStable(res.Accepted, func(i, j int) bool { return better(res.Accepted[i].Finding, res.Accepted[j].Finding) })
	return res
}

// Cap splits findings into those posted inline and the overflow listed in the summary.
func Cap(findings []Finding, maxInline int) (inline, overflow []Finding) {
	if maxInline <= 0 || len(findings) <= maxInline {
		return findings, nil
	}
	return findings[:maxInline], findings[maxInline:]
}

func better(a, b engine.Finding) bool {
	ra, rb := SeverityRank(a.Severity), SeverityRank(b.Severity)
	if ra != rb {
		return ra > rb
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.Line < b.Line
}

func anyInScope(f File, from, to int) bool {
	for l := from; l <= to; l++ {
		if f.inScope(l) {
			return true
		}
	}
	return false
}

// locateEvidence finds where the quoted evidence actually is, starting at the cited line and
// searching outward. It returns the first and last matched lines.
func locateEvidence(f File, finding engine.Finding) (int, int, bool) {
	ev := evidenceLines(finding.Evidence)
	if len(ev) == 0 {
		return 0, 0, false
	}
	for _, off := range searchOrder(relocateWindow) {
		start := finding.Line + off
		if end, ok := matchAt(f, start, ev); ok {
			return start, end, true
		}
	}
	return 0, 0, false
}

func searchOrder(n int) []int {
	out := []int{0}
	for i := 1; i <= n; i++ {
		out = append(out, -i, i)
	}
	return out
}

// matchAt checks that evidence lines appear in order starting at line, skipping blank file lines.
func matchAt(f File, line int, ev []string) (int, bool) {
	cur := line
	for i, want := range ev {
		for {
			got, ok := f.text(cur)
			if !ok {
				return 0, false
			}
			g := normalize(got)
			if g == "" && i > 0 {
				cur++
				continue
			}
			if !lineMatches(g, want) {
				return 0, false
			}
			break
		}
		if i < len(ev)-1 {
			cur++
		}
	}
	return cur, true
}

func lineMatches(got, want string) bool {
	if got == want {
		return true
	}
	// Models often quote a fragment of a long line.
	return len(want) >= 8 && strings.Contains(got, want)
}

func evidenceLines(ev string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(ev, "\r\n", "\n"), "\n") {
		if n := normalize(l); n != "" && n != "..." {
			out = append(out, n)
		}
	}
	return out
}

var spaceRun = regexp.MustCompile(`\s+`)

func normalize(s string) string {
	return strings.TrimSpace(spaceRun.ReplaceAllString(s, " "))
}

var titleNoise = regexp.MustCompile(`[^a-z0-9 ]+`)

// Fingerprint identifies a finding across runs: path, line, category and normalized title.
func Fingerprint(f engine.Finding) string {
	title := normalize(titleNoise.ReplaceAllString(strings.ToLower(f.Title), " "))
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%s|%s", f.Path, f.Line, strings.ToLower(f.Category), title)))
	return hex.EncodeToString(sum[:])[:16]
}

var markerRe = regexp.MustCompile(`<!-- ai-review:fp=([0-9a-f]{16}) -->`)

func Marker(fp string) string { return "<!-- ai-review:fp=" + fp + " -->" }

// ExistingFingerprints extracts markers from comments the bot already posted.
func ExistingFingerprints(bodies []string) map[string]bool {
	out := map[string]bool{}
	for _, b := range bodies {
		for _, m := range markerRe.FindAllStringSubmatch(b, -1) {
			out[m[1]] = true
		}
	}
	return out
}
