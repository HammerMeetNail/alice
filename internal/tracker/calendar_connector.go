package tracker

import (
	"context"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/edge"
)

// calendarConnector adapts the edge package's live Google Calendar poller.
// The MCP tracker expects a pre-minted access token (bootstrap via
// `cmd/edge-agent -bootstrap-connector`); it does not handle OAuth refresh
// itself to keep the in-process surface minimal.
type calendarConnector struct {
	poller *edge.GCalLivePoller
}

func newCalendarConnectorFromEnv() (Connector, error) {
	token := strings.TrimSpace(envOr("ALICE_TRACK_CALENDAR_TOKEN", ""))
	if token == "" {
		return nil, nil
	}

	ids := envCommaList("ALICE_TRACK_CALENDAR_IDS")
	if len(ids) == 0 {
		ids = []string{"primary"}
	}
	calendars := make([]edge.GCalCalendarConfig, 0, len(ids))
	for _, id := range ids {
		calendars = append(calendars, edge.GCalCalendarConfig{ID: id})
	}

	apiURL := envOr("ALICE_TRACK_CALENDAR_API_URL", "https://www.googleapis.com/calendar/v3")

	poller := edge.NewGCalLivePoller(edge.GCalLivePollerConfig{
		APIBaseURL: apiURL,
		Token:      token,
		Calendars:  calendars,
	}, edge.LivePollerOptions{})

	return &calendarConnector{poller: poller}, nil
}

func (c *calendarConnector) Name() string { return "calendar" }

func (c *calendarConnector) Poll(ctx context.Context) ([]core.Artifact, error) {
	events, err := c.poller.Poll(ctx)
	if err != nil {
		return nil, err
	}
	artifacts := edge.DeriveArtifactsLive(events)
	assignObservedAt(artifacts, time.Now().UTC())
	return artifacts, nil
}
