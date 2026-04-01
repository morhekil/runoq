package gh_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/saruman/runoq/internal/common"
	"github.com/saruman/runoq/internal/gh"
)

func TestOutput(t *testing.T) {
	fake := func(_ context.Context, req common.CommandRequest) error {
		fmt.Fprint(req.Stdout, "hello\n")
		return nil
	}

	got, err := gh.Output(context.Background(), fake, "/tmp", nil, "pr", "list")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected %q, got %q", "hello", got)
	}
}

func TestOutput_UsesGHBin(t *testing.T) {
	fake := func(_ context.Context, req common.CommandRequest) error {
		if req.Name != "mycustomgh" {
			t.Errorf("expected binary mycustomgh, got %s", req.Name)
		}
		return nil
	}

	env := []string{"GH_BIN=mycustomgh"}
	_, err := gh.Output(context.Background(), fake, "/tmp", env, "version")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
}
