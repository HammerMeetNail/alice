package edge

import (
	"context"
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
	Items         []gcalEventResponse `json:"items"`
	NextPageToken string              `json:"nextPageToken"`
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

func newGCalLiveSource(cfg Config) *gcalLiveSource {
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

func (s *gcalLiveSource) Poll(ctx context.Context, state State, credentials CredentialStore) ([]NormalizedEvent, error) {
	token, err := loadConnectorSecret("gcal", s.tokenEnvVar, s.tokenFile, credentials.ConnectorCredential("gcal"))
	if err != nil {
		return nil, err
	}

	events := make([]NormalizedEvent, 0)
	for _, calendar := range s.calendars {
		calendarEvents, err := s.listEvents(ctx, token, calendar, state.CursorTime(gcalCursorKey(calendar.ID)))
		if err != nil {
			return nil, fmt.Errorf("list events for %s: %w", calendar.ID, err)
		}
		events = append(events, normalizeLiveGCalEvents(calendar, calendarEvents)...)
	}
	return events, nil
}

// pollWithToken accepts a caller-resolved token and an explicit `since`
// cursor, letting reusers drive the poller without synthesising a State /
// CredentialStore pair.
func (s *gcalLiveSource) pollWithToken(ctx context.Context, token string, since time.Time) ([]NormalizedEvent, error) {
	events := make([]NormalizedEvent, 0)
	for _, calendar := range s.calendars {
		calendarEvents, err := s.listEvents(ctx, token, calendar, since)
		if err != nil {
			return nil, fmt.Errorf("list events for %s: %w", calendar.ID, err)
		}
		events = append(events, normalizeLiveGCalEvents(calendar, calendarEvents)...)
	}
	return events, nil
}

func (s *gcalLiveSource) PollCalendar(ctx context.Context, state State, credentials CredentialStore, calendar GCalCalendarConfig) ([]NormalizedEvent, error) {
	token, err := loadConnectorSecret("gcal", s.tokenEnvVar, s.tokenFile, credentials.ConnectorCredential("gcal"))
	if err != nil {
		return nil, err
	}

	calendarEvents, err := s.listEvents(ctx, token, calendar, state.CursorTime(gcalCursorKey(calendar.ID)))
	if err != nil {
		return nil, fmt.Errorf("list events for %s: %w", calendar.ID, err)
	}
	return normalizeLiveGCalEvents(calendar, calendarEvents), nil
}

func (s *gcalLiveSource) listEvents(ctx context.Context, token string, calendar GCalCalendarConfig, cursor time.Time) ([]gcalEventResponse, error) {
	const pageSize = 50

	base, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse gcal api base url: %w", err)
	}
	base.Path = path.Join("/", base.Path, "calendars", url.PathEscape(calendar.ID), "events")

	events := make([]gcalEventResponse, 0)
	pageToken := ""
	for {
		payload, _, err := doConnectorJSON[gcalEventsResponse](ctx, s.httpClient, "gcal", func() (*http.Request, error) {
			requestURL := *base
			query := requestURL.Query()
			query.Set("singleEvents", "true")
			query.Set("orderBy", "updated")
			query.Set("maxResults", fmt.Sprintf("%d", pageSize))
			query.Set("showDeleted", "false")
			if !cursor.IsZero() {
				query.Set("updatedMin", cursor.UTC().Format(time.RFC3339))
			}
			if strings.TrimSpace(pageToken) != "" {
				query.Set("pageToken", pageToken)
			}
			requestURL.RawQuery = query.Encode()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			return req, nil
		})
		if err != nil {
			return nil, err
		}

		events = append(events, payload.Items...)
		pageToken = strings.TrimSpace(payload.NextPageToken)
		if pageToken == "" {
			break
		}
	}

	return events, nil
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

func normalizeLiveGCalEvents(calendar GCalCalendarConfig, events []gcalEventResponse) []NormalizedEvent {
	normalized := make([]NormalizedEvent, 0, len(events))
	for _, event := range events {
		if strings.EqualFold(strings.TrimSpace(event.Status), "cancelled") {
			continue
		}
		normalized = append(normalized, normalizeLiveGCalEvent(calendar, event))
	}
	return normalized
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

func findGCalCalendarConfig(calendars []GCalCalendarConfig, calendarID string) (GCalCalendarConfig, bool) {
	trimmedCalendarID := strings.TrimSpace(calendarID)
	for _, calendar := range calendars {
		if strings.EqualFold(strings.TrimSpace(calendar.ID), trimmedCalendarID) {
			return calendar, true
		}
	}
	return GCalCalendarConfig{}, false
}
