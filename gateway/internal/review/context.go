package review

import (
	"fmt"
	"path"
	"strings"

	"code-review-agent/gateway/internal/diff"
)

const headerLines = 25

// SourceFile is one changed file prepared for review.
type SourceFile struct {
	Path     string
	Language string
	Diff     *diff.FileDiff
	// Lines holds the file at the head commit; nil when the content could not be fetched,
	// in which case only the diff hunks are rendered.
	Lines []string
	// Focus restricts review to these new-file lines (incremental review). nil means all added lines.
	Focus map[int]bool
}

func (f *SourceFile) InFocus(line int) bool {
	if !f.Diff.IsAdded(line) {
		return false
	}
	return f.Focus == nil || f.Focus[line]
}

// Segment is a contiguous, numbered excerpt of one file.
type Segment struct {
	Path     string
	Language string
	Text     string
	Tokens   int
}

type window struct{ start, end int }

// Render produces numbered excerpts around each changed hunk. Changed lines under review are
// marked "+", removed lines are shown unnumbered with "-", so the model can cite new-file lines.
func Render(f *SourceFile, contextLines int) []Segment {
	if f.Lines == nil {
		return renderHunksOnly(f)
	}
	deletions := f.Diff.DeletionsBefore()
	n := len(f.Lines)

	var wins []window
	for _, h := range f.Diff.Hunks {
		if !hunkHasFocus(f, h) {
			continue
		}
		first, last := h.NewRange()
		if first == 0 {
			first, last = h.NewStart, h.NewStart
		}
		w := window{max(1, first-contextLines), min(n, last+contextLines)}
		if w.start > w.end {
			continue
		}
		if len(wins) > 0 && w.start <= wins[len(wins)-1].end+1 {
			wins[len(wins)-1].end = max(wins[len(wins)-1].end, w.end)
			continue
		}
		wins = append(wins, w)
	}
	if len(wins) == 0 {
		return nil
	}

	var segs []Segment
	if wins[0].start > 1 {
		end := min(headerLines, wins[0].start-1)
		var b strings.Builder
		fmt.Fprintf(&b, "-- lines 1-%d (file header) --\n", end)
		for ln := 1; ln <= end; ln++ {
			writeLine(&b, ln, " ", f.Lines[ln-1])
		}
		segs = append(segs, newSegment(f, b.String()))
	}
	for _, w := range wins {
		var b strings.Builder
		fmt.Fprintf(&b, "-- lines %d-%d --\n", w.start, w.end)
		for ln := w.start; ln <= w.end; ln++ {
			for _, d := range deletions[ln] {
				writeDeleted(&b, d)
			}
			marker := " "
			if f.InFocus(ln) {
				marker = "+"
			}
			writeLine(&b, ln, marker, f.Lines[ln-1])
		}
		if w.end == n {
			for _, d := range deletions[n+1] {
				writeDeleted(&b, d)
			}
		}
		segs = append(segs, newSegment(f, b.String()))
	}
	return segs
}

func renderHunksOnly(f *SourceFile) []Segment {
	var segs []Segment
	for _, h := range f.Diff.Hunks {
		if !hunkHasFocus(f, h) {
			continue
		}
		first, last := h.NewRange()
		var b strings.Builder
		fmt.Fprintf(&b, "-- lines %d-%d (diff only, full file unavailable) --\n", first, last)
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.Deleted:
				writeDeleted(&b, l.Text)
			default:
				marker := " "
				if f.InFocus(l.NewLine) {
					marker = "+"
				}
				writeLine(&b, l.NewLine, marker, l.Text)
			}
		}
		segs = append(segs, newSegment(f, b.String()))
	}
	return segs
}

func hunkHasFocus(f *SourceFile, h diff.Hunk) bool {
	for _, l := range h.Lines {
		if l.Kind == diff.Added && f.InFocus(l.NewLine) {
			return true
		}
	}
	return false
}

func writeLine(b *strings.Builder, ln int, marker, text string) {
	fmt.Fprintf(b, "%5d %s| %s\n", ln, marker, text)
}

func writeDeleted(b *strings.Builder, text string) {
	fmt.Fprintf(b, "%5s -| %s\n", "", text)
}

func newSegment(f *SourceFile, text string) Segment {
	return Segment{Path: f.Path, Language: f.Language, Text: text, Tokens: EstimateTokens(text)}
}

// EstimateTokens uses the common ~4 characters per token approximation.
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

var languages = map[string]string{
	".go": "go", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".py": "python", ".rb": "ruby", ".java": "java",
	".kt": "kotlin", ".kts": "kotlin", ".swift": "swift", ".rs": "rust", ".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp", ".php": "php", ".scala": "scala",
	".sql": "sql", ".sh": "shell", ".bash": "shell", ".ps1": "powershell", ".yml": "yaml",
	".yaml": "yaml", ".json": "json", ".tf": "terraform", ".vue": "vue", ".svelte": "svelte",
	".dart": "dart", ".ex": "elixir", ".exs": "elixir", ".lua": "lua", ".html": "html", ".css": "css",
	".scss": "scss", ".md": "markdown",
}

func DetectLanguage(p string) string {
	base := strings.ToLower(path.Base(p))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	}
	if lang, ok := languages[path.Ext(base)]; ok {
		return lang
	}
	return "text"
}
