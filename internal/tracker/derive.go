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
	parts := []string{fmt.Sprintf("On branch %s.", state.Branch)}

	if len(state.RecentCommits) > 0 {
		parts = append(parts, fmt.Sprintf("Recent commits: %s.", formatCommitSummary(state.RecentCommits)))
	}
	if len(state.ModifiedFiles) > 0 {
		parts = append(parts, fmt.Sprintf("Modified files: %s.", formatFileList(state.ModifiedFiles)))
	}
	if len(state.StagedFiles) > 0 {
		parts = append(parts, fmt.Sprintf("Staged files: %s.", formatFileList(state.StagedFiles)))
	}

	return core.Artifact{
		Type:    core.ArtifactTypeStatusDelta,
		Title:   fmt.Sprintf("Local activity in %s", state.Name),
		Content: strings.Join(parts, " "),
		StructuredPayload: map[string]any{
			"derivation_key":  fmt.Sprintf("local_git:%s", state.Path),
			"source_system":   "local_git",
			"repo_path":       state.Path,
			"repo_name":       state.Name,
			"branch":          state.Branch,
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
