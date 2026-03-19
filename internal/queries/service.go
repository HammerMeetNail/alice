package queries

import (
	"errors"
	"slices"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

var ErrPermissionDenied = errors.New("permission denied")

type ArtifactSource interface {
	ListArtifactsByOwner(userID string) []core.Artifact
}

type PolicySource interface {
	ListGrantsForPair(grantorUserID, granteeUserID string) []core.PolicyGrant
}

type Service struct {
	store     *memory.Store
	artifacts ArtifactSource
	policies  PolicySource
}

func NewService(store *memory.Store, artifacts ArtifactSource, policies PolicySource) *Service {
	return &Service{
		store:     store,
		artifacts: artifacts,
		policies:  policies,
	}
}

func (s *Service) Evaluate(query core.Query) (core.QueryResponse, error) {
	s.store.SaveQuery(query)

	grants := s.policies.ListGrantsForPair(query.ToUserID, query.FromUserID)
	if len(grants) == 0 {
		s.store.UpdateQueryState(query.QueryID, core.QueryStateDenied)
		return core.QueryResponse{}, ErrPermissionDenied
	}

	allArtifacts := s.artifacts.ListArtifactsByOwner(query.ToUserID)
	filtered := make([]core.QueryArtifact, 0)
	policyBasis := make([]string, 0)

	for _, artifact := range allArtifacts {
		if !artifact.CreatedAt.IsZero() && (artifact.CreatedAt.Before(query.TimeWindow.Start) || artifact.CreatedAt.After(query.TimeWindow.End)) {
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

	s.store.SaveQueryResponse(response)
	s.store.UpdateQueryState(query.QueryID, core.QueryStateCompleted)
	return response, nil
}

func (s *Service) FindResult(queryID string) (core.Query, core.QueryResponse, bool) {
	query, ok := s.store.FindQuery(queryID)
	if !ok {
		return core.Query{}, core.QueryResponse{}, false
	}
	response, ok := s.store.FindQueryResponse(queryID)
	if !ok {
		return query, core.QueryResponse{}, false
	}
	return query, response, true
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
