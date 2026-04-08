package tracker

import (
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
)

// DeriveArtifacts produces publishable artifacts from local git state.
func DeriveArtifacts(state RepoState) []core.Artifact {
	return []core.Artifact{deriveGitStatusArtifact(state)}
}

func deriveGitStatusArtifact(state RepoState) core.Artifact {
	focus := inferWorkFocus(state)
	activity := inferActivityLevel(state)

	parts := []string{fmt.Sprintf("Working on %s (branch %s).", focus, state.Branch)}

	if activity == "active" {
		dirtyCount := len(state.ModifiedFiles) + len(state.StagedFiles)
		parts = append(parts, fmt.Sprintf("Active: %d file(s) with uncommitted changes.", dirtyCount))
	}
	if len(state.RecentCommits) > 0 {
		parts = append(parts, fmt.Sprintf("Recent commits: %s.", formatCommitSummary(state.RecentCommits)))
	}
	if len(state.ModifiedFiles) > 0 {
		parts = append(parts, fmt.Sprintf("Modified: %s.", formatFileList(state.ModifiedFiles)))
	}
	if len(state.StagedFiles) > 0 {
		parts = append(parts, fmt.Sprintf("Staged: %s.", formatFileList(state.StagedFiles)))
	}

	return core.Artifact{
		Type:    core.ArtifactTypeStatusDelta,
		Title:   fmt.Sprintf("%s — %s in %s", focus, activity, state.Name),
		Content: strings.Join(parts, " "),
		StructuredPayload: map[string]any{
			"derivation_key":  fmt.Sprintf("local_git:%s", state.Path),
			"source_system":   "local_git",
			"repo_path":       state.Path,
			"repo_name":       state.Name,
			"branch":          state.Branch,
			"work_focus":      focus,
			"activity_level":  activity,
			"recent_commits":  state.RecentCommits,
			"modified_files":  state.ModifiedFiles,
			"staged_files":    state.StagedFiles,
			"untracked_files": state.UntrackedFiles,
		},
		SourceRefs: []core.SourceReference{{
			SourceSystem: "local_git",
			SourceType:   "repository",
			SourceID:     state.Path,
			ObservedAt:   time.Now().UTC(),
			TrustClass:   core.TrustClassStructuredSystem,
			Sensitivity:  core.SensitivityLow,
		}},
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		Sensitivity:    core.SensitivityLow,
		Confidence:     0.9,
	}
}

// inferWorkFocus derives a human-readable work description from the branch name
// and recent commit subjects. Falls back to the branch name if no pattern matches.
func inferWorkFocus(state RepoState) string {
	// Try branch name patterns first: feature/auth-refactor, fix/login-bug, etc.
	branch := state.Branch
	if branch == "main" || branch == "master" || branch == "develop" {
		if len(state.RecentCommits) > 0 {
			return summarizeCommitSubjects(state.RecentCommits)
		}
		return branch + " branch"
	}

	// Strip common prefixes and convert separators to spaces.
	focus := branch
	for _, prefix := range []string{"feature/", "feat/", "fix/", "bugfix/", "hotfix/", "chore/", "refactor/", "docs/", "test/"} {
		if strings.HasPrefix(focus, prefix) {
			focus = strings.TrimPrefix(focus, prefix)
			break
		}
	}
	focus = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(focus)
	return strings.TrimSpace(focus)
}

// summarizeCommitSubjects creates a short summary from recent commit subjects.
func summarizeCommitSubjects(commits []CommitInfo) string {
	if len(commits) == 0 {
		return "unknown"
	}
	// Use the most recent commit subject as the focus.
	subject := commits[0].Subject
	// Trim conventional commit prefixes.
	for _, prefix := range []string{"feat: ", "fix: ", "chore: ", "refactor: ", "docs: ", "test: ", "ci: "} {
		if strings.HasPrefix(strings.ToLower(subject), prefix) {
			subject = subject[len(prefix):]
			break
		}
	}
	if len(subject) > 60 {
		subject = subject[:57] + "..."
	}
	return strings.TrimSpace(subject)
}

// inferActivityLevel returns "active" if there are uncommitted changes, "idle" otherwise.
func inferActivityLevel(state RepoState) string {
	if len(state.ModifiedFiles) > 0 || len(state.StagedFiles) > 0 || len(state.UntrackedFiles) > 0 {
		return "active"
	}
	return "idle"
}

func formatCommitSummary(commits []CommitInfo) string {
	summaries := make([]string, 0, len(commits))
	for _, c := range commits {
		summaries = append(summaries, fmt.Sprintf("%s (%s)", c.Subject, c.Hash[:minInt(7, len(c.Hash))]))
	}
	return strings.Join(summaries, ", ")
}

func formatFileList(files []string) string {
	if len(files) <= 5 {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:5], ", ") + fmt.Sprintf(" (+%d more)", len(files)-5)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
