package jobs

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

const TypeReviewPR = "review:pull_request"

type ReviewJob struct {
	DeliveryID     string `json:"delivery_id"`
	Action         string `json:"action"`
	InstallationID int64  `json:"installation_id"`
	Owner          string `json:"owner"`
	Repo           string `json:"repo"`
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	HeadSHA        string `json:"head_sha"`
	BaseSHA        string `json:"base_sha"`
	BeforeSHA      string `json:"before_sha,omitempty"`
}

func (j ReviewJob) FullName() string { return j.Owner + "/" + j.Repo }

// TaskID makes asynq reject a second job for the same PR head while one is queued or retained.
func (j ReviewJob) TaskID() string {
	return fmt.Sprintf("%s/%s#%d@%s", j.Owner, j.Repo, j.Number, j.HeadSHA)
}

func NewReviewTask(j ReviewJob) (*asynq.Task, error) {
	payload, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeReviewPR, payload,
		asynq.TaskID(j.TaskID()),
		asynq.MaxRetry(3),
		asynq.Timeout(15*time.Minute),
		asynq.Retention(24*time.Hour),
	), nil
}
