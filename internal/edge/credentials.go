package edge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const credentialStoreAAD = "alice.edge.credential_store.v1"

type CredentialStore struct {
	ConnectorCredentials map[string]ConnectorCredential `json:"connector_credentials,omitempty"`
}

type CredentialStoreOptions struct {
	EncryptionSecret string
}

type encryptedCredentialStoreFile struct {
	Format     string `json:"format"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func LoadCredentialStore(path string) (CredentialStore, error) {
	return LoadCredentialStoreWithOptions(path, CredentialStoreOptions{})
}

func LoadCredentialStoreWithOptions(path string, options CredentialStoreOptions) (CredentialStore, error) {
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

	if encrypted, ok, err := decodeEncryptedCredentialStore(path, data, options); ok || err != nil {
		return encrypted, err
	}

	var store CredentialStore
	if err := json.Unmarshal(data, &store); err != nil {
		return CredentialStore{}, fmt.Errorf("decode credential store: %w", err)
	}
	store.normalize()
	return store, nil
}

func SaveCredentialStore(path string, store CredentialStore) error {
	return SaveCredentialStoreWithOptions(path, store, CredentialStoreOptions{})
}

func SaveCredentialStoreWithOptions(path string, store CredentialStore, options CredentialStoreOptions) error {
	store.normalize()

	data, err := marshalCredentialStore(store, options)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential store dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write credential store: %w", err)
	}
	return nil
}

func marshalCredentialStore(store CredentialStore, options CredentialStoreOptions) ([]byte, error) {
	if options.EncryptionSecret == "" {
		data, err := json.MarshalIndent(store, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal credential store: %w", err)
		}
		return data, nil
	}

	plaintext, err := json.Marshal(store)
	if err != nil {
		return nil, fmt.Errorf("marshal credential store plaintext: %w", err)
	}
	aead, err := credentialStoreAEAD(options.EncryptionSecret)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate credential store nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte(credentialStoreAAD))
	envelope := encryptedCredentialStoreFile{
		Format:     "encrypted",
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal encrypted credential store: %w", err)
	}
	return data, nil
}

func decodeEncryptedCredentialStore(path string, data []byte, options CredentialStoreOptions) (CredentialStore, bool, error) {
	var envelope encryptedCredentialStoreFile
	if err := json.Unmarshal(data, &envelope); err != nil {
		return CredentialStore{}, false, nil
	}
	if envelope.Format != "encrypted" {
		return CredentialStore{}, false, nil
	}
	if options.EncryptionSecret == "" {
		return CredentialStore{}, true, &CredentialStoreKeyRequiredError{
			Path:   path,
			Reason: "set runtime.credentials_key_env_var or runtime.credentials_key_file before loading the encrypted store",
		}
	}
	if envelope.Algorithm != "aes-256-gcm" {
		return CredentialStore{}, true, fmt.Errorf("credential store %q uses unsupported algorithm %q", path, envelope.Algorithm)
	}

	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return CredentialStore{}, true, fmt.Errorf("decode credential store nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return CredentialStore{}, true, fmt.Errorf("decode credential store ciphertext: %w", err)
	}
	aead, err := credentialStoreAEAD(options.EncryptionSecret)
	if err != nil {
		return CredentialStore{}, true, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(credentialStoreAAD))
	if err != nil {
		return CredentialStore{}, true, &CredentialStoreDecryptError{
			Path:   path,
			Reason: "check the configured credentials key or credential-store contents",
		}
	}

	var store CredentialStore
	if err := json.Unmarshal(plaintext, &store); err != nil {
		return CredentialStore{}, true, fmt.Errorf("decode decrypted credential store: %w", err)
	}
	store.normalize()
	return store, true, nil
}

func credentialStoreAEAD(secret string) (cipher.AEAD, error) {
	key := deriveCredentialStoreKey(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("build credential store cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build credential store gcm: %w", err)
	}
	return aead, nil
}

func deriveCredentialStoreKey(secret string) [32]byte {
	if decoded, ok := decode32ByteCredentialKey(secret); ok {
		var key [32]byte
		copy(key[:], decoded)
		return key
	}
	return sha256.Sum256([]byte(secret))
}

func decode32ByteCredentialKey(secret string) ([]byte, bool) {
	trimmed := secret
	for _, decoder := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	} {
		decoded, err := decoder(trimmed)
		if err == nil && len(decoded) == 32 {
			return decoded, true
		}
	}
	return nil, false
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
