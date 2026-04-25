package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTracker_Tick_PublishesArtifact(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	run := func(args ...string) {
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

	run("init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	run("add", "main.go")
	run("commit", "-m", "init")

	var mu sync.Mutex
	var published []map[string]any

	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, body)
		return map[string]any{"artifact_id": "art_001"}, nil
	}

	tr := New(
		Config{RepoPaths: []string{dir}, Interval: time.Minute},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)

	ctx := context.Background()
	tr.Tick(ctx)

	mu.Lock()
	count := len(published)
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 publish call, got %d", count)
	}
}

func TestTracker_Tick_DeduplicatesUnchangedState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	run := func(args ...string) {
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

	run("init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	run("add", "main.go")
	run("commit", "-m", "init")

	callCount := 0
	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		callCount++
		return map[string]any{"artifact_id": "art_001"}, nil
	}

	tr := New(
		Config{RepoPaths: []string{dir}, Interval: time.Minute},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)

	ctx := context.Background()
	tr.Tick(ctx)
	tr.Tick(ctx)
	tr.Tick(ctx)

	if callCount != 1 {
		t.Errorf("expected 1 publish call (deduped), got %d", callCount)
	}
}

func TestTracker_Tick_RepublishesOnChange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	run := func(args ...string) {
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

	run("init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	run("add", "main.go")
	run("commit", "-m", "init")

	artifactSeq := 0
	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		artifactSeq++
		return map[string]any{"artifact_id": "art_" + string(rune('0'+artifactSeq))}, nil
	}

	tr := New(
		Config{RepoPaths: []string{dir}, Interval: time.Minute},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)

	ctx := context.Background()
	tr.Tick(ctx)

	// Modify a file to change git state
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	tr.Tick(ctx)

	if artifactSeq != 2 {
		t.Errorf("expected 2 publish calls after state change, got %d", artifactSeq)
	}
}

func TestTracker_Tick_SkipsWhenNoSession(t *testing.T) {
	callCount := 0
	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		callCount++
		return map[string]any{"artifact_id": "art_001"}, nil
	}

	registerErr := func(ctx context.Context) error {
		return context.DeadlineExceeded
	}

	tr := New(
		Config{RepoPaths: []string{"/nonexistent"}, Interval: time.Minute},
		publishFn,
		registerErr,
		func() bool { return false },
	)

	ctx := context.Background()
	tr.Tick(ctx)

	if callCount != 0 {
		t.Errorf("expected 0 publish calls when registration fails, got %d", callCount)
	}
}

func TestTracker_Tick_SupersedesOnChange(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	run := func(args ...string) {
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

	run("init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	run("add", "main.go")
	run("commit", "-m", "init")

	var bodies []map[string]any
	seq := 0
	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		seq++
		bodies = append(bodies, body)
		return map[string]any{"artifact_id": "art_" + string(rune('0'+seq))}, nil
	}

	tr := New(
		Config{RepoPaths: []string{dir}, Interval: time.Minute},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)

	ctx := context.Background()
	tr.Tick(ctx)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	tr.Tick(ctx)

	if len(bodies) != 2 {
		t.Fatalf("expected 2 publish calls, got %d", len(bodies))
	}

	// Second publish should have supersedes_artifact_id set
	second := bodies[1]
	artifact, ok := second["artifact"].(map[string]any)
	if !ok {
		t.Fatal("second body missing artifact field")
	}
	supersedes, ok := artifact["supersedes_artifact_id"]
	if !ok || supersedes == nil {
		t.Error("second artifact should have supersedes_artifact_id set")
	}
}
