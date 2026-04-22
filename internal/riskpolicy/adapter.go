package riskpolicy

import (
	"context"

	"alice/internal/core"
)

// QueriesAdapter adapts Service into queries.RiskPolicyEvaluator without
// forcing the queries package to import riskpolicy (avoids a cycle via
// storage and keeps queries' surface minimal).
type QueriesAdapter struct {
	service *Service
}

// AsQueriesEvaluator wraps the Service so it satisfies the queries-facing
// interface. Passing a nil receiver returns nil, letting callers write
// `service.AsQueriesEvaluator()` even when the service was never
// constructed (e.g. tests).
func (s *Service) AsQueriesEvaluator() *QueriesAdapter {
	if s == nil {
		return nil
	}
	return &QueriesAdapter{service: s}
}

// EvaluateQuery implements queries.RiskPolicyEvaluator.
func (a *QueriesAdapter) EvaluateQuery(ctx context.Context, query core.Query, matchedGrant core.PolicyGrant, artifact core.Artifact) core.RiskDecisionAction {
	if a == nil || a.service == nil {
		return core.RiskDecisionAllow
	}
	decision := a.service.Evaluate(ctx, query.OrgID, Inputs{
		Purpose:     query.Purpose,
		RiskLevel:   query.RiskLevel,
		Sensitivity: artifact.Sensitivity,
		ScopeType:   matchedGrant.ScopeType,
	})
	return decision.Action
}
