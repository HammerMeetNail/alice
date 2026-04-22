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

// RiskPolicyEvaluator overrides the grant-level `requires_approval_above_risk`
// ladder with an admin-applied policy. Implementations are expected to fail
// closed: any evaluation error returns core.RiskDecisionDeny. Passing nil
// disables the evaluator and the queries service falls back to the ladder,
// preserving pre-policy-engine behaviour.
type RiskPolicyEvaluator interface {
	EvaluateQuery(ctx context.Context, query core.Query, matchedGrant core.PolicyGrant, artifact core.Artifact) core.RiskDecisionAction
}

type Service struct {
	store     storage.QueryRepository
	artifacts ArtifactSource
	policies  PolicySource
	approvals storage.ApprovalRepository
	tx        storage.Transactor
	risk      RiskPolicyEvaluator
}

func NewService(store storage.QueryRepository, artifacts ArtifactSource, policies PolicySource, approvals storage.ApprovalRepository, tx storage.Transactor) *Service {
	return &Service{
		store:     store,
		artifacts: artifacts,
		policies:  policies,
		approvals: approvals,
		tx:        tx,
	}
}

// WithRiskPolicyEvaluator attaches an evaluator; calls with nil are ignored.
func (s *Service) WithRiskPolicyEvaluator(eval RiskPolicyEvaluator) *Service {
	if s != nil && eval != nil {
		s.risk = eval
	}
	return s
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

	// Track which grant (if any) requires approval for this query's risk level.
	var approvalRequiredByGrant *core.PolicyGrant
	redactions := make([]string, 0)

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
			// Check if the artifact was excluded solely because of sensitivity ceiling.
			sensitivityGrant := matchingGrantIgnoreSensitivity(grants, query, artifact)
			if sensitivityGrant != nil {
				redactions = append(redactions, fmt.Sprintf("artifact:%s: sensitivity %q exceeds grant ceiling %q", artifact.ArtifactID, artifact.Sensitivity, sensitivityGrant.MaxSensitivity))
			}
			continue
		}

		// Risk-policy decision: an admin-applied policy can override the
		// ladder either way — allow what the ladder would gate, or require
		// approval / deny something the ladder would wave through. When no
		// policy is attached (nil evaluator) we fall through to the ladder
		// so existing deployments keep behaving identically.
		ladderVerdict := core.RiskDecisionAllow
		if core.RiskLevelExceeds(query.RiskLevel, matchedGrant.RequiresApprovalAboveRisk) {
			ladderVerdict = core.RiskDecisionRequireApproval
		}
		verdict := ladderVerdict
		if s.risk != nil {
			verdict = s.risk.EvaluateQuery(ctx, query, *matchedGrant, artifact)
		}
		switch verdict {
		case core.RiskDecisionDeny:
			redactions = append(redactions, fmt.Sprintf("artifact:%s: denied by risk policy", artifact.ArtifactID))
			continue
		case core.RiskDecisionRequireApproval:
			if approvalRequiredByGrant == nil {
				approvalRequiredByGrant = matchedGrant
			}
			redactions = append(redactions, fmt.Sprintf("artifact:%s: withheld pending approval (risk level %q, grant threshold %q)", artifact.ArtifactID, query.RiskLevel, matchedGrant.RequiresApprovalAboveRisk))
			continue
		}

		content := artifact.Content
		if core.SensitivityAtCeiling(artifact.Sensitivity, matchedGrant.MaxSensitivity) {
			content = "[content redacted: sensitivity at grant ceiling]"
			redactions = append(redactions, fmt.Sprintf("artifact:%s: content redacted (sensitivity %q at grant ceiling %q)", artifact.ArtifactID, artifact.Sensitivity, matchedGrant.MaxSensitivity))
		}

		filtered = append(filtered, core.QueryArtifact{
			ArtifactID:  artifact.ArtifactID,
			Type:        artifact.Type,
			Title:       artifact.Title,
			Content:     content,
			Sensitivity: artifact.Sensitivity,
			Confidence:  artifact.Confidence,
			CreatedAt:   artifact.CreatedAt,
			ObservedAt:  latestObservedAt(artifact.SourceRefs),
			SourceRefs:  artifact.SourceRefs,
		})
		policyBasis = append(policyBasis, "grant:"+matchedGrant.PolicyGrantID)
	}

	approvalState := core.ApprovalStateNotRequired
	if approvalRequiredByGrant != nil {
		approvalState = core.ApprovalStatePending
	}

	response := core.QueryResponse{
		ResponseID:    id.New("response"),
		QueryID:       query.QueryID,
		FromAgentID:   query.FromAgentID,
		ToAgentID:     query.ToAgentID,
		Artifacts:     filtered,
		Redactions:    redactions,
		PolicyBasis:   dedupe(policyBasis),
		ApprovalState: approvalState,
		Confidence:    aggregateConfidence(filtered),
		CreatedAt:     time.Now().UTC(),
	}

	if _, err := s.store.SaveQueryResponse(ctx, response); err != nil {
		return core.QueryResponse{}, fmt.Errorf("save query response: %w", err)
	}

	// If approval is required, create an approval record and leave the query queued.
	if approvalRequiredByGrant != nil {
		approval := core.Approval{
			ApprovalID:  id.New("approval"),
			OrgID:       query.OrgID,
			AgentID:     query.ToAgentID,
			OwnerUserID: query.ToUserID,
			SubjectType: "query",
			SubjectID:   query.QueryID,
			Reason:      fmt.Sprintf("query risk level %s exceeds grant threshold", query.RiskLevel),
			State:       core.ApprovalStatePending,
			CreatedAt:   time.Now().UTC(),
			ExpiresAt:   query.ExpiresAt,
		}
		if _, err := s.approvals.SaveApproval(ctx, approval); err != nil {
			return core.QueryResponse{}, fmt.Errorf("save risk-based approval: %w", err)
		}
		return response, nil
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

// matchingGrantIgnoreSensitivity returns the first grant that matches all criteria
// except the sensitivity ceiling. Used to detect redactions due to sensitivity.
func matchingGrantIgnoreSensitivity(grants []core.PolicyGrant, query core.Query, artifact core.Artifact) *core.PolicyGrant {
	for i := range grants {
		grant := grants[i]
		if grant.ExpiresAt != nil && grant.ExpiresAt.Before(time.Now().UTC()) {
			continue
		}
		if grant.RevokedAt != nil {
			continue
		}
		if !slices.Contains(grant.AllowedPurposes, query.Purpose) {
			continue
		}
		if !slices.Contains(grant.AllowedArtifactTypes, artifact.Type) {
			continue
		}
		// Skipping sensitivity check intentionally — caller wants to know if
		// sensitivity is the only reason the artifact was excluded.
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

func matchingGrant(grants []core.PolicyGrant, query core.Query, artifact core.Artifact) *core.PolicyGrant {
	for i := range grants {
		grant := grants[i]
		if grant.ExpiresAt != nil && grant.ExpiresAt.Before(time.Now().UTC()) {
			continue
		}
		if grant.RevokedAt != nil {
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
	if observed := latestObservedAt(artifact.SourceRefs); !observed.IsZero() {
		return observed
	}
	return artifact.CreatedAt
}

// latestObservedAt returns the most recent observed_at across a set of source
// references. It is used both for time-window filtering (so we match on when
// the source actually occurred, not when the artifact was persisted) and for
// provenance display in query responses.
func latestObservedAt(refs []core.SourceReference) time.Time {
	var latest time.Time
	for _, ref := range refs {
		if ref.ObservedAt.After(latest) {
			latest = ref.ObservedAt
		}
	}
	return latest
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
