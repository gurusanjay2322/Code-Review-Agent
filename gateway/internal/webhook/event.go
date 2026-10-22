package webhook

import (
	"encoding/json"
	"fmt"

	"code-review-agent/gateway/internal/jobs"
)

var reviewableActions = map[string]bool{
	"opened":           true,
	"synchronize":      true,
	"reopened":         true,
	"ready_for_review": true,
}

type pullRequestEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	Before      string `json:"before"`
	After       string `json:"after"`
	PullRequest struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Draft  bool   `json:"draft"`
		State  string `json:"state"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
	Repository struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// ParsePullRequest returns (job, "", nil) for a reviewable event, or (nil, reason, nil) when
// the event is valid but should be ignored. GitHub App webhooks carry an installation ID;
// GitHub Actions event payloads do not, so callers there pass requireInstallation=false.
func ParsePullRequest(body []byte, deliveryID string, requireInstallation bool) (*jobs.ReviewJob, string, error) {
	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, "", fmt.Errorf("decode pull_request event: %w", err)
	}
	if !reviewableActions[ev.Action] {
		return nil, "action " + ev.Action + " not reviewed", nil
	}
	pr := ev.PullRequest
	if pr.Draft {
		return nil, "draft pull request", nil
	}
	if pr.State != "" && pr.State != "open" {
		return nil, "pull request is " + pr.State, nil
	}
	if requireInstallation && ev.Installation.ID == 0 {
		return nil, "", fmt.Errorf("event has no installation id")
	}
	if pr.Head.SHA == "" || ev.Repository.Name == "" || ev.Repository.Owner.Login == "" {
		return nil, "", fmt.Errorf("event is missing repository or head sha")
	}
	number := pr.Number
	if number == 0 {
		number = ev.Number
	}
	job := &jobs.ReviewJob{
		DeliveryID:     deliveryID,
		Action:         ev.Action,
		InstallationID: ev.Installation.ID,
		Owner:          ev.Repository.Owner.Login,
		Repo:           ev.Repository.Name,
		Number:         number,
		Title:          pr.Title,
		Body:           pr.Body,
		HeadSHA:        pr.Head.SHA,
		BaseSHA:        pr.Base.SHA,
	}
	if ev.Action == "synchronize" {
		job.BeforeSHA = ev.Before
	}
	return job, "", nil
}
