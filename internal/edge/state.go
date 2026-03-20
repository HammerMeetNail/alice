package edge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	AgentID                string                          `json:"agent_id"`
	OrgID                  string                          `json:"org_id"`
	PublicKey              string                          `json:"public_key"`
	PrivateKey             string                          `json:"private_key"`
	AccessToken            string                          `json:"access_token"`
	TokenExpiresAt         time.Time                       `json:"token_expires_at"`
	PublishedArtifacts     map[string]string               `json:"published_artifacts,omitempty"`
	LatestDerivedArtifacts map[string]string               `json:"latest_derived_artifacts,omitempty"`
	ProjectSignalStates    map[string]string               `json:"project_signal_states,omitempty"`
	PublishedFixtures      map[string]string               `json:"published_fixtures,omitempty"`
	ConnectorCursors       map[string]string               `json:"connector_cursors,omitempty"`
	ProcessedWebhookKeys   map[string]string               `json:"processed_webhook_keys,omitempty"`
	WebhookSequenceNumbers map[string]int64                `json:"webhook_sequence_numbers,omitempty"`
	ConnectorCredentials   map[string]ConnectorCredential  `json:"connector_credentials,omitempty"`
	PendingConnectorAuths  map[string]PendingConnectorAuth `json:"pending_connector_auths,omitempty"`
}

type ConnectorCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	ObtainedAt   time.Time `json:"obtained_at"`
}

type PendingConnectorAuth struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	RedirectURL  string    `json:"redirect_url"`
	CreatedAt    time.Time `json:"created_at"`
}

func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			state := State{
				PublishedArtifacts: map[string]string{},
			}
			state.normalizePublishedArtifacts()
			return state, nil
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
	state.ConnectorCredentials = nil

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
	if s.ConnectorCursors == nil {
		s.ConnectorCursors = map[string]string{}
	}
	if s.ProcessedWebhookKeys == nil {
		s.ProcessedWebhookKeys = map[string]string{}
	}
	if s.WebhookSequenceNumbers == nil {
		s.WebhookSequenceNumbers = map[string]int64{}
	}
	if s.LatestDerivedArtifacts == nil {
		s.LatestDerivedArtifacts = map[string]string{}
	}
	if s.ProjectSignalStates == nil {
		s.ProjectSignalStates = map[string]string{}
	}
	if s.ConnectorCredentials == nil {
		s.ConnectorCredentials = map[string]ConnectorCredential{}
	}
	if s.PendingConnectorAuths == nil {
		s.PendingConnectorAuths = map[string]PendingConnectorAuth{}
	}
}

func (s State) CursorTime(key string) time.Time {
	value, ok := s.ConnectorCursors[key]
	if !ok {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed
	}
	parsed, err = time.Parse(time.RFC3339, value)
	if err == nil {
		return parsed
	}
	return time.Time{}
}

func (s *State) SetCursorTime(key string, value time.Time) {
	s.normalizePublishedArtifacts()
	if key == "" || value.IsZero() {
		return
	}
	s.ConnectorCursors[key] = value.UTC().Format(time.RFC3339Nano)
}

func (s State) WebhookProcessedAt(key string) time.Time {
	value, ok := s.ProcessedWebhookKeys[key]
	if !ok {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed
	}
	parsed, err = time.Parse(time.RFC3339, value)
	if err == nil {
		return parsed
	}
	return time.Time{}
}

func (s *State) MarkWebhookProcessed(key string, value time.Time) {
	s.normalizePublishedArtifacts()
	if key == "" || value.IsZero() {
		return
	}
	s.ProcessedWebhookKeys[key] = value.UTC().Format(time.RFC3339Nano)
}

func (s *State) PruneProcessedWebhooks(cutoff time.Time) {
	s.normalizePublishedArtifacts()
	if cutoff.IsZero() {
		return
	}
	for key, value := range s.ProcessedWebhookKeys {
		processedAt, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			processedAt, err = time.Parse(time.RFC3339, value)
		}
		if err != nil || processedAt.Before(cutoff) {
			delete(s.ProcessedWebhookKeys, key)
		}
	}
}

func (s State) WebhookSequenceNumber(key string) int64 {
	return s.WebhookSequenceNumbers[key]
}

func (s *State) SetWebhookSequenceNumber(key string, value int64) {
	s.normalizePublishedArtifacts()
	if key == "" || value <= 0 {
		return
	}
	s.WebhookSequenceNumbers[key] = value
}

func (s State) ProjectSignalState(key string) string {
	return s.ProjectSignalStates[key]
}

func (s *State) SetProjectSignalState(key, value string) {
	s.normalizePublishedArtifacts()
	if key == "" {
		return
	}
	if value == "" {
		delete(s.ProjectSignalStates, key)
		return
	}
	s.ProjectSignalStates[key] = value
}
