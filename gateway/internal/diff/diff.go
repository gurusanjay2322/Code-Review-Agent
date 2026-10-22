package diff

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Kind int

const (
	Context Kind = iota
	Added
	Deleted
)

type Line struct {
	Kind    Kind
	OldLine int
	NewLine int // 0 for deleted lines
	Text    string
}

type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []Line
}

// NewRange returns the first and last new-file line numbers the hunk covers on the RIGHT side.
func (h Hunk) NewRange() (int, int) {
	first, last := 0, 0
	for _, l := range h.Lines {
		if l.Kind == Deleted {
			continue
		}
		if first == 0 {
			first = l.NewLine
		}
		last = l.NewLine
	}
	return first, last
}

type FileDiff struct {
	Path  string
	Hunks []Hunk

	rightHunk map[int]int
	added     map[int]bool
	text      map[int]string
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Parse reads the `patch` field GitHub returns for a PR file: unified diff hunks without file headers.
func Parse(path, patch string) (*FileDiff, error) {
	fd := &FileDiff{Path: path, rightHunk: map[int]int{}, added: map[int]bool{}, text: map[int]string{}}
	if strings.TrimSpace(patch) == "" {
		return fd, nil
	}
	var cur *Hunk
	oldLn, newLn := 0, 0
	for _, raw := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(raw, "@@") {
			m := hunkHeader.FindStringSubmatch(raw)
			if m == nil {
				return nil, fmt.Errorf("%s: malformed hunk header %q", path, raw)
			}
			fd.Hunks = append(fd.Hunks, Hunk{
				OldStart: atoi(m[1], 0), OldLines: atoi(m[2], 1),
				NewStart: atoi(m[3], 0), NewLines: atoi(m[4], 1),
			})
			cur = &fd.Hunks[len(fd.Hunks)-1]
			oldLn, newLn = cur.OldStart, cur.NewStart
			continue
		}
		if cur == nil || strings.HasPrefix(raw, `\`) {
			continue
		}
		idx := len(fd.Hunks) - 1
		switch {
		case strings.HasPrefix(raw, "+"):
			cur.Lines = append(cur.Lines, Line{Kind: Added, NewLine: newLn, Text: raw[1:]})
			fd.rightHunk[newLn] = idx
			fd.added[newLn] = true
			fd.text[newLn] = raw[1:]
			newLn++
		case strings.HasPrefix(raw, "-"):
			cur.Lines = append(cur.Lines, Line{Kind: Deleted, OldLine: oldLn, Text: raw[1:]})
			oldLn++
		default:
			text := strings.TrimPrefix(raw, " ")
			// A trailing empty string from the final newline is not a context line.
			if raw == "" && newLn >= cur.NewStart+cur.NewLines {
				continue
			}
			cur.Lines = append(cur.Lines, Line{Kind: Context, OldLine: oldLn, NewLine: newLn, Text: text})
			fd.rightHunk[newLn] = idx
			fd.text[newLn] = text
			oldLn++
			newLn++
		}
	}
	return fd, nil
}

// Commentable reports whether GitHub accepts an inline comment on this RIGHT-side line.
func (f *FileDiff) Commentable(line int) bool {
	_, ok := f.rightHunk[line]
	return ok
}

func (f *FileDiff) SameHunk(a, b int) bool {
	ha, okA := f.rightHunk[a]
	hb, okB := f.rightHunk[b]
	return okA && okB && ha == hb
}

func (f *FileDiff) IsAdded(line int) bool { return f.added[line] }

func (f *FileDiff) LineText(line int) (string, bool) {
	t, ok := f.text[line]
	return t, ok
}

func (f *FileDiff) AddedLines() []int {
	out := make([]int, 0, len(f.added))
	for l := range f.added {
		out = append(out, l)
	}
	sort.Ints(out)
	return out
}

func (f *FileDiff) PatchLineCount() int {
	n := 0
	for _, h := range f.Hunks {
		n += len(h.Lines)
	}
	return n
}

// DeletionsBefore groups removed lines by the new-file line they precede, so they can be
// shown inline when rendering the new file. Deletions at the end of a hunk key to the next line.
func (f *FileDiff) DeletionsBefore() map[int][]string {
	out := map[int][]string{}
	for _, h := range f.Hunks {
		next := h.NewStart
		var pending []string
		for _, l := range h.Lines {
			switch l.Kind {
			case Deleted:
				pending = append(pending, l.Text)
			default:
				if len(pending) > 0 {
					out[l.NewLine] = append(out[l.NewLine], pending...)
					pending = nil
				}
				next = l.NewLine + 1
			}
		}
		if len(pending) > 0 {
			out[next] = append(out[next], pending...)
		}
	}
	return out
}

func atoi(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
