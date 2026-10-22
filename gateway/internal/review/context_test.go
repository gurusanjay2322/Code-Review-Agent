package review

import (
	"strings"
	"testing"

	"code-review-agent/gateway/internal/diff"
	"code-review-agent/gateway/internal/testfixtures"
)

func fixtureSource(t *testing.T, withContent bool) *SourceFile {
	t.Helper()
	fd, err := diff.Parse(testfixtures.Path, testfixtures.Patch)
	if err != nil {
		t.Fatal(err)
	}
	sf := &SourceFile{Path: testfixtures.Path, Language: "go", Diff: fd}
	if withContent {
		sf.Lines = strings.Split(testfixtures.Content, "\n")
	}
	return sf
}

func TestRenderMarksChangedLines(t *testing.T) {
	segs := Render(fixtureSource(t, true), 3)
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1 (window starts at line 1, no header)", len(segs))
	}
	text := segs[0].Text
	for _, want := range []string{
		"    9 +| \tq := fmt.Sprintf",
		"   10 +| \trow := db.QueryRow(q)",
		"   11  | \tvar u User",
		"      -| \trow := db.QueryRow(\"SELECT",
		"   16  | }",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("render missing %q\n%s", want, text)
		}
	}
	if strings.Index(text, "-| \trow := db.QueryRow(\"SELECT") > strings.Index(text, "    9 +|") {
		t.Error("removed line should appear before the line that replaced it")
	}
}

func TestRenderHeaderAndFocus(t *testing.T) {
	sf := fixtureSource(t, true)
	sf.Focus = map[int]bool{10: true}
	segs := Render(sf, 0)
	all := ""
	for _, s := range segs {
		all += s.Text
	}
	if !strings.Contains(all, "file header") {
		t.Errorf("expected a file header segment:\n%s", all)
	}
	if strings.Contains(all, "    9 +|") || !strings.Contains(all, "   10 +|") {
		t.Errorf("only line 10 is in focus:\n%s", all)
	}
}

func TestRenderDiffOnly(t *testing.T) {
	segs := Render(fixtureSource(t, false), 40)
	if len(segs) != 1 || !strings.Contains(segs[0].Text, "diff only") || !strings.Contains(segs[0].Text, "    9 +|") {
		t.Fatalf("diff-only render: %+v", segs)
	}
}

func TestBuildChunksBudget(t *testing.T) {
	mk := func(path string, tokens int) []Segment {
		return []Segment{{Path: path, Text: strings.Repeat("x\n", tokens*2), Tokens: tokens}}
	}
	res := BuildChunks([][]Segment{mk("a", 100), mk("b", 100), mk("c", 100)}, 250, 0)
	if len(res.Chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(res.Chunks))
	}
	if len(res.Chunks[0].Files) != 2 || res.Chunks[1].Files[0].Path != "c" {
		t.Fatalf("unexpected packing: %+v", res.Chunks)
	}

	res = BuildChunks([][]Segment{mk("a", 100), mk("b", 100), mk("c", 100)}, 1000, 250)
	if len(res.OverBudget) != 1 || res.OverBudget[0] != "c" {
		t.Fatalf("over budget = %v", res.OverBudget)
	}
}

func TestBuildChunksSplitsLargeSegment(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("line of code here\n")
	}
	seg := Segment{Path: "big.go", Text: b.String(), Tokens: EstimateTokens(b.String())}
	res := BuildChunks([][]Segment{{seg}}, 500, 0)
	if len(res.Chunks) < 2 {
		t.Fatalf("expected split, got %d chunk(s)", len(res.Chunks))
	}
	for _, c := range res.Chunks {
		if c.Tokens > 500 {
			t.Errorf("chunk %s has %d tokens", c.ID, c.Tokens)
		}
	}
}
