package edge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"alice/internal/core"
)

type Runtime struct {
	cfg    Config
	client *Client
}

type Report struct {
	AgentID               string              `json:"agent_id"`
	OrgID                 string              `json:"org_id"`
	RegistrationPerformed bool                `json:"registration_performed"`
	TokenExpiresAt        time.Time           `json:"token_expires_at"`
	PublishedArtifacts    []PublishedArtifact `json:"published_artifacts,omitempty"`
	SkippedFixtures       []string            `json:"skipped_fixtures,omitempty"`
	QueryResults          []map[string]any    `json:"query_results,omitempty"`
	IncomingRequests      []map[string]any    `json:"incoming_requests,omitempty"`
}

type PublishedArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Digest     string `json:"digest"`
}

type fixtureFile struct {
	Artifacts []core.Artifact `json:"artifacts"`
}

func NewRuntime(cfg Config) *Runtime {
	return &Runtime{
		cfg:    cfg,
		client: NewClient(cfg.Server.BaseURL),
	}
}

func (r *Runtime) RunOnce(ctx context.Context) (Report, error) {
	state, err := LoadState(r.cfg.StatePath())
	if err != nil {
		return Report{}, err
	}
	if err := r.ensureKeyMaterial(&state); err != nil {
		return Report{}, err
	}
	credentials, err := r.prepareCredentialStore(ctx, &state)
	if err != nil {
		return Report{}, err
	}

	report := Report{}
	registrationPerformed, err := r.ensureSession(ctx, &state)
	if err != nil {
		return Report{}, err
	}
	report.RegistrationPerformed = registrationPerformed
	report.AgentID = state.AgentID
	report.OrgID = state.OrgID
	report.TokenExpiresAt = state.TokenExpiresAt

	published, skipped, err := r.publishArtifacts(ctx, &state, credentials, &report.RegistrationPerformed)
	if err != nil {
		return Report{}, err
	}
	report.PublishedArtifacts = published
	report.SkippedFixtures = skipped

	queryResults, err := r.loadQueryResults(ctx, &state, &report.RegistrationPerformed)
	if err != nil {
		return Report{}, err
	}
	report.QueryResults = queryResults

	if r.cfg.Runtime.PollIncomingRequests {
		incoming, err := r.loadIncomingRequests(ctx, &state, &report.RegistrationPerformed)
		if err != nil {
			return Report{}, err
		}
		report.IncomingRequests = incoming
	}

	if err := SaveState(r.cfg.StatePath(), state); err != nil {
		return Report{}, err
	}
	return report, nil
}

func (r *Runtime) ensureKeyMaterial(state *State) error {
	if strings.TrimSpace(state.PublicKey) != "" && strings.TrimSpace(state.PrivateKey) != "" {
		return core.ValidateAgentRegistration(
			r.cfg.Agent.OrgSlug,
			r.cfg.Agent.OwnerEmail,
			r.cfg.Agent.AgentName,
			r.cfg.Agent.ClientType,
			state.PublicKey,
		)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate Ed25519 keypair: %w", err)
	}
	state.PublicKey = base64.StdEncoding.EncodeToString(publicKey)
	state.PrivateKey = base64.StdEncoding.EncodeToString(privateKey)

	return core.ValidateAgentRegistration(
		r.cfg.Agent.OrgSlug,
		r.cfg.Agent.OwnerEmail,
		r.cfg.Agent.AgentName,
		r.cfg.Agent.ClientType,
		state.PublicKey,
	)
}

func (r *Runtime) ensureSession(ctx context.Context, state *State) (bool, error) {
	if strings.TrimSpace(state.AccessToken) != "" && time.Now().UTC().Before(state.TokenExpiresAt.Add(-30*time.Second)) {
		return false, nil
	}

	challenge, err := r.client.BeginRegistration(ctx, map[string]any{
		"org_slug":     r.cfg.Agent.OrgSlug,
		"owner_email":  r.cfg.Agent.OwnerEmail,
		"agent_name":   r.cfg.Agent.AgentName,
		"client_type":  r.cfg.Agent.ClientType,
		"public_key":   state.PublicKey,
		"capabilities": r.cfg.Agent.Capabilities,
	})
	if err != nil {
		return false, err
	}

	privateKeyBytes, err := decodeBase64(state.PrivateKey)
	if err != nil {
		return false, fmt.Errorf("decode private key: %w", err)
	}
	if len(privateKeyBytes) != ed25519.PrivateKeySize {
		return false, fmt.Errorf("stored private key has invalid length")
	}

	signature := ed25519.Sign(ed25519.PrivateKey(privateKeyBytes), []byte(challenge.Challenge))
	response, err := r.client.CompleteRegistration(ctx, challenge.ChallengeID, base64.StdEncoding.EncodeToString(signature))
	if err != nil {
		return false, err
	}

	state.AgentID = response.AgentID
	state.OrgID = response.OrgID
	state.AccessToken = response.AccessToken
	state.TokenExpiresAt = response.ExpiresAt
	return true, nil
}

