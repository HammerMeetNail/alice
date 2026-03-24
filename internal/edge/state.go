package edge

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const stateSecretsAAD = "alice.edge.state.secrets.v1"

// StateOptions controls optional encryption for the state file.
type StateOptions struct {
	EncryptionSecret string
}

type stateSecrets struct {
	PrivateKey  string `json:"private_key"`
	AccessToken string `json:"access_token"`
}

type encryptedStateSecretsEnvelope struct {
	Format     string `json:"format"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

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
	ConnectorCredentials   map[string]ConnectorCredential          `json:"connector_credentials,omitempty"`
	PendingConnectorAuths  map[string]PendingConnectorAuth         `json:"pending_connector_auths,omitempty"`
	ConnectorWatches       map[string]ConnectorWatchState          `json:"connector_watches,omitempty"`
	EncryptedSecrets       *encryptedStateSecretsEnvelope          `json:"encrypted_secrets,omitempty"`
}

// ConnectorWatchState records an active provider-side watch (e.g. a Google
// Calendar push-notification channel) that was registered by the edge agent.
type ConnectorWatchState struct {
	ConnectorType string    `json:"connector_type"`
	ScopeRef      string    `json:"scope_ref"`
	ChannelID     string    `json:"channel_id"`
	ResourceID    string    `json:"resource_id"`
	ResourceURI   string    `json:"resource_uri,omitempty"`
	CallbackURL   string    `json:"callback_url"`
	RegisteredAt  time.Time `json:"registered_at"`
	ExpiresAt     time.Time `json:"expires_at"`
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
	return LoadStateWithOptions(path, StateOptions{})
}

func LoadStateWithOptions(path string, options StateOptions) (State, error) {
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

	if state.EncryptedSecrets != nil {
		secrets, err := decryptStateSecrets(path, state.EncryptedSecrets, options)
		if err != nil {
			return State{}, err
		}
		state.PrivateKey = secrets.PrivateKey
		state.AccessToken = secrets.AccessToken
		state.EncryptedSecrets = nil
	}

	state.normalizePublishedArtifacts()
	return state, nil
}

func SaveState(path string, state State) error {
	return SaveStateWithOptions(path, state, StateOptions{})
}

func SaveStateWithOptions(path string, state State, options StateOptions) error {
	state.normalizePublishedArtifacts()
	state.PublishedFixtures = nil
	state.ConnectorCredentials = nil
	state.EncryptedSecrets = nil

	if options.EncryptionSecret != "" {
		envelope, err := encryptStateSecrets(stateSecrets{
			PrivateKey:  state.PrivateKey,
			AccessToken: state.AccessToken,
		}, options.EncryptionSecret)
		if err != nil {
			return fmt.Errorf("encrypt state secrets: %w", err)
		}
		state.PrivateKey = ""
		state.AccessToken = ""
		state.EncryptedSecrets = envelope
	} else {
		fmt.Fprintf(os.Stderr, "warning: state file %q contains plaintext credentials; set ALICE_EDGE_CREDENTIAL_KEY to enable encryption\n", path)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

func encryptStateSecrets(secrets stateSecrets, encryptionSecret string) (*encryptedStateSecretsEnvelope, error) {
	plaintext, err := json.Marshal(secrets)
	if err != nil {
		return nil, fmt.Errorf("marshal state secrets: %w", err)
	}
	aead, err := credentialStoreAEAD(encryptionSecret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate state secrets nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte(stateSecretsAAD))
	return &encryptedStateSecretsEnvelope{
		Format:     "encrypted",
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func decryptStateSecrets(path string, envelope *encryptedStateSecretsEnvelope, options StateOptions) (stateSecrets, error) {
	if envelope.Format != "encrypted" {
		return stateSecrets{}, fmt.Errorf("state file %q has unknown secrets format %q", path, envelope.Format)
	}
	if options.EncryptionSecret == "" {
		return stateSecrets{}, fmt.Errorf("state file %q contains encrypted secrets; set ALICE_EDGE_CREDENTIAL_KEY to decrypt", path)
	}
	if envelope.Algorithm != "aes-256-gcm" {
		return stateSecrets{}, fmt.Errorf("state file %q uses unsupported secrets algorithm %q", path, envelope.Algorithm)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return stateSecrets{}, fmt.Errorf("decode state secrets nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return stateSecrets{}, fmt.Errorf("decode state secrets ciphertext: %w", err)
	}
	aead, err := credentialStoreAEAD(options.EncryptionSecret)
	if err != nil {
		return stateSecrets{}, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(stateSecretsAAD))
	if err != nil {
		return stateSecrets{}, fmt.Errorf("decrypt state secrets in %q: wrong key or corrupted file", path)
	}
	var secrets stateSecrets
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return stateSecrets{}, fmt.Errorf("decode decrypted state secrets: %w", err)
	}
	return secrets, nil
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
	if s.ConnectorWatches == nil {
		s.ConnectorWatches = map[string]ConnectorWatchState{}
	}
}

func (s State) ConnectorWatch(key string) ConnectorWatchState {
	return s.ConnectorWatches[key]
}

func (s *State) SetConnectorWatch(key string, watch ConnectorWatchState) {
	s.normalizePublishedArtifacts()
	if key == "" {
		return
	}
	s.ConnectorWatches[key] = watch
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
