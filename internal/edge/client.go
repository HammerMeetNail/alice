package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"alice/internal/core"
)

var ErrUnauthorized = errors.New("unauthorized")

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type RegisterChallengeResponse struct {
	ChallengeID      string    `json:"challenge_id"`
	Challenge        string    `json:"challenge"`
	Algorithm        string    `json:"algorithm"`
	ExpiresAt        time.Time `json:"expires_at"`
	FirstInviteToken string    `json:"first_invite_token,omitempty"`
}

type RegisterResponse struct {
	AgentID     string    `json:"agent_id"`
	OrgID       string    `json:"org_id"`
	Status      string    `json:"status"`
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type PublishArtifactResponse struct {
	ArtifactID string `json:"artifact_id"`
	Stored     bool   `json:"stored"`
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) BeginRegistration(ctx context.Context, input map[string]any) (RegisterChallengeResponse, error) {
	var response RegisterChallengeResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/agents/register/challenge", "", input, &response)
	return response, err
}

func (c *Client) CompleteRegistration(ctx context.Context, challengeID, challengeSignature string) (RegisterResponse, error) {
	var response RegisterResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/agents/register", "", map[string]any{
		"challenge_id":        challengeID,
		"challenge_signature": challengeSignature,
	}, &response)
	return response, err
}

func (c *Client) PublishArtifact(ctx context.Context, accessToken string, artifact core.Artifact) (PublishArtifactResponse, error) {
	var response PublishArtifactResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1/artifacts", accessToken, map[string]any{
		"artifact": artifact,
	}, &response)
	return response, err
}

func (c *Client) GetQueryResult(ctx context.Context, accessToken, queryID string) (map[string]any, error) {
	var response map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/v1/queries/"+queryID, accessToken, nil, &response)
	return response, err
}

func (c *Client) ListIncomingRequests(ctx context.Context, accessToken string) (map[string]any, error) {
	var response map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/v1/requests/incoming", accessToken, nil, &response)
	return response, err
}

func (c *Client) doJSON(ctx context.Context, method, path, accessToken string, body any, out any) error {
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		requestBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}

	if resp.StatusCode >= http.StatusBadRequest {
		var errorPayload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errorPayload)
		if errCode, _ := errorPayload["error"].(string); strings.TrimSpace(errCode) != "" {
			if detail, _ := errorPayload["message"].(string); strings.TrimSpace(detail) != "" {
				return fmt.Errorf("%s: %s", errCode, detail)
			}
			return fmt.Errorf("%s", errCode)
		}
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	if out == nil || resp.ContentLength == 0 {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
