package edge

import (
	"context"
	"time"

	"alice/internal/core"
)

type NormalizedEvent struct {
	SourceSystem string           `json:"source_system"`
	EventType    string           `json:"event_type"`
	SourceType   string           `json:"source_type"`
	SourceID     string           `json:"source_id"`
	ObservedAt   time.Time        `json:"observed_at"`
	CursorKey    string           `json:"cursor_key,omitempty"`
	ProjectRefs  []string         `json:"project_refs,omitempty"`
	TrustClass   core.TrustClass  `json:"trust_class"`
	Sensitivity  core.Sensitivity `json:"sensitivity"`
	Attributes   map[string]any   `json:"attributes,omitempty"`
}

type EventSource interface {
	Name() string
	Poll(ctx context.Context, state State) ([]NormalizedEvent, error)
}
