// Package cli implements the alice command-line interface. The CLI is the
// primary surface for both human operators and non-MCP agents (Codex, Gemini
// CLI, plain shell scripts); the MCP server is a secondary adapter over the
// same coordination server HTTP API.
package cli

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
	"time"
)

// EnvEncryptStateKey is the environment variable that, when set, enables
// AES-256-GCM encryption of the private key and bearer token inside the CLI
// state file. The value is used as the encryption secret; any non-empty string
// is accepted and SHA-256-derived into a 32-byte key (a 32-byte base64 value
// is used directly). Encryption is opt-in: when the variable is not set the
// state file is stored as plaintext JSON, protected only by file permissions.
const EnvEncryptStateKey = "ALICE_ENCRYPT_STATE_KEY"

const cliStateSecretsAAD = "alice.cli.state.secrets.v1"

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
	PrivateKey     string    `json:"private_key,omitempty"`
	AccessToken    string    `json:"access_token,omitempty"`
	TokenExpiresAt time.Time `json:"token_expires_at"`

	// EncryptedSecrets is populated when ALICE_ENCRYPT_STATE_KEY is set.
	// When present, PrivateKey and AccessToken are empty in the JSON; the
	// real values are recovered by decrypting this envelope on load.
	EncryptedSecrets *cliEncryptedSecretsEnvelope `json:"encrypted_secrets,omitempty"`
}

type cliStateSecrets struct {
	PrivateKey  string `json:"private_key"`
	AccessToken string `json:"access_token"`
}

type cliEncryptedSecretsEnvelope struct {
	Format     string `json:"format"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
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
//
// When the file contains an encrypted_secrets block and ALICE_ENCRYPT_STATE_KEY
// is set, the private key and access token are decrypted transparently.
func LoadState(path string) (State, error) {
	encKey := os.Getenv(EnvEncryptStateKey)
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
	if s.EncryptedSecrets != nil {
		if encKey == "" {
			return State{}, fmt.Errorf("state file %q contains encrypted secrets; set %s to decrypt", path, EnvEncryptStateKey)
		}
		secrets, err := decryptCLIStateSecrets(path, s.EncryptedSecrets, encKey)
		if err != nil {
			return State{}, err
		}
		s.PrivateKey = secrets.PrivateKey
		s.AccessToken = secrets.AccessToken
		s.EncryptedSecrets = nil
	}
	return s, nil
}

// SaveState atomically writes the state file with 0600 permissions on a 0700
// parent directory.
//
// When ALICE_ENCRYPT_STATE_KEY is set, the private key and bearer token are
// encrypted with AES-256-GCM before writing. When the variable is not set the
// file is written as plaintext JSON (existing behaviour).
func SaveState(path string, s State) error {
	encKey := os.Getenv(EnvEncryptStateKey)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	if encKey != "" {
		envelope, err := encryptCLIStateSecrets(cliStateSecrets{
			PrivateKey:  s.PrivateKey,
			AccessToken: s.AccessToken,
		}, encKey)
		if err != nil {
			return fmt.Errorf("encrypt state secrets: %w", err)
		}
		s.PrivateKey = ""
		s.AccessToken = ""
		s.EncryptedSecrets = envelope
	} else {
		s.EncryptedSecrets = nil
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

// --- AES-256-GCM helpers (mirrors internal/edge encryption) ---

func encryptCLIStateSecrets(secrets cliStateSecrets, encryptionSecret string) (*cliEncryptedSecretsEnvelope, error) {
	plaintext, err := json.Marshal(secrets)
	if err != nil {
		return nil, fmt.Errorf("marshal state secrets: %w", err)
	}
	aead, err := cliStateAEAD(encryptionSecret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate state secrets nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte(cliStateSecretsAAD))
	return &cliEncryptedSecretsEnvelope{
		Format:     "encrypted",
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func decryptCLIStateSecrets(path string, envelope *cliEncryptedSecretsEnvelope, encryptionSecret string) (cliStateSecrets, error) {
	if envelope.Format != "encrypted" {
		return cliStateSecrets{}, fmt.Errorf("state file %q has unknown secrets format %q", path, envelope.Format)
	}
	if envelope.Algorithm != "aes-256-gcm" {
		return cliStateSecrets{}, fmt.Errorf("state file %q uses unsupported secrets algorithm %q", path, envelope.Algorithm)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return cliStateSecrets{}, fmt.Errorf("decode state secrets nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return cliStateSecrets{}, fmt.Errorf("decode state secrets ciphertext: %w", err)
	}
	aead, err := cliStateAEAD(encryptionSecret)
	if err != nil {
		return cliStateSecrets{}, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(cliStateSecretsAAD))
	if err != nil {
		return cliStateSecrets{}, fmt.Errorf("decrypt state secrets in %q: wrong key or corrupted file", path)
	}
	var secrets cliStateSecrets
	if err := json.Unmarshal(plaintext, &secrets); err != nil {
		return cliStateSecrets{}, fmt.Errorf("decode decrypted state secrets: %w", err)
	}
	return secrets, nil
}

func cliStateAEAD(secret string) (cipher.AEAD, error) {
	key := deriveCLIStateKey(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("build state cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("build state gcm: %w", err)
	}
	return aead, nil
}

func deriveCLIStateKey(secret string) [32]byte {
	// Accept a 32-byte base64-encoded key directly; otherwise SHA-256-derive one.
	for _, decoder := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	} {
		decoded, err := decoder(secret)
		if err == nil && len(decoded) == 32 {
			var key [32]byte
			copy(key[:], decoded)
			return key
		}
	}
	return sha256.Sum256([]byte(secret))
}
