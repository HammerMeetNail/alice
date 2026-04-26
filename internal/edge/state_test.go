package edge

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alice/internal/core"
)

func testEncryptionSecret(t *testing.T) string {
	t.Helper()
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatalf("rand read: %v", err)
	}
	key, err := hkdf.Key(sha256.New, master, nil, "alice.edge.credential-store.v1", 32)
	if err != nil {
		t.Fatalf("hkdf.Key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// --- encryptStateSecrets + decryptStateSecrets ---

func TestEncryptDecryptStateSecrets_RoundTrip(t *testing.T) {
	secret := testEncryptionSecret(t)
	orig := stateSecrets{PrivateKey: "pk", AccessToken: "at"}

	envelope, err := encryptStateSecrets(orig, secret)
	if err != nil {
		t.Fatalf("encryptStateSecrets: %v", err)
	}

	decrypted, err := decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: secret})
	if err != nil {
		t.Fatalf("decryptStateSecrets: %v", err)
	}
	if decrypted.PrivateKey != "pk" || decrypted.AccessToken != "at" {
		t.Fatalf("decrypted = %+v, want pk/at", decrypted)
	}
}

func TestDecryptStateSecrets_WrongFormat(t *testing.T) {
	envelope := &encryptedStateSecretsEnvelope{Format: "plain"}
	_, err := decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: "key"})
	if err == nil || !strings.Contains(err.Error(), "unknown secrets format") {
		t.Fatalf("expected 'unknown secrets format' error, got: %v", err)
	}
}

func TestDecryptStateSecrets_MissingSecret(t *testing.T) {
	envelope := &encryptedStateSecretsEnvelope{Format: "encrypted"}
	_, err := decryptStateSecrets("test.json", envelope, StateOptions{})
	if err == nil || !strings.Contains(err.Error(), "set ALICE_EDGE_CREDENTIAL_KEY") {
		t.Fatalf("expected 'set ALICE_EDGE_CREDENTIAL_KEY' error, got: %v", err)
	}
}

func TestDecryptStateSecrets_WrongAlgorithm(t *testing.T) {
	envelope := &encryptedStateSecretsEnvelope{Format: "encrypted", Algorithm: "rot13"}
	_, err := decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: "key"})
	if err == nil || !strings.Contains(err.Error(), "unsupported secrets algorithm") {
		t.Fatalf("expected 'unsupported secrets algorithm' error, got: %v", err)
	}
}

func TestDecryptStateSecrets_BadNonceBase64(t *testing.T) {
	envelope := &encryptedStateSecretsEnvelope{Format: "encrypted", Algorithm: "aes-256-gcm", Nonce: "!!!bad!!!"}
	_, err := decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: "key"})
	if err == nil || !strings.Contains(err.Error(), "decode state secrets nonce") {
		t.Fatalf("expected 'decode state secrets nonce' error, got: %v", err)
	}
}

func TestDecryptStateSecrets_BadCiphertextBase64(t *testing.T) {
	secret := testEncryptionSecret(t)
	envelope := &encryptedStateSecretsEnvelope{Format: "encrypted", Algorithm: "aes-256-gcm", Nonce: base64.StdEncoding.EncodeToString(make([]byte, 12)), Ciphertext: "!!!bad!!!"}
	_, err := decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: secret})
	if err == nil || !strings.Contains(err.Error(), "decode state secrets ciphertext") {
		t.Fatalf("expected 'decode state secrets ciphertext' error, got: %v", err)
	}
}

func TestDecryptStateSecrets_WrongKey(t *testing.T) {
	secret1 := testEncryptionSecret(t)
	secret2 := testEncryptionSecret(t)
	orig := stateSecrets{PrivateKey: "pk", AccessToken: "at"}
	envelope, err := encryptStateSecrets(orig, secret1)
	if err != nil {
		t.Fatalf("encryptStateSecrets: %v", err)
	}
	_, err = decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: secret2})
	if err == nil || !strings.Contains(err.Error(), "wrong key or corrupted file") {
		t.Fatalf("expected 'wrong key or corrupted file' error, got: %v", err)
	}
}

func TestDecryptStateSecrets_CorruptedJSON(t *testing.T) {
	secret := testEncryptionSecret(t)
	aead, err := credentialStoreAEAD(secret)
	if err != nil {
		t.Fatalf("credentialStoreAEAD: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand read: %v", err)
	}
	plaintext := []byte("{not json")
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte(stateSecretsAAD))
	envelope := &encryptedStateSecretsEnvelope{
		Format:     "encrypted",
		Algorithm:  "aes-256-gcm",
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	_, err = decryptStateSecrets("test.json", envelope, StateOptions{EncryptionSecret: secret})
	if err == nil || !strings.Contains(err.Error(), "decode decrypted state secrets") {
		t.Fatalf("expected 'decode decrypted state secrets' error, got: %v", err)
	}
}

