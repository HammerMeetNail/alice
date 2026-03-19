package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"alice/internal/core"
)

type gcalLiveSource struct {
	baseURL     string
	tokenEnvVar string
	tokenFile   string
	calendars   []GCalCalendarConfig
	httpClient  *http.Client
}

type gcalEventsResponse struct {
	Items []gcalEventResponse `json:"items"`
}

type gcalEventResponse struct {
	ID        string                 `json:"id"`
	Status    string                 `json:"status"`
	Updated   time.Time              `json:"updated"`
	EventType string                 `json:"eventType"`
	Start     gcalEventTimeResponse  `json:"start"`
	End       gcalEventTimeResponse  `json:"end"`
	Attendees []gcalAttendeeResponse `json:"attendees"`
}

type gcalEventTimeResponse struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

type gcalAttendeeResponse struct {
	Email string `json:"email"`
}

func newGCalLiveSource(cfg Config) EventSource {
	return &gcalLiveSource{
		baseURL:     cfg.GCalAPIBaseURL(),
		tokenEnvVar: cfg.GCalTokenEnvVar(),
		tokenFile:   cfg.GCalTokenFile(),
		calendars:   append([]GCalCalendarConfig(nil), cfg.Connectors.GCal.Calendars...),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *gcalLiveSource) Name() string {
	return "gcal_live"
}

func (s *gcalLiveSource) Poll(ctx context.Context, state State) ([]NormalizedEvent, error) {
	token, err := loadConnectorSecret("gcal", s.tokenEnvVar, s.tokenFile, state.ConnectorCredential("gcal"))
	if err != nil {
		return nil, err
	}

	events := make([]NormalizedEvent, 0)
	for _, calendar := range s.calendars {
		calendarEvents, err := s.listEvents(ctx, token, calendar, state.CursorTime(gcalCursorKey(calendar.ID)))
		if err != nil {
			return nil, fmt.Errorf("list events for %s: %w", calendar.ID, err)
		}
		for _, calendarEvent := range calendarEvents {
			if strings.EqualFold(strings.TrimSpace(calendarEvent.Status), "cancelled") {
				continue
			}
			events = append(events, normalizeLiveGCalEvent(calendar, calendarEvent))
		}
	}
	return events, nil
}

func (s *gcalLiveSource) listEvents(ctx context.Context, token string, calendar GCalCalendarConfig, cursor time.Time) ([]gcalEventResponse, error) {
	base, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse gcal api base url: %w", err)
	}
	base.Path = path.Join("/", base.Path, "calendars", url.PathEscape(calendar.ID), "events")

	query := base.Query()
	query.Set("singleEvents", "true")
	query.Set("orderBy", "updated")
	query.Set("maxResults", "50")
	query.Set("showDeleted", "false")
	if !cursor.IsZero() {
		query.Set("updatedMin", cursor.UTC().Format(time.RFC3339))
	}
	base.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build gcal request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform gcal request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("gcal api returned status %d", resp.StatusCode)
	}

	var payload gcalEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode gcal response: %w", err)
	}
	return payload.Items, nil
}

func normalizeLiveGCalEvent(calendar GCalCalendarConfig, event gcalEventResponse) NormalizedEvent {
	startAt := event.Start.Time()
	endAt := event.End.Time()
	observedAt := event.Updated
	if observedAt.IsZero() {
		switch {
		case !startAt.IsZero():
			observedAt = startAt
		default:
			observedAt = time.Now().UTC()
		}
	}

	return NormalizedEvent{
		SourceSystem: "gcal",
		EventType:    "event",
		SourceType:   "event",
		SourceID:     event.ID,
		ObservedAt:   observedAt,
		CursorKey:    gcalCursorKey(calendar.ID),
		ProjectRefs:  projectRefsForCalendar(calendar),
		TrustClass:   core.TrustClassStructuredSystem,
		Sensitivity:  core.SensitivityLow,
		Attributes: map[string]any{
			"category":       normalizeCalendarCategory(event.EventType, calendar.Category),
			"attendee_count": len(event.Attendees),
			"start_at":       startAt,
			"end_at":         endAt,
		},
	}
}

func (v gcalEventTimeResponse) Time() time.Time {
	if strings.TrimSpace(v.DateTime) != "" {
		parsed, err := time.Parse(time.RFC3339, v.DateTime)
		if err == nil {
			return parsed
		}
	}
	if strings.TrimSpace(v.Date) != "" {
		parsed, err := time.Parse("2006-01-02", v.Date)
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func normalizeCalendarCategory(eventType, configuredCategory string) string {
	if strings.TrimSpace(configuredCategory) != "" {
		return normalizeLabel(configuredCategory, "meeting")
	}

	switch strings.TrimSpace(eventType) {
	case "focusTime":
		return "focus"
	case "outOfOffice":
		return "out_of_office"
	case "workingLocation":
		return "working_location"
	case "":
		return "meeting"
	default:
		return normalizeLabel(eventType, "meeting")
	}
}

func projectRefsForCalendar(calendar GCalCalendarConfig) []string {
	if len(calendar.ProjectRefs) > 0 {
		return append([]string(nil), calendar.ProjectRefs...)
	}
	if strings.TrimSpace(calendar.ID) == "" {
		return nil
	}
	return []string{strings.TrimSpace(calendar.ID)}
}

func gcalCursorKey(calendarID string) string {
	return "gcal:calendar:" + strings.TrimSpace(calendarID)
}
