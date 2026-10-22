package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message)
}

func StatusCode(err error) int {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode
	}
	return 0
}

type File struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	Patch            string `json:"patch,omitempty"`
	SHA              string `json:"sha"`
}

type ReviewComment struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

type DraftComment struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line,omitempty"`
	StartSide string `json:"start_side,omitempty"`
}

type CreateReviewRequest struct {
	CommitID string         `json:"commit_id"`
	Body     string         `json:"body"`
	Event    string         `json:"event"`
	Comments []DraftComment `json:"comments,omitempty"`
}

type CheckRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type CheckRun struct {
	ID          int64           `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	HeadSHA     string          `json:"head_sha,omitempty"`
	Status      string          `json:"status,omitempty"`
	Conclusion  string          `json:"conclusion,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Output      *CheckRunOutput `json:"output,omitempty"`
}

const maxPRFiles = 3000

func (c *Client) ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]File, error) {
	var all []File
	for page := 1; len(all) < maxPRFiles; page++ {
		var batch []File
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files", esc(owner), esc(repo), number)
		if err := c.do(ctx, http.MethodGet, path, q, nil, "", &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

type Comparison struct {
	Status   string `json:"status"`
	Files    []File `json:"files"`
	AheadBy  int    `json:"ahead_by"`
	BehindBy int    `json:"behind_by"`
}

func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) (*Comparison, error) {
	var out Comparison
	path := fmt.Sprintf("/repos/%s/%s/compare/%s...%s", esc(owner), esc(repo), esc(base), esc(head))
	if err := c.do(ctx, http.MethodGet, path, url.Values{"per_page": {"100"}}, nil, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRawContent returns (nil, false, nil) when the file does not exist at ref.
func (c *Client) GetRawContent(ctx context.Context, owner, repo, filePath, ref string) ([]byte, bool, error) {
	segments := strings.Split(filePath, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	path := fmt.Sprintf("/repos/%s/%s/contents/%s", esc(owner), esc(repo), strings.Join(segments, "/"))
	var raw []byte
	err := c.do(ctx, http.MethodGet, path, url.Values{"ref": {ref}}, nil, "application/vnd.github.raw+json", &raw)
	if StatusCode(err) == http.StatusNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (c *Client) ListReviewComments(ctx context.Context, owner, repo string, number int) ([]ReviewComment, error) {
	var all []ReviewComment
	for page := 1; page <= 30; page++ {
		var batch []ReviewComment
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", esc(owner), esc(repo), number)
		if err := c.do(ctx, http.MethodGet, path, q, nil, "", &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

type Review struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

func (c *Client) ListReviews(ctx context.Context, owner, repo string, number int) ([]Review, error) {
	var all []Review
	for page := 1; page <= 10; page++ {
		var batch []Review
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", esc(owner), esc(repo), number)
		if err := c.do(ctx, http.MethodGet, path, q, nil, "", &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func (c *Client) CreateReview(ctx context.Context, owner, repo string, number int, req CreateReviewRequest) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", esc(owner), esc(repo), number)
	return c.do(ctx, http.MethodPost, path, nil, req, "", nil)
}

func (c *Client) CreateReviewComment(ctx context.Context, owner, repo string, number int, commitID string, dc DraftComment) error {
	body := struct {
		DraftComment
		CommitID string `json:"commit_id"`
	}{dc, commitID}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", esc(owner), esc(repo), number)
	return c.do(ctx, http.MethodPost, path, nil, body, "", nil)
}

func (c *Client) CreateCheckRun(ctx context.Context, owner, repo string, run CheckRun) (*CheckRun, error) {
	var out CheckRun
	path := fmt.Sprintf("/repos/%s/%s/check-runs", esc(owner), esc(repo))
	if err := c.do(ctx, http.MethodPost, path, nil, run, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateCheckRun(ctx context.Context, owner, repo string, id int64, run CheckRun) error {
	path := fmt.Sprintf("/repos/%s/%s/check-runs/%d", esc(owner), esc(repo), id)
	return c.do(ctx, http.MethodPatch, path, nil, run, "", nil)
}

const maxAttempts = 3

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in any, accept string, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}

	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "code-review-agent")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if attempt < maxAttempts && ctx.Err() == nil {
				sleep(ctx, backoff(attempt, nil))
				continue
			}
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil || len(body) == 0 {
				return nil
			}
			if raw, ok := out.(*[]byte); ok {
				*raw = body
				return nil
			}
			return json.Unmarshal(body, out)
		}

		if retryable(resp) && attempt < maxAttempts {
			if !sleep(ctx, backoff(attempt, resp)) {
				return ctx.Err()
			}
			continue
		}
		apiErr := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Body: string(body)}
		var msg struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &msg) == nil {
			apiErr.Message = msg.Message
		}
		return apiErr
	}
}

func retryable(resp *http.Response) bool {
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0")
}

func backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			return min(time.Duration(s)*time.Second, time.Minute)
		}
	}
	return time.Duration(attempt*attempt) * time.Second
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func esc(s string) string { return url.PathEscape(s) }
