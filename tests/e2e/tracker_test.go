//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"alice/internal/tracker"
)

func TestTracker_PublishesToServer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	baseURL := newE2EServer(t)
	slug := orgSlug(t)

	// Register an agent to get a token.
	agent := registerAgent(t, baseURL, slug, "tracker@example.com")

	// Create a temp git repo.
	repoDir := t.TempDir()
	gitInit(t, repoDir)

	// Set up a publish function that calls the real server.
	publishFn := func(ctx context.Context, body map[string]any) (map[string]any, error) {
		var resp map[string]any
		status, respBytes := doJSONRaw(t, baseURL, http.MethodPost, "/v1/artifacts", agent.AccessToken, body)
		if status != http.StatusOK {
			t.Fatalf("publish returned %d: %s", status, respBytes)
		}
		if err := jsonUnmarshal(respBytes, &resp); err != nil {
			t.Fatalf("decode publish response: %v", err)
		}
		return resp, nil
	}

	tr := tracker.New(
		tracker.Config{RepoPaths: []string{repoDir}, Interval: time.Minute},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)

	ctx := context.Background()
	tr.Tick(ctx)

	// Query the audit to verify the artifact was published.
	var auditResp map[string]any
	doJSON(t, baseURL, http.MethodGet, "/v1/audit/summary", agent.AccessToken, nil, http.StatusOK, &auditResp)

	events, ok := auditResp["events"].([]any)
	if !ok || len(events) == 0 {
		t.Fatal("expected at least one audit event after tracker publish")
	}
}

func TestTracker_DeduplicatesAcrossTicks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	baseURL := newE2EServer(t)
	slug := orgSlug(t)
	agent := registerAgent(t, baseURL, slug, "tracker2@example.com")

	repoDir := t.TempDir()
	gitInit(t, repoDir)

	publishCount := 0
	publishFn := func(ctx context.Context, body map[string]any) (map[string]any, error) {
		publishCount++
		var resp map[string]any
		status, respBytes := doJSONRaw(t, baseURL, http.MethodPost, "/v1/artifacts", agent.AccessToken, body)
		if status != http.StatusOK {
			t.Fatalf("publish returned %d: %s", status, respBytes)
		}
		if err := jsonUnmarshal(respBytes, &resp); err != nil {
			t.Fatalf("decode publish response: %v", err)
		}
		return resp, nil
	}

	tr := tracker.New(
		tracker.Config{RepoPaths: []string{repoDir}, Interval: time.Minute},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)

	ctx := context.Background()
	tr.Tick(ctx)
	tr.Tick(ctx)

	if publishCount != 1 {
		t.Errorf("expected 1 publish (deduped), got %d", publishCount)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitExec(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	gitExec(t, dir, "add", "main.go")
	gitExec(t, dir, "commit", "-m", "init")
}

func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "tag.gpgSign=false"}, args...)...)
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
