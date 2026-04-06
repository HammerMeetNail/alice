package tracker

import (
	"strings"
	"testing"
	"time"

	"alice/internal/core"
)

func TestDeriveArtifacts(t *testing.T) {
	state := RepoState{
		Path:   "/home/user/project",
		Name:   "project",
		Branch: "fix/auth-bug",
		RecentCommits: []CommitInfo{
			{Hash: "abc1234567890", Subject: "Fix token expiry", Author: "Dave", Timestamp: time.Now()},
		},
		ModifiedFiles:  []string{"auth.go", "auth_test.go"},
		StagedFiles:    []string{"config.go"},
		UntrackedFiles: []string{"tmp.txt"},
	}

	artifacts := DeriveArtifacts(state)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}

	a := artifacts[0]

	if a.Type != core.ArtifactTypeStatusDelta {
		t.Errorf("Type = %q, want %q", a.Type, core.ArtifactTypeStatusDelta)
	}
	if !strings.Contains(a.Title, "project") {
		t.Errorf("Title %q should contain repo name", a.Title)
	}
	if !strings.Contains(a.Content, "fix/auth-bug") {
		t.Errorf("Content %q should contain branch name", a.Content)
	}
	if !strings.Contains(a.Content, "Fix token expiry") {
		t.Errorf("Content %q should contain commit subject", a.Content)
	}
	if !strings.Contains(a.Content, "auth.go") {
		t.Errorf("Content %q should contain modified files", a.Content)
	}
	if a.Sensitivity != core.SensitivityLow {
		t.Errorf("Sensitivity = %q, want %q", a.Sensitivity, core.SensitivityLow)
	}
	if a.VisibilityMode != core.VisibilityModeExplicitGrantsOnly {
		t.Errorf("VisibilityMode = %q, want %q", a.VisibilityMode, core.VisibilityModeExplicitGrantsOnly)
	}
	if a.StructuredPayload == nil {
		t.Fatal("StructuredPayload is nil")
	}
	if dk, ok := a.StructuredPayload["derivation_key"].(string); !ok || dk == "" {
		t.Error("missing derivation_key in StructuredPayload")
	}
	if ss, ok := a.StructuredPayload["source_system"].(string); !ok || ss != "local_git" {
		t.Errorf("source_system = %q, want %q", ss, "local_git")
	}
}

func TestDeriveArtifacts_EmptyRepo(t *testing.T) {
	state := RepoState{
		Path:   "/home/user/empty",
		Name:   "empty",
		Branch: "main",
	}

	artifacts := DeriveArtifacts(state)
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if !strings.Contains(artifacts[0].Content, "On branch main.") {
		t.Errorf("Content %q should mention branch", artifacts[0].Content)
	}
}

func TestFormatFileList_Truncation(t *testing.T) {
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go"}
	result := formatFileList(files)
	if !strings.Contains(result, "(+2 more)") {
		t.Errorf("expected truncation, got %q", result)
	}
}
