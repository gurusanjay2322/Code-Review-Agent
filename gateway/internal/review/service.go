package review

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"code-review-agent/gateway/internal/config"
	"code-review-agent/gateway/internal/engine"
	"code-review-agent/gateway/internal/githubapp"
	"code-review-agent/gateway/internal/jobs"
	"code-review-agent/gateway/internal/publish"
	"code-review-agent/gateway/internal/validate"
)

const checkRunName = "AI code review"

type Engine interface {
	Review(ctx context.Context, req engine.ReviewRequest) (*engine.ReviewResponse, error)
	validate.Verifier
}

type Service struct {
	Cfg    *config.Config
	GitHub func(installationID int64) GitHub
	Engine Engine
	Log    *slog.Logger
}

type Summary struct {
	FilesReviewed int
	Inline        int
	Overflow      int
	Rejected      int
	Skipped       int
	Posted        bool
	// PostedSeverities lists the severity of every finding posted in this run (inline or summary).
	PostedSeverities []string
}

// HighestPostedRank returns the validate.SeverityRank of the most severe posted finding, or 0.
func (s *Summary) HighestPostedRank() int {
	if s == nil {
		return 0
	}
	best := 0
	for _, sev := range s.PostedSeverities {
		best = max(best, validate.SeverityRank(sev))
	}
	return best
}

func (s *Service) Run(ctx context.Context, job jobs.ReviewJob) (sum *Summary, err error) {
	log := s.Log.With("repo", job.FullName(), "pr", job.Number, "head", job.HeadSHA)
	gh := s.GitHub(job.InstallationID)

	var checkID int64
	if s.Cfg.EnableCheckRun {
		run, cerr := gh.CreateCheckRun(ctx, job.Owner, job.Repo, githubapp.CheckRun{Name: checkRunName, HeadSHA: job.HeadSHA, Status: "in_progress"})
		if cerr != nil {
			log.Warn("could not create check run", "err", cerr)
		} else {
			checkID = run.ID
		}
	}

	defer func() {
		if checkID == 0 {
			return
		}
		s.completeCheck(gh, job, checkID, sum, err, log)
	}()

	sum, err = s.review(ctx, gh, job, log)
	if err != nil {
		log.Error("review failed", "err", err)
		return nil, err
	}
	log.Info("review finished", "files", sum.FilesReviewed, "inline", sum.Inline, "overflow", sum.Overflow, "rejected", sum.Rejected, "posted", sum.Posted)
	return sum, nil
}

