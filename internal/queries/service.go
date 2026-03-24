package queries

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

var ErrPermissionDenied = errors.New("permission denied")

type ArtifactSource interface {
	ListArtifactsByOwner(ctx context.Context, userID string) ([]core.Artifact, error)
}

type PolicySource interface {
	ListGrantsForPair(ctx context.Context, grantorUserID, granteeUserID string) ([]core.PolicyGrant, error)
}

type Service struct {
	store     storage.QueryRepository
	artifacts ArtifactSource
	policies  PolicySource
}

func NewService(store storage.QueryRepository, artifacts ArtifactSource, policies PolicySource) *Service {
	return &Service{
		store:     store,
		artifacts: artifacts,
		policies:  policies,
	}
}

func (s *Service) Evaluate(ctx context.Context, query core.Query) (core.QueryResponse, error) {
	if _, err := s.store.SaveQuery(ctx, query); err != nil {
		return core.QueryResponse{}, fmt.Errorf("save query: %w", err)
	}

	grants, err := s.policies.ListGrantsForPair(ctx, query.ToUserID, query.FromUserID)
	if err != nil {
		return core.QueryResponse{}, fmt.Errorf("list grants for pair: %w", err)
	}
	if len(grants) == 0 {
		if _, _, err := s.store.UpdateQueryState(ctx, query.QueryID, core.QueryStateDenied); err != nil {
			return core.QueryResponse{}, fmt.Errorf("update query state to denied: %w", err)
		}
		return core.QueryResponse{}, ErrPermissionDenied
	}

	allArtifacts, err := s.artifacts.ListArtifactsByOwner(ctx, query.ToUserID)
	if err != nil {
		return core.QueryResponse{}, fmt.Errorf("list artifacts by owner: %w", err)
	}
	supersededArtifacts := supersededArtifactIDs(allArtifacts)
	filtered := make([]core.QueryArtifact, 0)
	policyBasis := make([]string, 0)

	for _, artifact := range allArtifacts {
		if _, ok := supersededArtifacts[artifact.ArtifactID]; ok {
			continue
		}
		activityTime := artifactActivityTime(artifact)
		if !activityTime.IsZero() && (activityTime.Before(query.TimeWindow.Start) || activityTime.After(query.TimeWindow.End)) {
			continue
		}
		if artifact.ExpiresAt != nil && artifact.ExpiresAt.Before(time.Now().UTC()) {
			continue
		}
		if !slices.Contains(query.RequestedTypes, artifact.Type) {
			continue
		}
		if artifact.VisibilityMode == core.VisibilityModePrivate {
			continue
		}

		matchedGrant := matchingGrant(grants, query, artifact)
		if matchedGrant == nil {
			continue
		}

		filtered = append(filtered, core.QueryArtifact{
			ArtifactID:  artifact.ArtifactID,
			Type:        artifact.Type,
			Title:       artifact.Title,
			Content:     artifact.Content,
			Sensitivity: artifact.Sensitivity,
			Confidence:  artifact.Confidence,
		})
		policyBasis = append(policyBasis, "grant:"+matchedGrant.PolicyGrantID)
	}

	response := core.QueryResponse{
		ResponseID:    id.New("response"),
		QueryID:       query.QueryID,
		FromAgentID:   query.FromAgentID,
		ToAgentID:     query.ToAgentID,
		Artifacts:     filtered,
		Redactions:    []string{},
		PolicyBasis:   dedupe(policyBasis),
		ApprovalState: core.ApprovalStateNotRequired,
		Confidence:    aggregateConfidence(filtered),
		CreatedAt:     time.Now().UTC(),
	}

	if _, err := s.store.SaveQueryResponse(ctx, response); err != nil {
		return core.QueryResponse{}, fmt.Errorf("save query response: %w", err)
	}
	if _, _, err := s.store.UpdateQueryState(ctx, query.QueryID, core.QueryStateCompleted); err != nil {
		return core.QueryResponse{}, fmt.Errorf("update query state to completed: %w", err)
	}
	return response, nil
}

func (s *Service) FindResult(ctx context.Context, queryID string) (core.Query, core.QueryResponse, bool, error) {
	query, ok, err := s.store.FindQuery(ctx, queryID)
	if err != nil {
		return core.Query{}, core.QueryResponse{}, false, fmt.Errorf("find query: %w", err)
	}
	if !ok {
		return core.Query{}, core.QueryResponse{}, false, nil
	}
	response, ok, err := s.store.FindQueryResponse(ctx, queryID)
	if err != nil {
		return core.Query{}, core.QueryResponse{}, false, fmt.Errorf("find query response: %w", err)
	}
	if !ok {
		return query, core.QueryResponse{}, false, nil
	}
	return query, response, true, nil
}

func matchingGrant(grants []core.PolicyGrant, query core.Query, artifact core.Artifact) *core.PolicyGrant {
	for i := range grants {
		grant := grants[i]
		if grant.ExpiresAt != nil && grant.ExpiresAt.Before(time.Now().UTC()) {
			continue
		}
		if !slices.Contains(grant.AllowedPurposes, query.Purpose) {
			continue
		}
		if !slices.Contains(grant.AllowedArtifactTypes, artifact.Type) {
			continue
		}
		if !core.SensitivityAllowed(artifact.Sensitivity, grant.MaxSensitivity) {
			continue
		}
		if grant.ScopeRef != "" && len(query.ProjectScope) > 0 && !slices.Contains(query.ProjectScope, grant.ScopeRef) {
			continue
		}
		if grant.ScopeRef != "" {
			projectRefs := projectRefsFromPayload(artifact.StructuredPayload)
			if len(projectRefs) == 0 && len(query.ProjectScope) > 0 {
				continue
			}
			if len(projectRefs) > 0 && !slices.Contains(projectRefs, grant.ScopeRef) {
				continue
			}
		}
		return &grant
	}
	return nil
}

func projectRefsFromPayload(payload map[string]any) []string {
	if payload == nil {
		return nil
	}

	raw, ok := payload["project_refs"]
	if !ok {
		return nil
	}

	switch value := raw.(type) {
	case []string:
		return value
	case []any:
		refs := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok || text == "" {
				continue
			}
			refs = append(refs, text)
		}
		return refs
	default:
		return nil
	}
}

func aggregateConfidence(artifacts []core.QueryArtifact) float64 {
	if len(artifacts) == 0 {
		return 0
	}
	var sum float64
	for _, artifact := range artifacts {
		sum += artifact.Confidence
	}
	return sum / float64(len(artifacts))
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func artifactActivityTime(artifact core.Artifact) time.Time {
	var latestObservedAt time.Time
	for _, ref := range artifact.SourceRefs {
		if ref.ObservedAt.After(latestObservedAt) {
			latestObservedAt = ref.ObservedAt
		}
	}
	if !latestObservedAt.IsZero() {
		return latestObservedAt
	}
	return artifact.CreatedAt
}

func supersededArtifactIDs(artifacts []core.Artifact) map[string]struct{} {
	superseded := make(map[string]struct{})
	for _, artifact := range artifacts {
		if artifact.SupersedesArtifactID == nil {
			continue
		}
		artifactID := strings.TrimSpace(*artifact.SupersedesArtifactID)
		if artifactID == "" {
			continue
		}
		superseded[artifactID] = struct{}{}
	}
	return superseded
}
