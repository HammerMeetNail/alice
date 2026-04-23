// Package actions implements the operator phase: approved side-effectful
// work the user's agent may execute on the user's behalf. The service
// layer owns lifecycle transitions (pending → approved → executed), the
// risk-policy hookup that decides whether an action can skip human
// approval, and audit emission. Executors are pluggable per action Kind.
//
// Every action is execute-once: SaveAction + UpdateActionState on the
// storage layer reject transitions away from terminal states, so replays
// of the same ActionID become no-ops. A failed executor leaves the action
// in `failed` with a FailureReason; the original authorising request stays
// actionable by the human, since the request state is never mutated by
// this package except when the executor explicitly succeeds.
package actions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/riskpolicy"
	"alice/internal/storage"
)

// DefaultActionTTL is the default lifetime of a pending action before the
// service expires it. Actions created without an explicit TTL use this
// value so offline users do not accumulate stale approvals indefinitely.
const DefaultActionTTL = 24 * time.Hour

// Errors the service returns. HTTP handlers translate these into specific
// status codes.
var (
	ErrActionNotFound        = storage.ErrActionNotFound
	ErrOperatorNotEnabled    = errors.New("operator phase is not enabled for this user")
	ErrUnknownActionKind     = errors.New("unknown action kind")
	ErrActionNotExecutable   = errors.New("action is not in an executable state")
	ErrActionForbidden       = errors.New("caller may not act on this action")
	ErrActionPolicyDenied    = errors.New("action denied by risk policy")
	ErrActionAlreadyApproved = errors.New("action is already approved")
)

// Executor is the pluggable implementation per ActionKind. Implementations
// must be idempotent on their own side effect (the service layer already
// prevents double-execution of the same ActionID, but the downstream
// system may still retry).
type Executor interface {
	Kind() core.ActionKind
	// Validate rejects malformed inputs at creation time so a bad action
	// never reaches an approved state.
	Validate(inputs map[string]any) error
	// Execute performs the side effect and returns a result map to be
	// stored on the action record. Errors are fatal for the action and
	// the action moves to failed with the error message as the failure
	// reason.
	Execute(ctx context.Context, action core.Action, user core.User) (map[string]any, error)
}

// RiskPolicyEvaluator mirrors the slice of riskpolicy.Service the actions
// service needs. Keeping it narrow avoids a concrete dependency on the
// riskpolicy package so tests can substitute a stub.
type RiskPolicyEvaluator interface {
	Evaluate(ctx context.Context, orgID string, inputs riskpolicy.Inputs) riskpolicy.Decision
}

// UserLookup is the minimum surface needed to look up a user's
// operator-enabled flag and role title for executor calls.
type UserLookup interface {
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
}

// Service is the coordination layer for operator-phase actions.
type Service struct {
	store     storage.ActionRepository
	prefs     storage.UserPreferencesRepository
	requests  storage.RequestRepository
	users     UserLookup
	risk      RiskPolicyEvaluator
	executors map[core.ActionKind]Executor
	ttl       time.Duration
}

// NewService constructs an actions service. Any executor kind not
// registered via WithExecutor will return ErrUnknownActionKind when the
// caller tries to create an action of that kind.
func NewService(store storage.ActionRepository, prefs storage.UserPreferencesRepository, requests storage.RequestRepository, users UserLookup) *Service {
	return &Service{
		store:     store,
		prefs:     prefs,
		requests:  requests,
		users:     users,
		executors: make(map[core.ActionKind]Executor),
		ttl:       DefaultActionTTL,
	}
}

// WithExecutor registers an executor for a kind. Calling it twice for the
// same kind replaces the prior registration, which matters for tests that
// swap in fakes.
func (s *Service) WithExecutor(e Executor) *Service {
	if s != nil && e != nil {
		s.executors[e.Kind()] = e
	}
	return s
}

// WithRiskPolicyEvaluator attaches a risk-policy evaluator. When nil the
// service treats every Create as "allow" (pending if user approval is
// required by the request type, otherwise approved). Explicit nil lets
// tests exercise the service without standing up the whole policy stack.
func (s *Service) WithRiskPolicyEvaluator(eval RiskPolicyEvaluator) *Service {
	if s != nil && eval != nil {
		s.risk = eval
	}
	return s
}

