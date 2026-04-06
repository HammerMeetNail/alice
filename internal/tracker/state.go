package tracker

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// trackerState is persisted to disk between MCP server restarts.
type trackerState struct {
	Published map[string]string `json:"published"` // digest -> artifact ID
	Latest    map[string]string `json:"latest"`    // derivation_key -> artifact ID
}

func loadTrackerState(path string) trackerState {
	if path == "" {
		return trackerState{
			Published: make(map[string]string),
			Latest:    make(map[string]string),
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("tracker: failed to load state", "path", path, "err", err)
		}
		return trackerState{
			Published: make(map[string]string),
			Latest:    make(map[string]string),
		}
	}

	var state trackerState
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("tracker: failed to decode state", "path", path, "err", err)
		return trackerState{
			Published: make(map[string]string),
			Latest:    make(map[string]string),
		}
	}

	if state.Published == nil {
		state.Published = make(map[string]string)
	}
	if state.Latest == nil {
		state.Latest = make(map[string]string)
	}
	return state
}

func saveTrackerState(path string, state trackerState) {
	if path == "" {
		return
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		slog.Warn("tracker: failed to create state dir", "path", dir, "err", err)
		return
	}

	data, err := json.Marshal(state)
	if err != nil {
		slog.Warn("tracker: failed to encode state", "err", err)
		return
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		slog.Warn("tracker: failed to save state", "path", path, "err", err)
	}
}