// --- SaveStateWithOptions ---

func TestSaveStateWithOptions_PlaintextAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	state := State{
		AgentID:    "agent-1",
		OrgID:      "org-1",
		PublicKey:  "pub",
		PrivateKey: "priv",
		AccessToken: "tok",
	}
	err := SaveStateWithOptions(path, state, StateOptions{AllowPlaintext: true})
	if err != nil {
		t.Fatalf("SaveStateWithOptions: %v", err)
	}

	saved, err := LoadStateWithOptions(path, StateOptions{AllowPlaintext: true})
	if err != nil {
		t.Fatalf("LoadStateWithOptions: %v", err)
	}
	if saved.AgentID != "agent-1" || saved.PrivateKey != "priv" || saved.AccessToken != "tok" {
		t.Fatalf("loaded state mismatch: %+v", saved)
	}
}

func TestSaveStateWithOptions_Encryption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	secret := testEncryptionSecret(t)
	state := State{
		AgentID:    "agent-1",
		OrgID:      "org-1",
		PublicKey:  "pub",
		PrivateKey: "priv",
		AccessToken: "tok",
	}
	err := SaveStateWithOptions(path, state, StateOptions{EncryptionSecret: secret})
	if err != nil {
		t.Fatalf("SaveStateWithOptions: %v", err)
	}

	saved, err := LoadStateWithOptions(path, StateOptions{EncryptionSecret: secret})
	if err != nil {
		t.Fatalf("LoadStateWithOptions: %v", err)
	}
	if saved.AgentID != "agent-1" || saved.PrivateKey != "priv" || saved.AccessToken != "tok" {
		t.Fatalf("loaded state mismatch: %+v", saved)
	}
}

func TestSaveStateWithOptions_RequiresEncryptionOrAllowPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	state := State{
		AgentID:    "agent-1",
		OrgID:      "org-1",
		PrivateKey: "priv",
		AccessToken: "tok",
	}
	err := SaveStateWithOptions(path, state, StateOptions{})
	if err == nil || !strings.Contains(err.Error(), "plaintext credentials") {
		t.Fatalf("expected plaintext credentials error, got: %v", err)
	}
}

func TestSaveState_ClearsConnectorCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	state := State{
		AgentID:    "agent-1",
		OrgID:      "org-1",
		PublicKey:  "pub",
		PrivateKey: "priv",
		AccessToken: "tok",
		ConnectorCredentials: map[string]ConnectorCredential{
			"github": {AccessToken: "gh-tok"},
		},
	}
	err := SaveStateWithOptions(path, state, StateOptions{AllowPlaintext: true})
	if err != nil {
		t.Fatalf("SaveStateWithOptions: %v", err)
	}

	saved, err := LoadStateWithOptions(path, StateOptions{AllowPlaintext: true})
	if err != nil {
		t.Fatalf("LoadStateWithOptions: %v", err)
	}
	if len(saved.ConnectorCredentials) != 0 {
		t.Fatalf("expected empty connector credentials after save, got %v", saved.ConnectorCredentials)
	}
}

// --- LoadState ---

func TestLoadState_MissingFile(t *testing.T) {
	state, err := LoadState("/nonexistent/path/state.json")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.PublishedArtifacts == nil {
		t.Fatal("expected non-nil PublishedArtifacts for missing file")
	}
}

func TestLoadState_FileNotFoundIsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	state, err := LoadStateWithOptions(path, StateOptions{AllowPlaintext: true})
	if err != nil {
		t.Fatalf("LoadStateWithOptions for missing file should return empty state, got: %v", err)
	}
	if state.PublishedArtifacts == nil {
		t.Fatal("expected non-nil PublishedArtifacts in empty state")
	}
}

func TestLoadState_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatalf("write bad state: %v", err)
	}
	_, err := LoadStateWithOptions(path, StateOptions{AllowPlaintext: true})
	if err == nil || !strings.Contains(err.Error(), "decode state") {
		t.Fatalf("expected 'decode state' error, got: %v", err)
	}
}

// --- ConnectorWatch / SetConnectorWatch ---

func TestConnectorWatch_NoKey(t *testing.T) {
	state := State{}
	got := state.ConnectorWatch("gcal:primary")
	if got.ConnectorType != "" {
		t.Fatalf("expected empty watch, got %+v", got)
	}
}

func TestSetConnectorWatch_EmptyKeyReturnsEarly(t *testing.T) {
	state := &State{}
	state.SetConnectorWatch("", ConnectorWatchState{ConnectorType: "gcal"})
	if len(state.ConnectorWatches) != 0 {
		t.Fatalf("expected no watches for empty key, got %d", len(state.ConnectorWatches))
	}
}

