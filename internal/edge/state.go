package edge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	AgentID            string            `json:"agent_id"`
	OrgID              string            `json:"org_id"`
	PublicKey          string            `json:"public_key"`
	PrivateKey         string            `json:"private_key"`
	AccessToken        string            `json:"access_token"`
	TokenExpiresAt     time.Time         `json:"token_expires_at"`
	PublishedArtifacts map[string]string `json:"published_artifacts,omitempty"`
	PublishedFixtures  map[string]string `json:"published_fixtures,omitempty"`
}

func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{
				PublishedArtifacts: map[string]string{},
			}, nil
		}
		return State{}, fmt.Errorf("read state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	state.normalizePublishedArtifacts()
	return state, nil
}

func SaveState(path string, state State) error {
	state.normalizePublishedArtifacts()
	state.PublishedFixtures = nil

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func (s *State) normalizePublishedArtifacts() {
	if s.PublishedArtifacts == nil {
		s.PublishedArtifacts = map[string]string{}
	}
	for digest, artifactID := range s.PublishedFixtures {
		if _, ok := s.PublishedArtifacts[digest]; ok {
			continue
		}
		s.PublishedArtifacts[digest] = artifactID
	}
}
