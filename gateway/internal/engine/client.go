package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ChunkFile struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Content  string `json:"content"`
}

type Chunk struct {
	ID     string      `json:"id"`
	Files  []ChunkFile `json:"files"`
	Tokens int         `json:"tokens"`
}

type ReviewRequest struct {
	Repo           string  `json:"repo"`
	PRTitle        string  `json:"prTitle"`
	PRBody         string  `json:"prBody"`
	Instructions   string  `json:"instructions,omitempty"`
	Chunks         []Chunk `json:"chunks"`
	MaxTotalTokens int     `json:"maxTotalTokens,omitempty"`
}

type Finding struct {
	Path        string  `json:"path"`
	Line        int     `json:"line"`
	EndLine     int     `json:"endLine,omitempty"`
	Severity    string  `json:"severity"`
	Category    string  `json:"category"`
	Title       string  `json:"title"`
	Explanation string  `json:"explanation"`
	Evidence    string  `json:"evidence"`
	Suggestion  string  `json:"suggestion,omitempty"`
	Confidence  float64 `json:"confidence"`
}

type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

type ChunkError struct {
	ChunkID string `json:"chunkId"`
	Error   string `json:"error"`
}

type ReviewResponse struct {
	Findings      []Finding    `json:"findings"`
	Usage         Usage        `json:"usage"`
	Errors        []ChunkError `json:"errors"`
	SkippedChunks []string     `json:"skippedChunks"`
}

type VerifyItem struct {
	ID      string  `json:"id"`
	Finding Finding `json:"finding"`
	Context string  `json:"context"`
}

type VerifyResult struct {
	ID        string `json:"id"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason"`
}

type VerifyResponse struct {
	Results []VerifyResult `json:"results"`
	Usage   Usage          `json:"usage"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error) {
	var out ReviewResponse
	if err := c.post(ctx, "/review", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Verify(ctx context.Context, repo string, items []VerifyItem) (*VerifyResponse, error) {
	var out VerifyResponse
	if err := c.post(ctx, "/verify", map[string]any{"repo": repo, "items": items}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("engine %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("engine %s: status %d: %s", path, resp.StatusCode, truncate(string(data), 500))
	}
	return json.Unmarshal(data, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
