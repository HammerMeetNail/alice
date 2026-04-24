package edge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const gcalWatchRefreshWindow = 15 * time.Minute

type ConnectorWatchReport struct {
	ConnectorType string                 `json:"connector_type"`
	Watches       []ConnectorWatchResult `json:"watches,omitempty"`
}

type ConnectorWatchResult struct {
	ScopeRef    string    `json:"scope_ref"`
	Action      string    `json:"action"`
	ChannelID   string    `json:"channel_id,omitempty"`
	ResourceID  string    `json:"resource_id,omitempty"`
	ResourceURI string    `json:"resource_uri,omitempty"`
	CallbackURL string    `json:"callback_url,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type gcalWatchRequest struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Address string            `json:"address"`
	Token   string            `json:"token,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

type gcalWatchResponse struct {
	ID         string `json:"id"`
	ResourceID string `json:"resourceId"`
	ResourceURI string `json:"resourceUri"`
	Expiration string `json:"expiration"`
}

func (r *Runtime) RegisterConnectorWatch(ctx context.Context, connectorType string) (ConnectorWatchReport, error) {
	switch normalizeLabel(connectorType, "") {
	case "gcal":
		return r.registerGCalWebhookWatches(ctx)
	default:
		return ConnectorWatchReport{}, fmt.Errorf("unsupported connector watch %q", connectorType)
	}
}

func (r *Runtime) registerGCalWebhookWatches(ctx context.Context) (ConnectorWatchReport, error) {
	if !r.cfg.GCalWebhookEnabled() {
		return ConnectorWatchReport{}, fmt.Errorf("gcal webhook intake is not configured")
	}
	callbackURL := r.cfg.GCalWebhookCallbackURL()
	if strings.TrimSpace(callbackURL) == "" {
		return ConnectorWatchReport{}, fmt.Errorf("connectors.gcal.webhook.callback_url is required to register provider watches")
	}

	webhookSecret, err := loadOptionalSecret("gcal webhook", r.cfg.GCalWebhookSecretEnvVar(), r.cfg.GCalWebhookSecretFile())
	if err != nil {
		return ConnectorWatchReport{}, err
	}
	if strings.TrimSpace(webhookSecret) == "" {
		return ConnectorWatchReport{}, fmt.Errorf("gcal webhook secret is required to register provider watches")
	}

	state, err := r.loadState()
	if err != nil {
		return ConnectorWatchReport{}, err
	}
	credentials, err := r.prepareCredentialStore(ctx, &state)
	if err != nil {
		return ConnectorWatchReport{}, err
	}

	source := newGCalLiveSource(r.cfg)
	now := time.Now().UTC()
	results := make([]ConnectorWatchResult, 0, len(r.cfg.Connectors.GCal.Calendars))
	updated := false
	for _, calendar := range r.cfg.Connectors.GCal.Calendars {
		watchKey := gcalWatchStateKey(calendar.ID)
		existing := state.ConnectorWatch(watchKey)
		if gcalWatchUsable(existing, callbackURL, now) {
			results = append(results, ConnectorWatchResult{
				ScopeRef:    calendar.ID,
				Action:      "reused",
				ChannelID:   existing.ChannelID,
				ResourceID:  existing.ResourceID,
				ResourceURI: existing.ResourceURI,
				CallbackURL: existing.CallbackURL,
				ExpiresAt:   existing.ExpiresAt,
			})
			continue
		}

		watch, err := source.RegisterCalendarWatch(
			ctx,
			credentials,
			calendar,
			webhookSecret,
			callbackURL,
			r.cfg.GCalWebhookChannelIDPrefix(),
			r.cfg.GCalWebhookRequestedTTLSeconds(),
		)
		if err != nil {
			return ConnectorWatchReport{}, fmt.Errorf("register gcal watch for %s: %w", calendar.ID, err)
		}
		state.SetConnectorWatch(watchKey, watch)
		updated = true
		results = append(results, ConnectorWatchResult{
			ScopeRef:    calendar.ID,
			Action:      "registered",
			ChannelID:   watch.ChannelID,
			ResourceID:  watch.ResourceID,
			ResourceURI: watch.ResourceURI,
			CallbackURL: watch.CallbackURL,
			ExpiresAt:   watch.ExpiresAt,
		})
	}

	if updated {
		if err := r.saveState(state); err != nil {
			return ConnectorWatchReport{}, err
		}
	}

	return ConnectorWatchReport{
		ConnectorType: "gcal",
		Watches:       results,
	}, nil
}

func (s *gcalLiveSource) RegisterCalendarWatch(ctx context.Context, credentials CredentialStore, calendar GCalCalendarConfig, webhookSecret, callbackURL, channelPrefix string, requestedTTLSeconds int) (ConnectorWatchState, error) {
	token, err := loadConnectorSecret("gcal", s.tokenEnvVar, s.tokenFile, credentials.ConnectorCredential("gcal"))
	if err != nil {
		return ConnectorWatchState{}, err
	}

	channelID, err := newGCalChannelID(channelPrefix, calendar.ID)
	if err != nil {
		return ConnectorWatchState{}, err
	}

	base, err := url.Parse(s.baseURL)
	if err != nil {
		return ConnectorWatchState{}, fmt.Errorf("parse gcal api base url: %w", err)
	}
	base.Path = path.Join("/", base.Path, "calendars", url.PathEscape(calendar.ID), "events", "watch")

	requestPayload := gcalWatchRequest{
		ID:      channelID,
		Type:    "web_hook",
		Address: callbackURL,
		Token:   webhookSecret,
	}
	if requestedTTLSeconds > 0 {
		requestPayload.Params = map[string]string{
			"ttl": strconv.Itoa(requestedTTLSeconds),
		}
	}

	registeredAt := time.Now().UTC()
	payload, _, err := doConnectorJSON[gcalWatchResponse](ctx, s.httpClient, "gcal", func() (*http.Request, error) {
		body, err := json.Marshal(requestPayload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
	if err != nil {
		return ConnectorWatchState{}, err
	}

	expiresAt, err := parseGCalWatchExpiration(payload.Expiration)
	if err != nil {
		return ConnectorWatchState{}, err
	}
	if strings.TrimSpace(payload.ResourceID) == "" {
		return ConnectorWatchState{}, fmt.Errorf("gcal watch response did not include resourceId")
	}
	if strings.TrimSpace(payload.ID) == "" {
		payload.ID = channelID
	}

	return ConnectorWatchState{
		ConnectorType: "gcal",
		ScopeRef:      strings.TrimSpace(calendar.ID),
		ChannelID:     strings.TrimSpace(payload.ID),
		ResourceID:    strings.TrimSpace(payload.ResourceID),
		ResourceURI:   strings.TrimSpace(payload.ResourceURI),
		CallbackURL:   strings.TrimSpace(callbackURL),
		RegisteredAt:  registeredAt,
		ExpiresAt:     expiresAt,
	}, nil
}

func gcalWatchStateKey(calendarID string) string {
	return "gcal:watch:" + strings.TrimSpace(calendarID)
}

func gcalWatchUsable(watch ConnectorWatchState, callbackURL string, now time.Time) bool {
	if !strings.EqualFold(strings.TrimSpace(watch.ConnectorType), "gcal") {
		return false
	}
	if strings.TrimSpace(watch.ChannelID) == "" || strings.TrimSpace(watch.ResourceID) == "" {
		return false
	}
	if strings.TrimSpace(watch.CallbackURL) != strings.TrimSpace(callbackURL) {
		return false
	}
	if watch.ExpiresAt.IsZero() {
		return false
	}
	return now.Add(gcalWatchRefreshWindow).Before(watch.ExpiresAt)
}

func parseGCalWatchExpiration(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}
	millis, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse gcal watch expiration: %w", err)
	}
	return time.UnixMilli(millis).UTC(), nil
}

func newGCalChannelID(prefix, calendarID string) (string, error) {
	suffix, err := randomOAuthValue(8)
	if err != nil {
		return "", fmt.Errorf("generate gcal watch channel id: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(calendarID)))
	trimmedPrefix := strings.TrimSpace(prefix)
	if trimmedPrefix == "" {
		trimmedPrefix = "alice-edge-gcal"
	}
	return fmt.Sprintf("%s-%s-%s", trimmedPrefix, hex.EncodeToString(sum[:4]), suffix), nil
}
