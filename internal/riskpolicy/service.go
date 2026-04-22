package riskpolicy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

// ErrNotOrgAdmin mirrors the sentinel used by the agents package so HTTP
// handlers can translate a non-admin call into 403.
var ErrNotOrgAdmin = errors.New("caller is not an org admin")

// UserDirectory is the minimal surface the policy service needs from the
// agents package to enforce admin-only writes without pulling in a full
// dependency on agents.Service.
type UserDirectory interface {
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
}

// Service wraps the RiskPolicyRepository and the pure evaluator. It is the
// layer HTTP / CLI / MCP surfaces call; the evaluator stays free of
// storage and cross-cutting concerns so it can be unit-tested tightly.
type Service struct {
	store storage.RiskPolicyRepository
	users UserDirectory
}

// NewService constructs a Service from the storage layer and an optional
// user directory. When users is nil the admin check is skipped — useful
// for unit tests that drive Apply directly without plumbing an agents
// service.
func NewService(store storage.RiskPolicyRepository, users UserDirectory) *Service {
	return &Service{store: store, users: users}
}

// Apply parses source, writes it as a new versioned policy, and activates
// it. Caller must identify the acting agent; when the service has a user
// directory attached, it verifies the acting user is an org admin.
func (s *Service) Apply(ctx context.Context, agent core.Agent, name string, source []byte) (core.RiskPolicy, error) {
	if _, err := Parse(source); err != nil {
		return core.RiskPolicy{}, fmt.Errorf("invalid policy: %w", err)
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return core.RiskPolicy{}, err
	}

	version, err := s.store.NextPolicyVersionForOrg(ctx, agent.OrgID)
	if err != nil {
		return core.RiskPolicy{}, fmt.Errorf("allocate policy version: %w", err)
	}

	now := time.Now().UTC()
	policy := core.RiskPolicy{
		PolicyID:        id.New("rpolicy"),
		OrgID:           agent.OrgID,
		Name:            name,
		Version:         version,
		Source:          string(source),
		CreatedAt:       now,
		CreatedByUserID: agent.OwnerUserID,
	}
	if _, err := s.store.SavePolicy(ctx, policy); err != nil {
		return core.RiskPolicy{}, fmt.Errorf("save policy: %w", err)
	}
	if err := s.store.ActivatePolicy(ctx, policy.PolicyID, now); err != nil {
		return core.RiskPolicy{}, fmt.Errorf("activate policy: %w", err)
	}
	policy.ActiveAt = &now

	slog.Info("risk policy applied",
		"org_id", agent.OrgID,
		"policy_id", policy.PolicyID,
		"version", policy.Version,
		"created_by", agent.OwnerUserID,
	)
	return policy, nil
}

// Activate rolls back (or forward) by flipping the active policy to a
// previously-saved version. Caller must be an org admin of the policy's
// org. The call is a no-op when the policy is already active.
func (s *Service) Activate(ctx context.Context, agent core.Agent, policyID string) (core.RiskPolicy, error) {
	policy, ok, err := s.store.FindPolicyByID(ctx, policyID)
	if err != nil {
		return core.RiskPolicy{}, fmt.Errorf("find policy: %w", err)
	}
	if !ok {
		return core.RiskPolicy{}, storage.ErrRiskPolicyNotFound
	}
	if policy.OrgID != agent.OrgID {
		return core.RiskPolicy{}, core.ForbiddenError{Message: "policy belongs to a different org"}
	}
	if err := s.requireAdmin(ctx, agent); err != nil {
		return core.RiskPolicy{}, err
	}

	now := time.Now().UTC()
	if err := s.store.ActivatePolicy(ctx, policyID, now); err != nil {
		return core.RiskPolicy{}, fmt.Errorf("activate policy: %w", err)
	}
	policy.ActiveAt = &now

	slog.Info("risk policy reactivated",
		"org_id", agent.OrgID,
		"policy_id", policy.PolicyID,
		"version", policy.Version,
		"actor", agent.OwnerUserID,
	)
	return policy, nil
}

// History returns up to limit policies for the org, newest first. Any
// member of the org may read the history — it contains no raw source data
// about other users, and admins should be able to audit rotations.
func (s *Service) History(ctx context.Context, agent core.Agent, limit, offset int) ([]core.RiskPolicy, error) {
	return s.store.ListPoliciesForOrg(ctx, agent.OrgID, limit, offset)
}

// Evaluate returns the decision for the given inputs against the org's
// current active policy. When no policy is active, the built-in default
// policy applies (allow + fall through to grant-level ladder), which keeps
// existing behaviour for deployments that have never applied a policy.
//
// Evaluation failure (malformed stored policy, storage error) fails
// closed: the caller sees a `deny` decision with the reason explaining why.
func (s *Service) Evaluate(ctx context.Context, orgID string, inputs Inputs) Decision {
	if s == nil || s.store == nil {
		return Decision{Action: core.RiskDecisionAllow, Reasons: []string{"no policy engine configured"}}
	}
	active, ok, err := s.store.FindActivePolicyForOrg(ctx, orgID)
	if err != nil {
		slog.Warn("risk policy lookup failed, denying conservatively", "org_id", orgID, "err", err)
		return Decision{Action: core.RiskDecisionDeny, Reasons: []string{"policy lookup failed: " + err.Error()}}
	}
	if !ok {
		decision := Evaluate(DefaultPolicy, inputs)
		return decision
	}
	parsed, err := Parse([]byte(active.Source))
	if err != nil {
		slog.Warn("active risk policy failed to parse, denying conservatively",
			"org_id", orgID, "policy_id", active.PolicyID, "version", active.Version, "err", err)
		return Decision{
			Action:        core.RiskDecisionDeny,
			Reasons:       []string{"active policy parse error: " + err.Error()},
			PolicyID:      active.PolicyID,
			PolicyVersion: active.Version,
		}
	}
	parsed.Version = active.Version
	decision := Evaluate(parsed, inputs)
	decision.PolicyID = active.PolicyID
	decision.PolicyVersion = active.Version
	return decision
}

func (s *Service) requireAdmin(ctx context.Context, agent core.Agent) error {
	if s.users == nil {
		return nil
	}
	user, ok, err := s.users.FindUserByID(ctx, agent.OwnerUserID)
	if err != nil {
		return fmt.Errorf("find acting user: %w", err)
	}
	if !ok {
		return fmt.Errorf("acting user not found")
	}
	if user.Role != core.UserRoleAdmin {
		return ErrNotOrgAdmin
	}
	return nil
}