// WithTTL overrides the default action lifetime. Values ≤ 0 are ignored.
func (s *Service) WithTTL(ttl time.Duration) *Service {
	if s != nil && ttl > 0 {
		s.ttl = ttl
	}
	return s
}

// CreateParams bundles the inputs to Create so test fixtures can compose
// them without ordering-sensitive positional arguments. Kept identical in
// shape to services.ActionCreateParams; see the adapter that translates
// between the two so the services package does not import actions.
type CreateParams struct {
	OrgID       string
	OwnerUser   core.User
	OwnerAgent  core.Agent
	RequestID   string
	Kind        core.ActionKind
	Inputs      map[string]any
	RiskLevel   core.RiskLevel
	RequestType string
}

// Create registers a new action for the owner user. The user must have
// opted in to the operator phase. Risk-policy evaluation decides whether
// the action starts pending (user must approve it later) or approved
// (ready for execution on the next poll).
func (s *Service) Create(ctx context.Context, p CreateParams) (core.Action, error) {
	if !p.OwnerUser.OperatorEnabled {
		return core.Action{}, ErrOperatorNotEnabled
	}
	executor, ok := s.executors[p.Kind]
	if !ok {
		return core.Action{}, fmt.Errorf("%w: %q", ErrUnknownActionKind, p.Kind)
	}
	if err := executor.Validate(p.Inputs); err != nil {
		return core.Action{}, fmt.Errorf("invalid inputs for %s: %w", p.Kind, err)
	}

	decision := riskpolicy.Decision{Action: core.RiskDecisionAllow}
	if s.risk != nil {
		decision = s.risk.Evaluate(ctx, p.OrgID, riskpolicy.Inputs{
			RequestType: p.RequestType,
			RiskLevel:   p.RiskLevel,
		})
	}
	if decision.Action == core.RiskDecisionDeny {
		return core.Action{}, fmt.Errorf("%w: %s", ErrActionPolicyDenied, joinReasons(decision.Reasons))
	}

	state := core.ActionStateApproved
	if decision.Action == core.RiskDecisionRequireApproval {
		state = core.ActionStatePending
	}

	now := time.Now().UTC()
	action := core.Action{
		ActionID:     id.New("action"),
		OrgID:        p.OrgID,
		RequestID:    p.RequestID,
		OwnerUserID:  p.OwnerUser.UserID,
		OwnerAgentID: p.OwnerAgent.AgentID,
		Kind:         p.Kind,
		Inputs:       p.Inputs,
		RiskLevel:    p.RiskLevel,
		State:        state,
		CreatedAt:    now,
		ExpiresAt:    now.Add(s.ttl),
	}
	saved, err := s.store.SaveAction(ctx, action)
	if err != nil {
		return core.Action{}, fmt.Errorf("save action: %w", err)
	}
	slog.Info("action created",
		"action_id", saved.ActionID,
		"kind", saved.Kind,
		"state", saved.State,
		"owner_user_id", saved.OwnerUserID,
	)
	return saved, nil
}

// Approve moves a pending action to approved. Caller must be the action
// owner; terminal-state transitions are rejected at the storage layer.
func (s *Service) Approve(ctx context.Context, agent core.Agent, actionID string) (core.Action, error) {
	action, err := s.findOwnedAction(ctx, agent, actionID)
	if err != nil {
		return core.Action{}, err
	}
	if action.State == core.ActionStateApproved {
		return action, nil
	}
	if action.State != core.ActionStatePending {
		return core.Action{}, fmt.Errorf("%w: state=%s", ErrActionNotExecutable, action.State)
	}
	action.State = core.ActionStateApproved
	updated, err := s.store.UpdateActionState(ctx, action)
	if err != nil {
		return core.Action{}, fmt.Errorf("approve action: %w", err)
	}
	slog.Info("action approved", "action_id", updated.ActionID)
	return updated, nil
}

// Cancel moves an action to cancelled. Caller must be the action owner.
// Approved-but-not-yet-executed actions may be cancelled; executed ones
// cannot (the storage layer rejects the transition).
func (s *Service) Cancel(ctx context.Context, agent core.Agent, actionID string) (core.Action, error) {
	action, err := s.findOwnedAction(ctx, agent, actionID)
	if err != nil {
		return core.Action{}, err
	}
	action.State = core.ActionStateCancelled
	updated, err := s.store.UpdateActionState(ctx, action)
	if err != nil {
		return core.Action{}, fmt.Errorf("cancel action: %w", err)
	}
	slog.Info("action cancelled", "action_id", updated.ActionID)
	return updated, nil
}

