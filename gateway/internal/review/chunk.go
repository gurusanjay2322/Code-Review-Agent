package review

import (
	"fmt"
	"strings"

	"code-review-agent/gateway/internal/engine"
)

// fileOverheadTokens approximates the per-file heading the engine adds to the prompt.
const fileOverheadTokens = 20

type ChunkResult struct {
	Chunks []engine.Chunk
	// OverBudget lists files dropped because the per-PR token cap was reached.
	OverBudget []string
}

// BuildChunks packs segments into chunks of at most budget tokens, keeping a file's segments
// together where possible, and stops adding files once maxTotal tokens are used.
func BuildChunks(files [][]Segment, budget, maxTotal int) ChunkResult {
	var res ChunkResult
	var cur *engine.Chunk
	total := 0

	flush := func() {
		if cur != nil && len(cur.Files) > 0 {
			res.Chunks = append(res.Chunks, *cur)
		}
		cur = nil
	}
	add := func(s Segment) {
		cost := s.Tokens + fileOverheadTokens
		if cur != nil && cur.Tokens+cost > budget {
			flush()
		}
		if cur == nil {
			cur = &engine.Chunk{ID: fmt.Sprintf("chunk-%d", len(res.Chunks)+1)}
		}
		if n := len(cur.Files); n > 0 && cur.Files[n-1].Path == s.Path {
			cur.Files[n-1].Content += s.Text
			cur.Tokens += s.Tokens
		} else {
			cur.Files = append(cur.Files, engine.ChunkFile{Path: s.Path, Language: s.Language, Content: s.Text})
			cur.Tokens += cost
		}
	}

	for _, segs := range files {
		if len(segs) == 0 {
			continue
		}
		fileTokens := 0
		for _, s := range segs {
			fileTokens += s.Tokens + fileOverheadTokens
		}
		if maxTotal > 0 && total+fileTokens > maxTotal {
			res.OverBudget = append(res.OverBudget, segs[0].Path)
			continue
		}
		total += fileTokens
		for _, s := range segs {
			for _, part := range splitSegment(s, budget-fileOverheadTokens) {
				add(part)
			}
		}
	}
	flush()
	return res
}

// splitSegment breaks a segment larger than limit tokens on line boundaries.
func splitSegment(s Segment, limit int) []Segment {
	if s.Tokens <= limit || limit <= 0 {
		return []Segment{s}
	}
	var out []Segment
	var b strings.Builder
	for _, line := range strings.SplitAfter(s.Text, "\n") {
		if line == "" {
			continue
		}
		if b.Len() > 0 && EstimateTokens(b.String()+line) > limit {
			out = append(out, Segment{Path: s.Path, Language: s.Language, Text: b.String(), Tokens: EstimateTokens(b.String())})
			b.Reset()
			b.WriteString("-- (continued) --\n")
		}
		b.WriteString(line)
	}
	if b.Len() > 0 {
		out = append(out, Segment{Path: s.Path, Language: s.Language, Text: b.String(), Tokens: EstimateTokens(b.String())})
	}
	return out
}
