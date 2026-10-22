package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

const stderrTailLines = 20

// ExecClient runs the engine CLI as a subprocess per call (`<command...> review|verify`),
// exchanging JSON over stdin and stdout. It needs no engine server.
type ExecClient struct {
	Command []string
	// Env is appended to the parent environment.
	Env []string
}

func NewExecClient(command ...string) *ExecClient {
	return &ExecClient{Command: command}
}

func (c *ExecClient) Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error) {
	var out ReviewResponse
	if err := c.run(ctx, "review", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *ExecClient) Verify(ctx context.Context, repo string, items []VerifyItem) (*VerifyResponse, error) {
	var out VerifyResponse
	if err := c.run(ctx, "verify", map[string]any{"repo": repo, "items": items}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *ExecClient) run(ctx context.Context, sub string, in, out any) error {
	if len(c.Command) == 0 {
		return fmt.Errorf("engine exec: no command configured")
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	args := append(append([]string{}, c.Command[1:]...), sub)
	cmd := exec.CommandContext(ctx, c.Command[0], args...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(c.Env) > 0 {
		cmd.Env = append(cmd.Environ(), c.Env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("engine %s: %w: %s", sub, err, tail(stderr.String(), stderrTailLines))
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("engine %s: decode output: %w", sub, err)
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
