package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// TestHelperProcess acts as the engine CLI when invoked by the tests below.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("ENGINE_HELPER") != "1" {
		return
	}
	defer os.Exit(0)
	args := os.Args
	sub := args[len(args)-1]
	in, _ := io.ReadAll(os.Stdin)

	switch os.Getenv("ENGINE_HELPER_MODE") {
	case "fail":
		fmt.Fprintln(os.Stderr, "line one\nboom: provider unavailable")
		os.Exit(1)
	case "garbage":
		fmt.Print("not json")
		return
	}

	switch sub {
	case "review":
		var req ReviewRequest
		_ = json.Unmarshal(in, &req)
		resp := ReviewResponse{Findings: []Finding{{Path: req.Chunks[0].Files[0].Path, Line: 3, Severity: "high", Category: "bug", Title: req.Repo}}}
		_ = json.NewEncoder(os.Stdout).Encode(resp)
	case "verify":
		var req struct {
			Items []VerifyItem `json:"items"`
		}
		_ = json.Unmarshal(in, &req)
		resp := VerifyResponse{}
		for _, it := range req.Items {
			resp.Results = append(resp.Results, VerifyResult{ID: it.ID, Supported: it.Finding.Line != 13})
		}
		_ = json.NewEncoder(os.Stdout).Encode(resp)
	default:
		os.Exit(2)
	}
}

func helperClient(mode string) *ExecClient {
	return &ExecClient{
		Command: []string{os.Args[0], "-test.run=TestHelperProcess", "--"},
		Env:     []string{"ENGINE_HELPER=1", "ENGINE_HELPER_MODE=" + mode},
	}
}

func TestExecClientReview(t *testing.T) {
	resp, err := helperClient("").Review(context.Background(), ReviewRequest{
		Repo:   "acme/api",
		Chunks: []Chunk{{ID: "c1", Files: []ChunkFile{{Path: "a.go", Content: "x"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Findings) != 1 || resp.Findings[0].Path != "a.go" || resp.Findings[0].Title != "acme/api" {
		t.Fatalf("unexpected response %+v", resp)
	}
}

func TestExecClientVerify(t *testing.T) {
	resp, err := helperClient("").Verify(context.Background(), "r", []VerifyItem{
		{ID: "0", Finding: Finding{Line: 1}}, {ID: "1", Finding: Finding{Line: 13}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 || !resp.Results[0].Supported || resp.Results[1].Supported {
		t.Fatalf("unexpected results %+v", resp.Results)
	}
}

func TestExecClientSurfacesStderrOnFailure(t *testing.T) {
	_, err := helperClient("fail").Review(context.Background(), ReviewRequest{Chunks: []Chunk{{}}})
	if err == nil || !strings.Contains(err.Error(), "boom: provider unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecClientRejectsBadOutput(t *testing.T) {
	_, err := helperClient("garbage").Review(context.Background(), ReviewRequest{})
	if err == nil || !strings.Contains(err.Error(), "decode output") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecClientNeedsCommand(t *testing.T) {
	if _, err := (&ExecClient{}).Review(context.Background(), ReviewRequest{}); err == nil {
		t.Fatal("expected error")
	}
}
