package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReadRepoState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "file1.go"), []byte("package main\n"), 0644)
	run("add", "file1.go")
	run("commit", "-m", "initial commit")

	os.WriteFile(filepath.Join(dir, "file1.go"), []byte("package main\n\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "file2.go"), []byte("package main\n"), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	state, err := ReadRepoState(ctx, dir)
	if err != nil {
		t.Fatalf("ReadRepoState: %v", err)
	}

	if state.Branch != "main" {
		t.Errorf("Branch = %q, want %q", state.Branch, "main")
	}
	if state.Name != filepath.Base(dir) {
		t.Errorf("Name = %q, want %q", state.Name, filepath.Base(dir))
	}
	if len(state.RecentCommits) != 1 {
		t.Errorf("RecentCommits = %d, want 1", len(state.RecentCommits))
	} else {
		if state.RecentCommits[0].Subject != "initial commit" {
			t.Errorf("Commit subject = %q, want %q", state.RecentCommits[0].Subject, "initial commit")
		}
		if state.RecentCommits[0].Author != "Test" {
			t.Errorf("Commit author = %q, want %q", state.RecentCommits[0].Author, "Test")
		}
	}
	if len(state.ModifiedFiles) != 1 || state.ModifiedFiles[0] != "file1.go" {
		t.Errorf("ModifiedFiles = %v, want [file1.go]", state.ModifiedFiles)
	}
	if len(state.UntrackedFiles) != 1 || state.UntrackedFiles[0] != "file2.go" {
		t.Errorf("UntrackedFiles = %v, want [file2.go]", state.UntrackedFiles)
	}
}

func TestReadRepoState_StagedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0644)
	run("add", "a.go")
	run("commit", "-m", "first")

	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b\n"), 0644)
	run("add", "b.go")

	ctx := context.Background()
	state, err := ReadRepoState(ctx, dir)
	if err != nil {
		t.Fatalf("ReadRepoState: %v", err)
	}

	if state.Branch != "feature" {
		t.Errorf("Branch = %q, want %q", state.Branch, "feature")
	}
	if len(state.StagedFiles) != 1 || state.StagedFiles[0] != "b.go" {
		t.Errorf("StagedFiles = %v, want [b.go]", state.StagedFiles)
	}
}

func TestReadRepoState_NotARepo(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ReadRepoState(ctx, dir)
	if err == nil {
		t.Error("expected error for non-repo directory")
	}
}