func TestSetGetConnectorWatch(t *testing.T) {
	state := &State{}
	watch := ConnectorWatchState{
		ConnectorType: "gcal",
		ResourceID:    "res-1",
		ChannelID:     "ch-1",
	}
	state.SetConnectorWatch("gcal:primary", watch)
	got := state.ConnectorWatch("gcal:primary")
	if got.ConnectorType != "gcal" || got.ResourceID != "res-1" || got.ChannelID != "ch-1" {
		t.Fatalf("ConnectorWatch mismatch: got %+v", got)
	}
	missing := state.ConnectorWatch("gcal:missing")
	if missing.ConnectorType != "" {
		t.Fatalf("expected empty watch for missing key, got %+v", missing)
	}
}

// --- normalizePublishedArtifacts ---

func TestNormalizePublishedArtifacts_NilMaps(t *testing.T) {
	state := &State{}
	state.normalizePublishedArtifacts()
	if state.PublishedArtifacts == nil {
		t.Fatal("PublishedArtifacts should be non-nil after normalize")
	}
	if state.ConnectorCursors == nil {
		t.Fatal("ConnectorCursors should be non-nil after normalize")
	}
	if state.ProcessedWebhookKeys == nil {
		t.Fatal("ProcessedWebhookKeys should be non-nil after normalize")
	}
	if state.WebhookSequenceNumbers == nil {
		t.Fatal("WebhookSequenceNumbers should be non-nil after normalize")
	}
	if state.LatestDerivedArtifacts == nil {
		t.Fatal("LatestDerivedArtifacts should be non-nil after normalize")
	}
	if state.ProjectSignalStates == nil {
		t.Fatal("ProjectSignalStates should be non-nil after normalize")
	}
	if state.ConnectorCredentials == nil {
		t.Fatal("ConnectorCredentials should be non-nil after normalize")
	}
	if state.PendingConnectorAuths == nil {
		t.Fatal("PendingConnectorAuths should be non-nil after normalize")
	}
	if state.ConnectorWatches == nil {
		t.Fatal("ConnectorWatches should be non-nil after normalize")
	}
}

func TestNormalizePublishedArtifacts_ExistingMaps(t *testing.T) {
	cursors := map[string]string{"a": "b"}
	state := &State{ConnectorCursors: cursors}
	state.normalizePublishedArtifacts()
	if state.PublishedArtifacts == nil {
		t.Fatal("PublishedArtifacts should be non-nil after normalize")
	}
	if state.ConnectorCursors["a"] != "b" {
		t.Fatal("existing cursor data should be preserved")
	}
}

func TestNormalizePublishedArtifacts_PreservesPublishedFixturesMapped(t *testing.T) {
	state := &State{
		PublishedFixtures: map[string]string{"digest-a": "artifact-1"},
		PublishedArtifacts: nil,
	}
	state.normalizePublishedArtifacts()
	if state.PublishedArtifacts["digest-a"] != "artifact-1" {
		t.Fatalf("PublishedArtifacts should include PublishedFixtures entries; got %v", state.PublishedArtifacts)
	}
}

func TestNormalizePublishedArtifacts_DoesNotOverwriteExisting(t *testing.T) {
	state := &State{
		PublishedFixtures: map[string]string{"digest-a": "fixture-1"},
		PublishedArtifacts: map[string]string{"digest-a": "existing"},
	}
	state.normalizePublishedArtifacts()
	if state.PublishedArtifacts["digest-a"] != "existing" {
		t.Fatalf("PublishedArtifacts should not be overwritten by fixture; got %v", state.PublishedArtifacts)
	}
}

// --- CursorTime ---

func TestCursorTime_NoKey(t *testing.T) {
	state := State{}
	got := state.CursorTime("missing")
	if !got.IsZero() {
		t.Fatalf("expected zero time for missing cursor, got %v", got)
	}
}

func TestCursorTime_RFC3339Nano(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	state := State{ConnectorCursors: map[string]string{"test": now.Format(time.RFC3339Nano)}}
	got := state.CursorTime("test")
	if !got.Equal(now) {
		t.Fatalf("CursorTime = %v, want %v", got, now)
	}
}

func TestCursorTime_RFC3339(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	state := State{ConnectorCursors: map[string]string{"test": now.Format(time.RFC3339)}}
	got := state.CursorTime("test")
	if !got.Equal(now) {
		t.Fatalf("CursorTime = %v, want %v", got, now)
	}
}

func TestCursorTime_InvalidFormat(t *testing.T) {
	state := State{ConnectorCursors: map[string]string{"test": "not a time"}}
	got := state.CursorTime("test")
	if !got.IsZero() {
		t.Fatalf("expected zero time for invalid format, got %v", got)
	}
}

// --- LoadState (encrypted secrets path) ---

func TestLoadState_EncryptedSecretsWithPlaintextFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	secret := testEncryptionSecret(t)

	orig := stateSecrets{PrivateKey: "pk-enc", AccessToken: "at-enc"}
	envelope, err := encryptStateSecrets(orig, secret)
	if err != nil {
		t.Fatalf("encryptStateSecrets: %v", err)
	}

	state := State{
		AgentID:          "agent-1",
		OrgID:            "org-1",
		PublicKey:        "pub",
		PrivateKey:       "plain-pk",
		AccessToken:      "plain-at",
		EncryptedSecrets: envelope,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadStateWithOptions(path, StateOptions{EncryptionSecret: secret})
	if err != nil {
		t.Fatalf("LoadStateWithOptions: %v", err)
	}
	if loaded.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", loaded.AgentID)
	}
	if loaded.PrivateKey != "pk-enc" {
		t.Fatalf("PrivateKey = %q, want pk-enc (from encrypted envelope)", loaded.PrivateKey)
	}
	if loaded.AccessToken != "at-enc" {
		t.Fatalf("AccessToken = %q, want at-enc (from encrypted envelope)", loaded.AccessToken)
	}
}

// --- LoadStateWithOptions (encrypted without secret) ---

func TestLoadStateWithOptions_EncryptedWithoutSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	secret := testEncryptionSecret(t)

	orig := stateSecrets{PrivateKey: "pk-enc", AccessToken: "at-enc"}
	envelope, err := encryptStateSecrets(orig, secret)
	if err != nil {
		t.Fatalf("encryptStateSecrets: %v", err)
	}

	state := State{
		AgentID:          "agent-1",
		OrgID:            "org-1",
		EncryptedSecrets: envelope,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = LoadStateWithOptions(path, StateOptions{AllowPlaintext: true})
	if err == nil || !strings.Contains(err.Error(), "set ALICE_EDGE_CREDENTIAL_KEY") {
		t.Fatalf("expected decryption key required error, got: %v", err)
	}
}

// --- State struct JSON round-trip ---

func TestStateJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	orig := State{
		AgentID:      "agent-1",
		OrgID:        "org-1",
		PublicKey:    "pub-key",
		PrivateKey:   "priv-key",
		AccessToken:  "access-token",
		TokenExpiresAt: now,
		PublishedArtifacts: map[string]string{"a": "b"},
		ConnectorCursors: map[string]string{"c": "d"},
		ConnectorWatches: map[string]ConnectorWatchState{
			"gcal:primary": {
				ConnectorType: "gcal",
				ResourceID:    "res-1",
				ChannelID:     "ch-1",
			},
		},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored State
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.AgentID != "agent-1" || restored.OrgID != "org-1" {
		t.Fatalf("basic fields mismatch: %+v", restored)
	}
	if restored.PublishedArtifacts["a"] != "b" {
		t.Fatalf("PublishedArtifacts mismatch: %v", restored.PublishedArtifacts)
	}
	if restored.ConnectorCursors["c"] != "d" {
		t.Fatalf("ConnectorCursors mismatch: %v", restored.ConnectorCursors)
	}
	if w := restored.ConnectorWatches["gcal:primary"]; w.ConnectorType != "gcal" {
		t.Fatalf("ConnectorWatches mismatch: %+v", w)
	}
}

// --- fixtureEventSource Name ---

func TestFixtureEventSourceName(t *testing.T) {
	src := fixtureEventSource{name: "test-fixture"}
	if src.Name() != "test-fixture" {
		t.Fatalf("Name() = %q, want test-fixture", src.Name())
	}
}

// --- filterEventsSinceCursors ---

func TestFilterEventsSinceCursors_EmptyCursors(t *testing.T) {
	now := time.Now()
	state := State{}
	events := []NormalizedEvent{
		{SourceSystem: "github", CursorKey: "github:pr", SourceID: "1", ObservedAt: now},
	}
	fresh, updates := filterEventsSinceCursors(state, events)
	if len(fresh) != 1 {
		t.Fatalf("expected 1 fresh event with empty cursors, got %d", len(fresh))
	}
	if _, ok := updates["github:pr"]; !ok {
		t.Fatalf("expected github:pr cursor key in updates, got %v", updates)
	}
}

func TestFilterEventsSinceCursors_FiltersOldEvents(t *testing.T) {
	now := time.Now()
	past := now.Add(-1 * time.Hour)
	state := State{
		ConnectorCursors: map[string]string{
			"github:pr": now.Format(time.RFC3339Nano),
		},
	}
	events := []NormalizedEvent{
		{SourceSystem: "github", CursorKey: "github:pr", SourceID: "1", ObservedAt: past},
	}
	fresh, _ := filterEventsSinceCursors(state, events)
	if len(fresh) != 0 {
		t.Fatalf("expected 0 fresh events for past event, got %d", len(fresh))
	}
}