func (s *Service) review(ctx context.Context, gh GitHub, job jobs.ReviewJob, log *slog.Logger) (*Summary, error) {
	repoCfg := LoadRepoConfig(ctx, gh, job, log)
	if !repoCfg.IsEnabled() {
		log.Info("review disabled by repo config")
		return &Summary{}, nil
	}

	prep, err := Prepare(ctx, gh, job, PrepareOptions{MaxFiles: s.Cfg.MaxFiles, MaxPatchLines: s.Cfg.MaxPatchLines, Config: repoCfg}, log)
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}

	segs := make([][]Segment, 0, len(prep.Sources))
	for _, sf := range prep.Sources {
		segs = append(segs, Render(sf, s.Cfg.ContextLines))
	}
	chunks := BuildChunks(segs, s.Cfg.ChunkTokenBudget, s.Cfg.MaxTotalTokens)
	skipped := toPublishSkipped(prep.Skipped)
	for _, p := range chunks.OverBudget {
		skipped = append(skipped, publish.SkippedFile{Path: p, Reason: "per-PR token budget reached"})
	}

	reviewed := map[string]bool{}
	for _, c := range chunks.Chunks {
		for _, f := range c.Files {
			reviewed[f.Path] = true
		}
	}
	files := map[string]validate.File{}
	for _, sf := range prep.Sources {
		if reviewed[sf.Path] {
			files[sf.Path] = validate.File{Diff: sf.Diff, Lines: sf.Lines, InScope: sf.InFocus}
		}
	}

	sum := &Summary{FilesReviewed: len(files), Skipped: len(skipped)}
	in := publish.Input{
		Owner: job.Owner, Repo: job.Repo, Number: job.Number, HeadSHA: job.HeadSHA,
		Skipped: skipped, FilesReviewed: len(files), Incremental: prep.Incremental,
	}

	if len(chunks.Chunks) > 0 {
		resp, err := s.Engine.Review(ctx, engine.ReviewRequest{
			Repo: job.FullName(), PRTitle: job.Title, PRBody: job.Body,
			Instructions: repoCfg.ReviewInstructions(), Chunks: chunks.Chunks,
			MaxTotalTokens: s.Cfg.MaxTotalTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("engine review: %w", err)
		}
		if len(resp.Errors) == len(chunks.Chunks) {
			return nil, fmt.Errorf("engine failed every chunk: %s", resp.Errors[0].Error)
		}
		in.EngineErrors = len(resp.Errors) + len(resp.SkippedChunks)
		log.Info("engine returned", "findings", len(resp.Findings), "chunk_errors", len(resp.Errors), "usage_in", resp.Usage.InputTokens, "usage_out", resp.Usage.OutputTokens)

		existing := s.existingFingerprints(ctx, gh, job, log)
		opts := validate.Options{MinConfidence: s.Cfg.MinConfidence, MinSeverity: s.Cfg.MinSeverity}
		if repoCfg != nil {
			if repoCfg.MinConfidence > 0 {
				opts.MinConfidence = repoCfg.MinConfidence
			}
			if repoCfg.MinSeverity != "" {
				opts.MinSeverity = repoCfg.MinSeverity
			}
		}
		checked := validate.Check(resp.Findings, files, existing, opts)
		accepted, rejected := checked.Accepted, checked.Rejected

		if s.Cfg.VerifyFindings {
			kept, dropped, verr := validate.Verify(ctx, s.Engine, job.FullName(), accepted, files, "high")
			if verr != nil {
				log.Warn("verification failed, keeping findings", "err", verr)
			}
			accepted = kept
			rejected = append(rejected, dropped...)
		}
		for _, r := range rejected {
			log.Debug("finding rejected", "path", r.Finding.Path, "line", r.Finding.Line, "title", r.Finding.Title, "reason", r.Reason)
		}

		maxComments := s.Cfg.MaxComments
		if repoCfg != nil && repoCfg.MaxComments > 0 {
			maxComments = repoCfg.MaxComments
		}
		in.Inline, in.Overflow = validate.Cap(accepted, maxComments)
		in.Rejected = len(rejected)
		sum.Inline, sum.Overflow, sum.Rejected = len(in.Inline), len(in.Overflow), len(rejected)
	}

	// Follow-up pushes with nothing new stay quiet; the check run still records the result.
	if job.Action == "synchronize" && len(in.Inline)+len(in.Overflow) == 0 {
		return sum, nil
	}
	if len(files) == 0 && len(skipped) == 0 {
		return sum, nil
	}
	out, err := publish.Publish(ctx, gh, in)
	if err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}
	sum.Posted = true
	sum.Inline = out.InlinePosted
	sum.Overflow += len(out.Unanchored)
	for _, f := range append(append([]validate.Finding{}, in.Inline...), in.Overflow...) {
		sum.PostedSeverities = append(sum.PostedSeverities, f.Severity)
	}
	return sum, nil
}

func (s *Service) existingFingerprints(ctx context.Context, gh GitHub, job jobs.ReviewJob, log *slog.Logger) map[string]bool {
	var bodies []string
	comments, err := gh.ListReviewComments(ctx, job.Owner, job.Repo, job.Number)
	if err != nil {
		log.Warn("could not list review comments for dedupe", "err", err)
	}
	for _, c := range comments {
		bodies = append(bodies, c.Body)
	}
	reviews, err := gh.ListReviews(ctx, job.Owner, job.Repo, job.Number)
	if err != nil {
		log.Warn("could not list reviews for dedupe", "err", err)
	}
	for _, r := range reviews {
		bodies = append(bodies, r.Body)
	}
	return validate.ExistingFingerprints(bodies)
}

func (s *Service) completeCheck(gh GitHub, job jobs.ReviewJob, id int64, sum *Summary, runErr error, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	run := githubapp.CheckRun{Status: "completed", CompletedAt: &now, Conclusion: "success"}
	switch {
	case runErr != nil:
		// Neutral so an outage in the reviewer never blocks merging.
		run.Conclusion = "neutral"
		run.Output = &githubapp.CheckRunOutput{Title: "Review could not be completed", Summary: runErr.Error()}
	case sum == nil || sum.FilesReviewed == 0:
		run.Conclusion = "skipped"
		run.Output = &githubapp.CheckRunOutput{Title: "Nothing to review", Summary: "No reviewable changes."}
	default:
		total := sum.Inline + sum.Overflow
		if total > 0 {
			run.Conclusion = "neutral"
		}
		run.Output = &githubapp.CheckRunOutput{
			Title:   fmt.Sprintf("%d issue(s) found", total),
			Summary: fmt.Sprintf("Reviewed %d file(s). %d inline comment(s), %d in summary, %d filtered out, %d file(s) skipped.", sum.FilesReviewed, sum.Inline, sum.Overflow, sum.Rejected, sum.Skipped),
		}
	}
	if err := gh.UpdateCheckRun(ctx, job.Owner, job.Repo, id, run); err != nil {
		log.Warn("could not complete check run", "err", err)
	}
}

func toPublishSkipped(in []Skipped) []publish.SkippedFile {
	out := make([]publish.SkippedFile, len(in))
	for i, s := range in {
		out[i] = publish.SkippedFile{Path: s.Path, Reason: s.Reason}
	}
	return out
}
