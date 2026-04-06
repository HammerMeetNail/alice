package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTrackerState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracker-state.json")

	original := trackerState{
		Published: map[string]string{"digest1": "art_001", "digest2": "art_002"},
		Latest:    map[string]string{"local_git:/repo": "art_002"},
	}

	saveTrackerState(path, original)

	loaded := loadTrackerState(path)

	if len(loaded.Published) != 2 {
		t.Errorf("Published count = %d, want 2", len(loaded.Published))
	}
	if loaded.Published["digest1"] != "art_001" {
		t.Errorf("Published[digest1] = %q, want %q", loaded.Published["digest1"], "art_001")
	}
	if loaded.Latest["local_git:/repo"] != "art_002" {
		t.Errorf("Latest[local_git:/repo] = %q, want %q", loaded.Latest["local_git:/repo"], "art_002")
	}
}

func TestTrackerState_MissingFile(t *testing.T) {
	state := loadTrackerState("/nonexistent/path/state.json")

	if state.Published == nil {
		t.Error("Published should be initialized")
	}
	if state.Latest == nil {
		t.Error("Latest should be initialized")
	}
}

func TestTrackerState_EmptyPath(t *testing.T) {
	state := loadTrackerState("")
	if state.Published == nil {
		t.Error("Published should be initialized")
	}

	// Should not panic
	saveTrackerState("", trackerState{Published: map[string]string{"a": "b"}})
}

func TestTrackerState_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json"), 0600)

	state := loadTrackerState(path)
	if state.Published == nil {
		t.Error("Published should be initialized even on corrupt file")
	}
}

func TestTrackerState_DirPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "state.json")

	saveTrackerState(path, trackerState{
		Published: map[string]string{"d": "a"},
		Latest:    map[string]string{},
	})

	loaded := loadTrackerState(path)
	if loaded.Published["d"] != "a" {
		t.Errorf("Expected state to survive nested dir creation")
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestTracker_PersistsState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	repoDir := t.TempDir()
	gitRun(t, repoDir, "init", "-b", "main")
	os.WriteFile(filepath.Join(repoDir, "f.go"), []byte("package f\n"), 0644)
	gitRun(t, repoDir, "add", "f.go")
	gitRun(t, repoDir, "commit", "-m", "init")

	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "state.json")

	callCount := 0
	publishFn := func(_ context.Context, body map[string]any) (map[string]any, error) {
		callCount++
		return map[string]any{"artifact_id": "art_001"}, nil
	}

	// First tracker: publishes and saves state
	t1 := New(
		Config{RepoPaths: []string{repoDir}, Interval: 0, StatePath: statePath},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)
	t1.Tick(context.Background())

	if callCount != 1 {
		t.Fatalf("expected 1 publish, got %d", callCount)
	}

	// Second tracker: loads state, should not re-publish
	t2 := New(
		Config{RepoPaths: []string{repoDir}, Interval: 0, StatePath: statePath},
		publishFn,
		func(ctx context.Context) error { return nil },
		func() bool { return true },
	)
	t2.Tick(context.Background())

	if callCount != 1 {
		t.Errorf("expected no re-publish after state reload, got %d total", callCount)
	}
}
