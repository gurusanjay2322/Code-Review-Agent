package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"code-review-agent/gateway/internal/jobs"
)

const maxBodyBytes = 25 << 20

// Deduper records delivery IDs; Claim returns false if the ID was already seen.
type Deduper interface {
	Claim(ctx context.Context, deliveryID string) (bool, error)
	Release(ctx context.Context, deliveryID string) error
}

type Enqueuer interface {
	Enqueue(ctx context.Context, job jobs.ReviewJob) error
}

// ErrDuplicateJob is returned by an Enqueuer when the same PR head is already queued.
var ErrDuplicateJob = errors.New("review job already queued")

type Handler struct {
	Secret  []byte
	Dedupe  Deduper
	Queue   Enqueuer
	Log     *slog.Logger
	Timeout time.Duration
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, "body too large or unreadable")
		return
	}
	if err := VerifySignature(h.Secret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
		h.Log.Warn("rejected webhook", "err", err, "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, err.Error())
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	delivery := r.Header.Get("X-GitHub-Delivery")
	log := h.Log.With("event", event, "delivery", delivery)

	switch event {
	case "ping":
		writeJSON(w, http.StatusOK, "pong")
		return
	case "pull_request":
	default:
		writeJSON(w, http.StatusOK, "event ignored")
		return
	}

	job, reason, err := ParsePullRequest(body, delivery, true)
	if err != nil {
		log.Warn("bad pull_request payload", "err", err)
		writeJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if job == nil {
		writeJSON(w, http.StatusOK, "ignored: "+reason)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout())
	defer cancel()

	if delivery != "" && h.Dedupe != nil {
		fresh, err := h.Dedupe.Claim(ctx, delivery)
		if err != nil {
			log.Error("dedupe check failed", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, "dedupe unavailable")
			return
		}
		if !fresh {
			writeJSON(w, http.StatusOK, "duplicate delivery")
			return
		}
	}

	if err := h.Queue.Enqueue(ctx, *job); err != nil {
		if errors.Is(err, ErrDuplicateJob) {
			writeJSON(w, http.StatusOK, "review already queued for this commit")
			return
		}
		if delivery != "" && h.Dedupe != nil {
			_ = h.Dedupe.Release(context.Background(), delivery)
		}
		log.Error("enqueue failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, "queue unavailable")
		return
	}
	log.Info("review queued", "repo", job.FullName(), "pr", job.Number, "head", job.HeadSHA, "action", job.Action)
	writeJSON(w, http.StatusAccepted, "queued")
}

func (h *Handler) timeout() time.Duration {
	if h.Timeout > 0 {
		return h.Timeout
	}
	return 5 * time.Second
}

func writeJSON(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}