func TestFilterEventsSinceCursors_PassesNewEvents(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	now := time.Now()
	state := State{
		ConnectorCursors: map[string]string{
			"github:pr": past.Format(time.RFC3339Nano),
		},
	}
	events := []NormalizedEvent{
		{SourceSystem: "github", CursorKey: "github:pr", SourceID: "2", ObservedAt: now},
	}
	fresh, _ := filterEventsSinceCursors(state, events)
	if len(fresh) != 1 {
		t.Fatalf("expected 1 fresh event after cursor, got %d", len(fresh))
	}
}

// --- mergeCursorUpdates ---

func TestMergeCursorUpdates_NewKeys(t *testing.T) {
	dst := make(map[string]time.Time)
	src := map[string]time.Time{"github": time.Now()}
	mergeCursorUpdates(dst, src)
	if _, ok := dst["github"]; !ok {
		t.Fatal("expected github key in merged map")
	}
}

func TestMergeCursorUpdates_KeepsNewer(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	dst := map[string]time.Time{"github": newer}
	src := map[string]time.Time{"github": older}
	mergeCursorUpdates(dst, src)
	if dst["github"] != newer {
		t.Fatal("mergeCursorUpdates should keep newer time")
	}
}

// --- connectorBackoff ---

func TestConnectorBackoff_ReturnToMax(t *testing.T) {
	for attempt := 0; attempt < 30; attempt++ {
		d := connectorBackoff(attempt)
		if d < 0 {
			t.Fatalf("connectorBackoff(%d) = %v, should not be negative", attempt, d)
		}
		if d == 0 && attempt > 0 {
			t.Fatalf("connectorBackoff(%d) = 0, expected positive", attempt)
		}
		if d > connectorMaxBackoff {
			t.Fatalf("connectorBackoff(%d) = %v, exceeds max %v", attempt, d, connectorMaxBackoff)
		}
	}
}

// --- credentialStoreAEAD ---

func TestCredentialStoreAEAD_EmptySecret(t *testing.T) {
	aead, err := credentialStoreAEAD("")
	if err != nil {
		t.Fatalf("credentialStoreAEAD: %v", err)
	}
	if aead == nil {
		t.Fatal("expected non-nil AEAD even with empty secret")
	}
}

func TestCredentialStoreAEAD_ValidSecret(t *testing.T) {
	secret := testEncryptionSecret(t)
	aead, err := credentialStoreAEAD(secret)
	if err != nil {
		t.Fatalf("credentialStoreAEAD: %v", err)
	}
	if aead == nil {
		t.Fatal("expected non-nil AEAD")
	}
}

// --- decodeBase64 ---

