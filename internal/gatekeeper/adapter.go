package gatekeeper

import (
	"context"

	"alice/internal/core"
	"alice/internal/requests"
)

// RequestsAdapter exposes the gatekeeper Service as a requests.AutoAnswerer.
// It exists so the requests package can stay free of import cycles on the
// queries package.
type RequestsAdapter struct {
	service *Service
}

// AsRequestsAutoAnswerer returns an adapter that satisfies
// requests.AutoAnswerer.
func (s *Service) AsRequestsAutoAnswerer() *RequestsAdapter {
	return &RequestsAdapter{service: s}
}

// Evaluate implements requests.AutoAnswerer.
func (a *RequestsAdapter) Evaluate(ctx context.Context, request core.Request) requests.AutoAnswerResult {
	if a == nil || a.service == nil {
		return requests.AutoAnswerResult{Reason: "gatekeeper not configured"}
	}
	verdict := a.service.Evaluate(ctx, request)
	return requests.AutoAnswerResult{
		Answered:    verdict.Answered,
		Summary:     verdict.Summary,
		ArtifactIDs: verdict.ArtifactIDs,
		Confidence:  verdict.Confidence,
		Reason:      verdict.Reason,
	}
}
