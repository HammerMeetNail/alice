package cli

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"
)

// TestCompletionSubcommandsInSync fails when a subcommand is added to or
// removed from the dispatch map without the corresponding update to the
// shell-completion word list. The two lists MUST match so that tab completion
// matches runtime dispatch exactly.
func TestCompletionSubcommandsInSync(t *testing.T) {
	listed := strings.Fields(completionSubcommands)
	sort.Strings(listed)

	registered := make([]string, 0, len(subcommands))
	for name := range subcommands {
		registered = append(registered, name)
	}
	sort.Strings(registered)

	if strings.Join(listed, ",") != strings.Join(registered, ",") {
		t.Fatalf("completionSubcommands out of sync with subcommands map\n  completion: %v\n  dispatch:   %v", listed, registered)
	}
}

// TestCompletionCommand verifies each supported shell emits a non-empty
// completion script and that an unknown shell errors out cleanly.
func TestCompletionCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"completion", shell},
			strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("completion %s: code=%d stderr=%s", shell, code, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Fatalf("completion %s: empty output", shell)
		}
		if !strings.Contains(stdout.String(), "alice") {
			t.Fatalf("completion %s: script does not reference alice: %s", shell, stdout.String())
		}
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"completion", "powershell"},
		strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown shell, got stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unsupported shell") {
		t.Fatalf("expected 'unsupported shell' error, got stderr=%s", stderr.String())
	}
}