func (r *Runtime) publishArtifacts(ctx context.Context, state *State, credentials CredentialStore, registrationPerformed *bool) ([]PublishedArtifact, []string, error) {
	artifacts, cursorUpdates, err := r.configuredArtifacts(ctx, *state, credentials)
	if err != nil {
		return nil, nil, err
	}
	if len(artifacts) == 0 {
		applyCursorUpdates(state, cursorUpdates)
		return nil, nil, nil
	}

	published := make([]PublishedArtifact, 0)
	skipped := make([]string, 0)
	for _, artifact := range artifacts {
		digest, err := artifactDigest(artifact)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := state.PublishedArtifacts[digest]; ok {
			skipped = append(skipped, digest)
			continue
		}

		var response PublishArtifactResponse
		err = r.withSession(ctx, state, registrationPerformed, func(token string) error {
			var callErr error
			response, callErr = r.client.PublishArtifact(ctx, token, artifact)
			return callErr
		})
		if err != nil {
			return nil, nil, err
		}

		state.PublishedArtifacts[digest] = response.ArtifactID
		published = append(published, PublishedArtifact{
			ArtifactID: response.ArtifactID,
			Digest:     digest,
		})
	}
	applyCursorUpdates(state, cursorUpdates)
	return published, skipped, nil
}

func (r *Runtime) configuredArtifacts(ctx context.Context, state State, credentials CredentialStore) ([]core.Artifact, map[string]time.Time, error) {
	artifacts := make([]core.Artifact, 0)

	fixturePath := r.cfg.ArtifactFixturePath()
	if fixturePath != "" {
		fixtures, err := loadFixtureFile(fixturePath)
		if err != nil {
			return nil, nil, err
		}
		artifacts = append(artifacts, fixtures.Artifacts...)
	}

	derivedArtifacts, cursorUpdates, err := loadConnectorArtifacts(ctx, r.cfg, state, credentials)
	if err != nil {
		return nil, nil, err
	}
	artifacts = append(artifacts, derivedArtifacts...)

	return artifacts, cursorUpdates, nil
}

func (r *Runtime) loadQueryResults(ctx context.Context, state *State, registrationPerformed *bool) ([]map[string]any, error) {
	results := make([]map[string]any, 0, len(r.cfg.Runtime.QueryWatchIDs))
	for _, queryID := range r.cfg.Runtime.QueryWatchIDs {
		queryID = strings.TrimSpace(queryID)
		if queryID == "" {
			continue
		}

		var payload map[string]any
		err := r.withSession(ctx, state, registrationPerformed, func(token string) error {
			var callErr error
			payload, callErr = r.client.GetQueryResult(ctx, token, queryID)
			return callErr
		})
		if err != nil {
			return nil, err
		}
		results = append(results, payload)
	}
	return results, nil
}

func (r *Runtime) loadIncomingRequests(ctx context.Context, state *State, registrationPerformed *bool) ([]map[string]any, error) {
	var payload map[string]any
	err := r.withSession(ctx, state, registrationPerformed, func(token string) error {
		var callErr error
		payload, callErr = r.client.ListIncomingRequests(ctx, token)
		return callErr
	})
	if err != nil {
		return nil, err
	}

	rawRequests, ok := payload["requests"]
	if !ok {
		return nil, nil
	}

	var requestsList []map[string]any
	if err := decodeInto(rawRequests, &requestsList); err != nil {
		return nil, fmt.Errorf("decode incoming requests: %w", err)
	}
	return requestsList, nil
}

func (r *Runtime) withSession(ctx context.Context, state *State, registrationPerformed *bool, call func(accessToken string) error) error {
	if err := call(state.AccessToken); err == nil {
		return nil
	} else if !errorsIsUnauthorized(err) {
		return err
	}

	state.AccessToken = ""
	state.TokenExpiresAt = time.Time{}
	performed, err := r.ensureSession(ctx, state)
	if err != nil {
		return err
	}
	if performed {
		*registrationPerformed = true
	}
	return call(state.AccessToken)
}

func loadFixtureFile(path string) (fixtureFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fixtureFile{}, fmt.Errorf("read fixture file: %w", err)
	}

	var fixtures fixtureFile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		return fixtureFile{}, fmt.Errorf("decode fixture file: %w", err)
	}
	return fixtures, nil
}

func artifactDigest(artifact core.Artifact) (string, error) {
	data, err := json.Marshal(artifact)
	if err != nil {
		return "", fmt.Errorf("marshal artifact for digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func decodeInto(value any, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func decodeBase64(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(trimmed)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("decode base64 value")
}

func errorsIsUnauthorized(err error) bool {
	return err == ErrUnauthorized
}

func applyCursorUpdates(state *State, cursorUpdates map[string]time.Time) {
	for key, value := range cursorUpdates {
		state.SetCursorTime(key, value)
	}
}
