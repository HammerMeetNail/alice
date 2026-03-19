package edge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type CredentialStore struct {
	ConnectorCredentials map[string]ConnectorCredential `json:"connector_credentials,omitempty"`
}

func LoadCredentialStore(path string) (CredentialStore, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CredentialStore{
				ConnectorCredentials: map[string]ConnectorCredential{},
			}, nil
		}
		return CredentialStore{}, fmt.Errorf("stat credential store: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return CredentialStore{}, fmt.Errorf("credential store %q must not be group or world accessible", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return CredentialStore{}, fmt.Errorf("read credential store: %w", err)
	}

	var store CredentialStore
	if err := json.Unmarshal(data, &store); err != nil {
		return CredentialStore{}, fmt.Errorf("decode credential store: %w", err)
	}
	store.normalize()
	return store, nil
}

func SaveCredentialStore(path string, store CredentialStore) error {
	store.normalize()

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credential store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential store dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write credential store: %w", err)
	}
	return nil
}

func (s *CredentialStore) normalize() {
	if s.ConnectorCredentials == nil {
		s.ConnectorCredentials = map[string]ConnectorCredential{}
	}
}

func (s CredentialStore) ConnectorCredential(connectorType string) ConnectorCredential {
	if s.ConnectorCredentials == nil {
		return ConnectorCredential{}
	}
	return s.ConnectorCredentials[connectorType]
}

func (s *CredentialStore) SetConnectorCredential(connectorType string, credential ConnectorCredential) {
	s.normalize()
	s.ConnectorCredentials[connectorType] = credential
}

func (s *CredentialStore) MigrateFromState(state *State) bool {
	s.normalize()
	if len(state.ConnectorCredentials) == 0 {
		return false
	}

	migrated := false
	for connectorType, credential := range state.ConnectorCredentials {
		if existing := s.ConnectorCredentials[connectorType]; existing.AccessToken == "" && existing.RefreshToken == "" {
			s.ConnectorCredentials[connectorType] = credential
			migrated = true
		}
	}
	state.ConnectorCredentials = nil
	return migrated
}