func TestDecodeBase64_Invalid(t *testing.T) {
	_, err := decodeBase64("!!!not base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecodeBase64_Valid(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))
	decoded, err := decodeBase64(encoded)
	if err != nil {
		t.Fatalf("decodeBase64: %v", err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("decodeBase64: got %q, want hello", string(decoded))
	}
}

// --- connectorRetryAfter (takes statusCode int, returns bool) ---

func TestConnectorRetryAfter_Statuses(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{429, true},
		{503, true},
		{502, true},
		{504, true},
		{500, false},
		{200, false},
		{0, false},
	}
	for _, tc := range tests {
		got := connectorRetryAfter(tc.code)
		if got != tc.want {
			t.Errorf("connectorRetryAfter(%d) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// --- parseRetryAfter ---

func TestParseRetryAfter_Valid(t *testing.T) {
	dur := parseRetryAfter("120")
	if dur != 120*time.Second {
		t.Fatalf("parseRetryAfter(120) = %v, want 120s", dur)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	dur := parseRetryAfter("")
	if dur != 0 {
		t.Fatalf("parseRetryAfter('') = %v, want 0", dur)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	dur := parseRetryAfter("not-a-number")
	if dur != 0 {
		t.Fatalf("parseRetryAfter(not-a-number) = %v, want 0", dur)
	}
}

// --- shouldRetryConnectorError ---

func TestShouldRetryConnectorError_NilError(t *testing.T) {
	if shouldRetryConnectorError(nil) {
		t.Fatal("expected false for nil error")
	}
}

func TestShouldRetryConnectorError_NonTimeoutNetError(t *testing.T) {
	netErr := &mockNetError{temporary: true, timeout: false}
	if !shouldRetryConnectorError(netErr) {
		t.Fatal("expected true for temporary non-timeout net.Error")
	}
}

func TestShouldRetryConnectorError_WrappedNetError(t *testing.T) {
	netErr := &mockNetError{timeout: true}
	wrapped := fmt.Errorf("wrap: %w", netErr)
	if !shouldRetryConnectorError(wrapped) {
		t.Fatal("expected true for wrapped net.Error")
	}
}

// --- decodeInto ---

func TestDecodeInto_Valid(t *testing.T) {
	data := map[string]any{"key": "value"}
	var m map[string]string
	if err := decodeInto(data, &m); err != nil {
		t.Fatalf("decodeInto: %v", err)
	}
	if m["key"] != "value" {
		t.Fatalf("decodeInto: expected key=value, got %v", m)
	}
}

func TestDecodeInto_Invalid(t *testing.T) {
	err := decodeInto(make(chan int), &map[string]string{})
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

// --- dedupeStrings ---

func TestDedupeStrings_RemovesDuplicates(t *testing.T) {
	result := dedupeStrings([]string{"a", "b", "a", "c", "b"})
	if len(result) != 3 {
		t.Fatalf("expected 3 unique strings, got %d: %v", len(result), result)
	}
}

func TestDedupeStrings_Empty(t *testing.T) {
	result := dedupeStrings(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty slice for nil input, got %v", result)
	}
}

// --- sameEmail / subtleConstantTimeEqual ---

func TestSameEmail(t *testing.T) {
	if !sameEmail("user@example.com", "User@Example.com") {
		t.Fatal("expected case-insensitive email match")
	}
	if sameEmail("user@example.com", "other@example.com") {
		t.Fatal("expected different emails to not match")
	}
}

func TestSubtleConstantTimeEqual(t *testing.T) {
	if !subtleConstantTimeEqual("abc", "abc") {
		t.Fatal("expected equal")
	}
	if subtleConstantTimeEqual("abc", "abd") {
		t.Fatal("expected not equal")
	}
	if subtleConstantTimeEqual("abc", "ab") {
		t.Fatal("expected not equal for different lengths")
	}
}

// --- errorsIsUnauthorized ---

func TestErrorsIsUnauthorized(t *testing.T) {
	if !errorsIsUnauthorized(ErrUnauthorized) {
		t.Fatal("expected ErrUnauthorized to match")
	}
	if errorsIsUnauthorized(errors.New("other error")) {
		t.Fatal("expected other error to not match")
	}
}

// --- deriveAggregateArtifacts ---

func TestDeriveAggregateArtifacts_Empty(t *testing.T) {
	state := &State{}
	artifacts := deriveAggregateArtifacts(nil, state)
	if len(artifacts) != 0 {
		t.Fatalf("expected 0 aggregate artifacts for nil input, got %d", len(artifacts))
	}
}

// --- projectRefs helpers ---

func TestProjectRefsForRepository(t *testing.T) {
	repo := GitHubRepositoryConfig{
		Name:        "acme/payments",
		ProjectRefs: []string{"project-1"},
	}
	refs := projectRefsForRepository(repo)
	if len(refs) != 1 || refs[0] != "project-1" {
		t.Fatalf("projectRefsForRepository = %v, want [project-1]", refs)
	}
}

func TestProjectRefsForRepository_Empty(t *testing.T) {
	repo := GitHubRepositoryConfig{Name: "acme/payments"}
	refs := projectRefsForRepository(repo)
	// Returns repo name part after "/" as default
	if len(refs) != 1 || refs[0] != "payments" {
		t.Fatalf("expected [payments] for repo, got %v", refs)
	}
}

func TestProjectRefsForRepository_EmptyName(t *testing.T) {
	repo := GitHubRepositoryConfig{Name: ""}
	refs := projectRefsForRepository(repo)
	if len(refs) != 0 {
		t.Fatalf("expected empty refs for empty name, got %v", refs)
	}
}

func TestProjectRefsForJiraProject_WithRefs(t *testing.T) {
	project := JiraProjectConfig{Key: "PROJ", ProjectRefs: []string{"project-1"}}
	refs := projectRefsForJiraProject(project)
	if len(refs) != 1 || refs[0] != "project-1" {
		t.Fatalf("projectRefsForJiraProject = %v, want [project-1]", refs)
	}
}

func TestProjectRefsForJiraProject_Default(t *testing.T) {
	project := JiraProjectConfig{Key: "PROJ"}
	refs := projectRefsForJiraProject(project)
	if len(refs) != 1 || refs[0] != "proj" {
		t.Fatalf("projectRefsForJiraProject default = %v, want [proj]", refs)
	}
}

func TestProjectRefsForCalendar_WithRefs(t *testing.T) {
	cal := GCalCalendarConfig{ID: "primary", ProjectRefs: []string{"project-1"}}
	refs := projectRefsForCalendar(cal)
	if len(refs) != 1 || refs[0] != "project-1" {
		t.Fatalf("projectRefsForCalendar = %v, want [project-1]", refs)
	}
}

func TestProjectRefsForCalendar_Default(t *testing.T) {
	cal := GCalCalendarConfig{ID: "primary"}
	refs := projectRefsForCalendar(cal)
	if len(refs) != 1 || refs[0] != "primary" {
		t.Fatalf("projectRefsForCalendar default = %v, want [primary]", refs)
	}
}

// --- applyCursorUpdates ---

func TestApplyCursorUpdates(t *testing.T) {
	state := &State{}
	updates := map[string]time.Time{
		"github": time.Now(),
	}
	applyCursorUpdates(state, updates)
	if state.ConnectorCursors["github"] == "" {
		t.Fatal("expected github cursor to be set")
	}
}

// --- artifactContentDigest ---

func TestArtifactContentDigest_ProducesDigest(t *testing.T) {
	artifact := core.Artifact{
		ArtifactID: "test-id",
		Content:    "hello",
	}
	digest1, err := artifactContentDigest(artifact)
	if err != nil {
		t.Fatalf("artifactContentDigest: %v", err)
	}
	digest2, err := artifactContentDigest(artifact)
	if err != nil {
		t.Fatalf("artifactContentDigest: %v", err)
	}
	if digest1 != digest2 {
		t.Fatal("same artifact should produce same digest")
	}
}

// --- decode32ByteCredentialKey ---

func TestDecode32ByteCredentialKey_ShortKey(t *testing.T) {
	_, ok := decode32ByteCredentialKey("short")
	if ok {
		t.Fatal("expected short key to not decode")
	}
}

func TestDecode32ByteCredentialKey_ValidBase64(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	decoded, ok := decode32ByteCredentialKey(encoded)
	if !ok || len(decoded) != 32 {
		t.Fatalf("decode32ByteCredentialKey failed for valid key: ok=%v len=%d", ok, len(decoded))
	}
}

// --- deriveCredentialStoreKey ---

func TestDeriveCredentialStoreKey_ArbitraryString(t *testing.T) {
	key := deriveCredentialStoreKey("not-32-bytes")
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key))
	}
}

// --- normalizeLabel ---

func TestNormalizeLabel_UsesFallback(t *testing.T) {
	if got := normalizeLabel("", "fallback"); got != "fallback" {
		t.Fatalf("normalizeLabel('', 'fallback') = %q, want fallback", got)
	}
}

func TestNormalizeLabel_UsesValue(t *testing.T) {
	if got := normalizeLabel("value", "fallback"); got != "value" {
		t.Fatalf("normalizeLabel('value', 'fallback') = %q, want value", got)
	}
}

// --- gcalWatchStateKey / gcalWatchUsable ---

func TestGCalWatchStateKey(t *testing.T) {
	key := gcalWatchStateKey("primary")
	if key != "gcal:watch:primary" {
		t.Fatalf("gcalWatchStateKey = %q, want gcal:watch:primary", key)
	}
}

func TestGCalWatchUsable_NotUsableWhenDifferent(t *testing.T) {
	watch := ConnectorWatchState{
		ConnectorType: "gcal",
		ResourceID:    "res-old",
	}
	if gcalWatchUsable(watch, "https://other.example.com/callback", time.Now()) {
		t.Fatal("expected not usable when callback URL mismatches")
	}
}

// --- isCompletedJiraStatus ---

func TestIsCompletedJiraStatus(t *testing.T) {
	if !isCompletedJiraStatus("done") {
		t.Fatal("expected 'done' to be completed")
	}
	if !isCompletedJiraStatus("closed") {
		t.Fatal("expected 'closed' to be completed")
	}
	if isCompletedJiraStatus("in progress") {
		t.Fatal("expected 'in progress' not to be completed")
	}
}

// --- LoadCredentialStore ---

func TestLoadCredentialStore_MissingFile(t *testing.T) {
	store, err := LoadCredentialStore("/nonexistent/path/creds.json")
	if err != nil {
		t.Fatalf("LoadCredentialStore: %v", err)
	}
	// Missing file returns empty store, not an error
	if len(store.ConnectorCredentials) != 0 {
		t.Fatal("expected empty connector credentials for missing file")
	}
}

func TestSaveLoadCredentialStore_Plaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	store := CredentialStore{}

	err := SaveCredentialStoreWithOptions(path, store, CredentialStoreOptions{})
	if err != nil {
		t.Fatalf("SaveCredentialStoreWithOptions: %v", err)
	}

	loaded, err := LoadCredentialStoreWithOptions(path, CredentialStoreOptions{})
	if err != nil {
		t.Fatalf("LoadCredentialStoreWithOptions: %v", err)
	}
	_ = loaded
}

func TestLoadCredentialStore_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadCredentialStore(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- CredentialStore normalize / SetConnectorCredential / ConnectorCredential ---

func TestCredentialStore_SetGetConnectorCredential(t *testing.T) {
	store := &CredentialStore{}
	cred := ConnectorCredential{AccessToken: "tok", RefreshToken: "ref"}
	store.SetConnectorCredential("github", cred)
	got := store.ConnectorCredential("github")
	if got.AccessToken != "tok" || got.RefreshToken != "ref" {
		t.Fatalf("ConnectorCredential mismatch: got %+v", got)
	}
}

func TestCredentialStore_GetMissingKey(t *testing.T) {
	store := &CredentialStore{}
	got := store.ConnectorCredential("missing")
	if got.AccessToken != "" {
		t.Fatalf("expected empty credential for missing key, got %+v", got)
	}
}

func TestCredentialStore_NormalizeNilMaps(t *testing.T) {
	store := &CredentialStore{}
	store.normalize()
	if store.ConnectorCredentials == nil {
		t.Fatal("expected non-nil ConnectorCredentials after normalize")
	}
}

func TestCredentialStore_MigrateFromState_Empty(t *testing.T) {
	store := &CredentialStore{}
	state := &State{}
	if store.MigrateFromState(state) {
		t.Fatal("expected no migration from empty state")
	}
}

func TestCredentialStore_MigrateFromState_WithCredentials(t *testing.T) {
	store := &CredentialStore{}
	state := &State{
		ConnectorCredentials: map[string]ConnectorCredential{
			"github": {AccessToken: "gh-tok"},
		},
	}
	if !store.MigrateFromState(state) {
		t.Fatal("expected migration from state with credentials")
	}
	if store.ConnectorCredential("github").AccessToken != "gh-tok" {
		t.Fatalf("migrated credential mismatch: got %+v", store.ConnectorCredential("github"))
	}
	if len(state.ConnectorCredentials) != 0 {
		t.Fatal("expected state credentials to be cleared after migration")
	}
}

// --- connectorCredentialNeedsRefresh ---

func TestConnectorCredentialNeedsRefresh_NoToken(t *testing.T) {
	cred := ConnectorCredential{}
	if connectorCredentialNeedsRefresh(cred) {
		t.Fatal("expected no refresh needed when no access token")
	}
}

func TestConnectorCredentialNeedsRefresh_Expired(t *testing.T) {
	cred := ConnectorCredential{
		AccessToken:  "tok",
		ExpiresAt:    time.Now().Add(-1 * time.Hour),
		RefreshToken: "ref",
	}
	if !connectorCredentialNeedsRefresh(cred) {
		t.Fatal("expected needs refresh when expired")
	}
}

func TestConnectorCredentialNeedsRefresh_Valid(t *testing.T) {
	cred := ConnectorCredential{
		AccessToken: "tok",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}
	if connectorCredentialNeedsRefresh(cred) {
		t.Fatal("expected no refresh needed when valid")
	}
}

// --- sourceReferenceForEvent ---

func TestSourceReferenceForEvent_ZeroTime(t *testing.T) {
	evt := NormalizedEvent{
		SourceSystem: "github",
		SourceType:   "pull_request",
		SourceID:     "pr-1",
	}
	ref := sourceReferenceForEvent(evt)
	if ref.SourceSystem != "github" {
		t.Fatalf("SourceSystem = %q, want github", ref.SourceSystem)
	}
}

// --- ConnectorReauthRequiredError ---

func TestConnectorReauthRequiredError_WithReason(t *testing.T) {
	err := &ConnectorReauthRequiredError{ConnectorType: "jira", Reason: "expired"}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Error() should contain 'expired', got: %s", err.Error())
	}
}

// --- CredentialStoreDecryptError ---

func TestCredentialStoreDecryptError_Nominal(t *testing.T) {
	err := &CredentialStoreDecryptError{Path: "/tmp/creds.json", Reason: "decode fail"}
	if !strings.Contains(err.Error(), "/tmp/creds.json") {
		t.Fatalf("Error() should contain path, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "decode fail") {
		t.Fatalf("Error() should contain reason, got: %s", err.Error())
	}
}

func TestCredentialStoreDecryptError_NoReason(t *testing.T) {
	err := &CredentialStoreDecryptError{Path: "/tmp/creds.json"}
	if !strings.Contains(err.Error(), "/tmp/creds.json") {
		t.Fatalf("Error() should contain path, got: %s", err.Error())
	}
}

func TestCredentialStoreDecryptError_NilReceiver(t *testing.T) {
	var err *CredentialStoreDecryptError
	if err.Error() != "" {
		t.Fatalf("Error() on nil receiver should be empty, got: %s", err.Error())
	}
}

// --- CredentialStoreKeyRequiredError ---

func TestCredentialStoreKeyRequiredError_Nominal(t *testing.T) {
	err := &CredentialStoreKeyRequiredError{Path: "/tmp/creds.json", Reason: "missing key"}
	if !strings.Contains(err.Error(), "/tmp/creds.json") {
		t.Fatalf("Error() should contain path, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "missing key") {
		t.Fatalf("Error() should contain reason, got: %s", err.Error())
	}
}

func TestCredentialStoreKeyRequiredError_NilReceiver(t *testing.T) {
	var err *CredentialStoreKeyRequiredError
	if err.Error() != "" {
		t.Fatalf("Error() on nil receiver should be empty, got: %s", err.Error())
	}
}