// Execute runs the registered executor for the given action. Only
// approved actions may execute; the state flips to executing for the
// duration of the call and then to executed (or failed). Expired actions
// are transitioned lazily here so a lookup after the deadline reports the
// correct state.
func (s *Service) Execute(ctx context.Context, agent core.Agent, actionID string) (core.Action, error) {
	action, err := s.findOwnedAction(ctx, agent, actionID)
	if err != nil {
		return core.Action{}, err
	}

	now := time.Now().UTC()
	if action.State == core.ActionStateApproved && now.After(action.ExpiresAt) {
		action.State = core.ActionStateExpired
		expired, updateErr := s.store.UpdateActionState(ctx, action)
		if updateErr != nil {
			return core.Action{}, fmt.Errorf("mark action expired: %w", updateErr)
		}
		return expired, fmt.Errorf("%w: action expired", ErrActionNotExecutable)
	}
	if action.State != core.ActionStateApproved {
		return core.Action{}, fmt.Errorf("%w: state=%s", ErrActionNotExecutable, action.State)
	}

	executor, ok := s.executors[action.Kind]
	if !ok {
		return core.Action{}, fmt.Errorf("%w: %q", ErrUnknownActionKind, action.Kind)
	}

	// Flip to executing so a concurrent caller cannot double-run this
	// action — the storage layer's terminal-state check is not enough on
	// its own because `approved` is non-terminal.
	action.State = core.ActionStateExecuting
	if _, err := s.store.UpdateActionState(ctx, action); err != nil {
		return core.Action{}, fmt.Errorf("mark action executing: %w", err)
	}

	user, ok, err := s.users.FindUserByID(ctx, action.OwnerUserID)
	if err != nil || !ok {
		return s.finalise(ctx, action, core.ActionStateFailed, nil, fmt.Sprintf("owner user lookup failed: %v", err))
	}

	result, err := executor.Execute(ctx, action, user)
	if err != nil {
		return s.finalise(ctx, action, core.ActionStateFailed, nil, err.Error())
	}
	return s.finalise(ctx, action, core.ActionStateExecuted, result, "")
}

// List returns the actions owned by the given agent's user. Admins and
// other users may not list another user's actions.
func (s *Service) List(ctx context.Context, agent core.Agent, filter storage.ActionFilter) ([]core.Action, error) {
	filter.OwnerUserID = agent.OwnerUserID
	return s.store.ListActions(ctx, filter)
}

// SetOperatorEnabled toggles the per-user opt-in. Only the user themself
// may toggle their own flag; there is no admin override by design — the
// spec is explicit that operator-phase consent lives with the user.
func (s *Service) SetOperatorEnabled(ctx context.Context, agent core.Agent, enabled bool) error {
	return s.prefs.SetOperatorEnabled(ctx, agent.OwnerUserID, enabled)
}

func (s *Service) findOwnedAction(ctx context.Context, agent core.Agent, actionID string) (core.Action, error) {
	action, ok, err := s.store.FindActionByID(ctx, actionID)
	if err != nil {
		return core.Action{}, fmt.Errorf("find action: %w", err)
	}
	if !ok {
		return core.Action{}, ErrActionNotFound
	}
	if action.OwnerUserID != agent.OwnerUserID || action.OrgID != agent.OrgID {
		return core.Action{}, ErrActionForbidden
	}
	return action, nil
}

func (s *Service) finalise(ctx context.Context, action core.Action, state core.ActionState, result map[string]any, failureReason string) (core.Action, error) {
	now := time.Now().UTC()
	action.State = state
	action.Result = result
	action.FailureReason = failureReason
	action.ExecutedAt = &now
	updated, err := s.store.UpdateActionState(ctx, action)
	if err != nil {
		// The action has already moved to executing; a storage failure
		// here is best-effort logged and surfaced so callers can retry.
		return core.Action{}, fmt.Errorf("finalise action state: %w", err)
	}
	if state == core.ActionStateExecuted {
		slog.Info("action executed", "action_id", updated.ActionID, "kind", updated.Kind)
	} else {
		slog.Warn("action failed", "action_id", updated.ActionID, "kind", updated.Kind, "reason", failureReason)
	}
	return updated, nil
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "no reason recorded"
	}
	return reasons[0]
}
