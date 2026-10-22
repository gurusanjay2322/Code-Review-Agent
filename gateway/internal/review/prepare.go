package review

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"code-review-agent/gateway/internal/diff"
	"code-review-agent/gateway/internal/filter"
	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/jobs"
	"code-review-agent/gateway/internal/repoconfig"
)

// GitHub is the subset of the REST client the review pipeline needs.
type GitHub interface {
	ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]githubapp.File, error)
	Compare(ctx context.Context, owner, repo, base, head string) (*githubapp.Comparison, error)
	GetRawContent(ctx context.Context, owner, repo, path, ref string) ([]byte, bool, error)
	ListReviewComments(ctx context.Context, owner, repo string, number int) ([]githubapp.ReviewComment, error)
	ListReviews(ctx context.Context, owner, repo string, number int) ([]githubapp.Review, error)
	CreateReview(ctx context.Context, owner, repo string, number int, req githubapp.CreateReviewRequest) error
	CreateReviewComment(ctx context.Context, owner, repo string, number int, commitID string, c githubapp.DraftComment) error
	CreateCheckRun(ctx context.Context, owner, repo string, run githubapp.CheckRun) (*githubapp.CheckRun, error)
	UpdateCheckRun(ctx context.Context, owner, repo string, id int64, run githubapp.CheckRun) error
}

type Skipped struct {
	Path   string
	Reason string
}

type Prepared struct {
	Sources     []*SourceFile
	Skipped     []Skipped
	Incremental bool
	// Diffs covers every file in the PR diff (not just reviewed ones); used for line validation.
	Diffs map[string]*diff.FileDiff
}

type PrepareOptions struct {
	MaxFiles      int
	MaxPatchLines int
	Config        *repoconfig.Config
}

const contentFetchConcurrency = 8

func Prepare(ctx context.Context, gh GitHub, job jobs.ReviewJob, opts PrepareOptions, log *slog.Logger) (*Prepared, error) {
	files, err := gh.ListPullRequestFiles(ctx, job.Owner, job.Repo, job.Number)
	if err != nil {
		return nil, err
	}
	focus := incrementalFocus(ctx, gh, job, log)
	p := &Prepared{Incremental: focus != nil, Diffs: map[string]*diff.FileDiff{}}

	var candidates []*SourceFile
	for _, f := range files {
		fd, err := diff.Parse(f.Filename, f.Patch)
		if err != nil {
			p.Skipped = append(p.Skipped, Skipped{f.Filename, "unparseable diff"})
			continue
		}
		p.Diffs[f.Filename] = fd

		var fileFocus map[int]bool
		if focus != nil {
			lines, changed := focus[f.Filename]
			if !changed {
				continue
			}
			fileFocus = lines
		}

		reason := filter.ByStatus(f.Status, f.Patch, fd.PatchLineCount(), opts.MaxPatchLines)
		if reason == "" {
			reason = filter.ByPath(f.Filename)
		}
		if reason == "" {
			reason = opts.Config.Excluded(f.Filename)
		}
		if reason == "" && len(fd.AddedLines()) == 0 {
			reason = "no added lines"
		}
		if reason != "" {
			p.Skipped = append(p.Skipped, Skipped{f.Filename, reason})
			continue
		}
		if opts.MaxFiles > 0 && len(candidates) >= opts.MaxFiles {
			p.Skipped = append(p.Skipped, Skipped{f.Filename, "file limit reached"})
			continue
		}
		candidates = append(candidates, &SourceFile{
			Path:     f.Filename,
			Language: DetectLanguage(f.Filename),
			Diff:     fd,
			Focus:    fileFocus,
		})
	}

	type fetched struct {
		reason string
	}
	results := make([]fetched, len(candidates))
	sem := make(chan struct{}, contentFetchConcurrency)
	var wg sync.WaitGroup
	for i, sf := range candidates {
		wg.Add(1)
		go func(i int, sf *SourceFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			content, found, err := gh.GetRawContent(ctx, job.Owner, job.Repo, sf.Path, job.HeadSHA)
			if err != nil || !found {
				log.Warn("content unavailable, reviewing diff only", "path", sf.Path, "err", err)
				return
			}
			if reason := filter.ByContent(content); reason != "" {
				results[i].reason = reason
				return
			}
			sf.Lines = strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
		}(i, sf)
	}
	wg.Wait()

	for i, sf := range candidates {
		if results[i].reason != "" {
			p.Skipped = append(p.Skipped, Skipped{sf.Path, results[i].reason})
			continue
		}
		p.Sources = append(p.Sources, sf)
	}
	return p, nil
}

// incrementalFocus returns the added lines per file between the previously reviewed head and the
// new head, or nil when a full review is needed (first review, force push, compare failure).
func incrementalFocus(ctx context.Context, gh GitHub, job jobs.ReviewJob, log *slog.Logger) map[string]map[int]bool {
	if job.BeforeSHA == "" || strings.Trim(job.BeforeSHA, "0") == "" {
		return nil
	}
	cmp, err := gh.Compare(ctx, job.Owner, job.Repo, job.BeforeSHA, job.HeadSHA)
	if err != nil {
		log.Warn("compare failed, falling back to full review", "err", err)
		return nil
	}
	if cmp.Status != "ahead" {
		log.Info("history rewritten, full review", "status", cmp.Status)
		return nil
	}
	// The compare API caps files at 300; beyond that the incremental view is incomplete.
	if len(cmp.Files) >= 300 {
		return nil
	}
	out := map[string]map[int]bool{}
	for _, f := range cmp.Files {
		fd, err := diff.Parse(f.Filename, f.Patch)
		if err != nil {
			return nil
		}
		lines := map[int]bool{}
		for _, l := range fd.AddedLines() {
			lines[l] = true
		}
		out[f.Filename] = lines
	}
	return out
}

func LoadRepoConfig(ctx context.Context, gh GitHub, job jobs.ReviewJob, log *slog.Logger) *repoconfig.Config {
	ref := job.BaseSHA
	if ref == "" {
		ref = job.HeadSHA
	}
	data, found, err := gh.GetRawContent(ctx, job.Owner, job.Repo, repoconfig.Path, ref)
	if err != nil {
		log.Warn("could not read repo config", "err", err)
		return nil
	}
	if !found {
		return nil
	}
	cfg, err := repoconfig.Parse(data)
	if err != nil {
		log.Warn("ignoring invalid repo config", "err", err)
		return nil
	}
	return cfg
}
