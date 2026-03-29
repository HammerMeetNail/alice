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

func (r *Runtime) stateOptions() (StateOptions, error) {
	secret, err := loadOptionalSecret("edge state encryption key", r.cfg.CredentialsKeyEnvVar(), r.cfg.CredentialsKeyFile())
	if err != nil {
		return StateOptions{}, err
	}
	return StateOptions{EncryptionSecret: secret}, nil
}

func (r *Runtime) loadState() (State, error) {
	opts, err := r.stateOptions()
	if err != nil {
		return State{}, err
	}
	return LoadStateWithOptions(r.cfg.StatePath(), opts)
}

func (r *Runtime) saveState(state State) error {
	opts, err := r.stateOptions()
	if err != nil {
		return err
	}
	return SaveStateWithOptions(r.cfg.StatePath(), state, opts)
}

func (r *Runtime) RunOnce(ctx context.Context) (Report, error) {
	state, err := r.loadState()
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

	if err := r.saveState(state); err != nil {
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

	beginBody := map[string]any{
		"org_slug":    r.cfg.Agent.OrgSlug,
		"owner_email": r.cfg.Agent.OwnerEmail,
		"agent_name":  r.cfg.Agent.AgentName,
		"client_type": r.cfg.Agent.ClientType,
		"public_key":  state.PublicKey,
	}
	if r.cfg.Agent.InviteToken != "" {
		beginBody["invite_token"] = r.cfg.Agent.InviteToken
	}
	challenge, err := r.client.BeginRegistration(ctx, beginBody)
	if err != nil {
		return false, err
	}

	if challenge.FirstInviteToken != "" {
		fmt.Fprintf(os.Stderr,
			"[alice] org invite token (save now; shown once): %s\n"+
				"[alice] share this token with teammates registering to the same org.\n",
			challenge.FirstInviteToken,
		)
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

	if response.Status == "pending_email_verification" {
		fmt.Fprintf(os.Stderr,
			"[alice] email verification required for %s\n"+
				"[alice] a 6-digit code has been sent to the registered email address.\n"+
				"[alice] submit the code via: POST /v1/agents/verify-email {\"code\": \"<6-digit-code>\"}\n"+
				"[alice] or use the verify_email MCP tool.\n",
			response.AgentID,
		)
	}
	if response.Status == "pending_admin_approval" {
		fmt.Fprintf(os.Stderr,
			"[alice] awaiting org admin approval for %s\n"+
				"[alice] ask an org admin to call MCP tool `review_agent` with:\n"+
				"[alice]   agent_id: %s\n"+
				"[alice]   decision: approved\n"+
				"[alice]   confirm: true\n"+
				"[alice] or via HTTP:\n"+
				"[alice]   curl -X POST \"$ALICE_SERVER_URL/v1/orgs/agents/%s/review\" \\\n"+
				"[alice]     -H \"Authorization: Bearer $ADMIN_TOKEN\" \\\n"+
				"[alice]     -H \"Content-Type: application/json\" \\\n"+
				"[alice]     -d '{\"decision\":\"approved\",\"reason\":\"ok\"}'\n",
			response.AgentID,
			response.AgentID,
			response.AgentID,
		)
	}

	return true, nil
}

func (r *Runtime) publishArtifacts(ctx context.Context, state *State, credentials CredentialStore, registrationPerformed *bool) ([]PublishedArtifact, []string, error) {
	artifacts, cursorUpdates, err := r.configuredArtifacts(ctx, state, credentials)
	if err != nil {
		return nil, nil, err
	}
	return r.publishArtifactBatch(ctx, state, artifacts, cursorUpdates, registrationPerformed)
}

func (r *Runtime) publishArtifactBatch(ctx context.Context, state *State, artifacts []core.Artifact, cursorUpdates map[string]time.Time, registrationPerformed *bool) ([]PublishedArtifact, []string, error) {
	if len(artifacts) == 0 {
		applyCursorUpdates(state, cursorUpdates)
		return nil, nil, nil
	}

	published := make([]PublishedArtifact, 0)
	skipped := make([]string, 0)
	for _, artifact := range artifacts {
		derivationKey := artifactDerivationKey(artifact)
		if derivationKey != "" {
			if previousArtifactID := state.LatestDerivedArtifacts[derivationKey]; strings.TrimSpace(previousArtifactID) != "" {
				previousArtifactID := previousArtifactID
				artifact.SupersedesArtifactID = &previousArtifactID
			}
		}

		digest, err := artifactContentDigest(artifact)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := state.PublishedArtifacts[digest]; ok {
			skipped = append(skipped, digest)
			if derivationKey != "" && strings.TrimSpace(state.LatestDerivedArtifacts[derivationKey]) == "" {
				state.LatestDerivedArtifacts[derivationKey] = state.PublishedArtifacts[digest]
			}
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
		if derivationKey != "" {
			state.LatestDerivedArtifacts[derivationKey] = response.ArtifactID
		}
		published = append(published, PublishedArtifact{
			ArtifactID: response.ArtifactID,
			Digest:     digest,
		})
	}
	applyCursorUpdates(state, cursorUpdates)
	return published, skipped, nil
}

func (r *Runtime) configuredArtifacts(ctx context.Context, state *State, credentials CredentialStore) ([]core.Artifact, map[string]time.Time, error) {
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

func artifactContentDigest(artifact core.Artifact) (string, error) {
	artifact.SupersedesArtifactID = nil
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
