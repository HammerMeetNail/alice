package tracker

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RepoState captures the current git state of a local repository.
type RepoState struct {
	Path           string       `json:"path"`
	Name           string       `json:"name"`
	Branch         string       `json:"branch"`
	RecentCommits  []CommitInfo `json:"recent_commits,omitempty"`
	ModifiedFiles  []string     `json:"modified_files,omitempty"`
	StagedFiles    []string     `json:"staged_files,omitempty"`
	UntrackedFiles []string     `json:"untracked_files,omitempty"`
}

// CommitInfo holds metadata for a single git commit.
type CommitInfo struct {
	Hash      string    `json:"hash"`
	Subject   string    `json:"subject"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
}

// ReadRepoState reads the current git state from a local repository path.
func ReadRepoState(ctx context.Context, repoPath string) (RepoState, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return RepoState{}, fmt.Errorf("resolve repo path: %w", err)
	}

	state := RepoState{
		Path: absPath,
		Name: filepath.Base(absPath),
	}

	branch, err := gitOutput(ctx, absPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return RepoState{}, fmt.Errorf("read branch: %w", err)
	}
	state.Branch = branch

	if logOutput, err := gitOutput(ctx, absPath, "log", "--format=%H|%s|%an|%aI", "-n", "5"); err == nil && logOutput != "" {
		state.RecentCommits = parseCommitLog(logOutput)
	}

	if diffOutput, err := gitOutput(ctx, absPath, "diff", "--name-only"); err == nil && diffOutput != "" {
		state.ModifiedFiles = splitLines(diffOutput)
	}

	if stagedOutput, err := gitOutput(ctx, absPath, "diff", "--name-only", "--cached"); err == nil && stagedOutput != "" {
		state.StagedFiles = splitLines(stagedOutput)
	}

	if untrackedOutput, err := gitOutput(ctx, absPath, "ls-files", "--others", "--exclude-standard"); err == nil && untrackedOutput != "" {
		state.UntrackedFiles = splitLines(untrackedOutput)
	}

	return state, nil
}

func gitOutput(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func parseCommitLog(output string) []CommitInfo {
	lines := splitLines(output)
	commits := make([]CommitInfo, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, strings.TrimSpace(parts[3]))
		commits = append(commits, CommitInfo{
			Hash:      strings.TrimSpace(parts[0]),
			Subject:   strings.TrimSpace(parts[1]),
			Author:    strings.TrimSpace(parts[2]),
			Timestamp: ts,
		})
	}
	return commits
}

func splitLines(output string) []string {
	lines := strings.Split(output, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
