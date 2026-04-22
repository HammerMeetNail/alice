// Package cli implements the alice command-line interface. The CLI is the
// primary surface for both human operators and non-MCP agents (Codex, Gemini
// CLI, plain shell scripts); the MCP server is a secondary adapter over the
// same coordination server HTTP API.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is the persisted session for a CLI user. It is written to disk so that
// `alice register` followed by `alice query` on a later process keeps working.
// The file is stored at $ALICE_STATE_FILE if set, otherwise ~/.alice/state.json.
type State struct {
	ServerURL      string    `json:"server_url"`
	OrgSlug        string    `json:"org_slug"`
	OrgID          string    `json:"org_id"`
	OwnerEmail     string    `json:"owner_email"`
	AgentName      string    `json:"agent_name"`
	AgentID        string    `json:"agent_id"`
	PublicKey      string    `json:"public_key"`
	PrivateKey     string    `json:"private_key"`
	AccessToken    string    `json:"access_token"`
	TokenExpiresAt time.Time `json:"token_expires_at"`
}

// DefaultStatePath returns the default state file path, honoring
// $ALICE_STATE_FILE before falling back to $HOME/.alice/state.json.
func DefaultStatePath() (string, error) {
	if path := os.Getenv("ALICE_STATE_FILE"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".alice", "state.json"), nil
}

// LoadState reads and parses the state file. A missing file returns an empty
// State with no error — new users have nothing yet.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read state file: %w", err)
	}
	var s State
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("parse state file: %w", err)
	}
	return s, nil
}

// SaveState atomically writes the state file with 0600 permissions on a 0700
// parent directory.
func SaveState(path string, s State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod state tmp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write state tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close state tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}

// HasSession reports whether the state carries enough material to make
// authenticated calls.
func (s State) HasSession() bool {
	return s.AccessToken != "" && s.ServerURL != ""
}
