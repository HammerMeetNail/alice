package actions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"alice/internal/actions"
	"alice/internal/app/services"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/riskpolicy"
	"alice/internal/storage"
	"alice/internal/storage/memory"
)

// newTestFixture builds a memory-backed service with the acknowledge_blocker
// executor wired and a single enabled user who owns one pending request.
// Tests drive Create/Approve/Execute against this fixture so every path
// shares the same setup without hidden state drift.
type testFixture struct {
	ctx     context.Context
	store   *memory.Store
	svc     *actions.Service
	user    core.User
	agent   core.Agent
	request core.Request
}

func newTestFixture(t *testing.T) testFixture {
	t.Helper()
	ctx := context.Background()
	store := memory.New()

	org := core.Organization{OrgID: id.New("org"), Slug: "ops-org", Status: "active"}
	store.UpsertOrganization(ctx, org)

	user := core.User{
		UserID:          id.New("user"),
		OrgID:           org.OrgID,
		Email:           "alice@example.com",
		Status:          "active",
		Role:            core.UserRoleAdmin,
		OperatorEnabled: true,
	}
	store.UpsertUser(ctx, user)

	agent := core.Agent{AgentID: id.New("agent"), OrgID: org.OrgID, OwnerUserID: user.UserID, Status: "active"}
	store.UpsertAgent(ctx, agent)

	request := core.Request{
		RequestID:   id.New("request"),
		OrgID:       org.OrgID,
		FromAgentID: id.New("agent"),
		FromUserID:  id.New("user"),
		ToAgentID:   agent.AgentID,
		ToUserID:    user.UserID,
		RequestType: "blocker",
		Title:       "Paying workflow blocked",
		Content:     "retry service returning 500",
		State:       core.RequestStatePending,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
	}
	store.SaveRequest(ctx, request)

	svc := actions.NewService(store, store, store, store).
		WithExecutor(actions.NewAcknowledgeBlockerExecutor(store))
	return testFixture{ctx: ctx, store: store, svc: svc, user: user, agent: agent, request: request}
}

func TestCreateRequiresOperatorEnabled(t *testing.T) {
	f := newTestFixture(t)
	f.user.OperatorEnabled = false
	if _, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "got it"},
	}); !errors.Is(err, actions.ErrOperatorNotEnabled) {
		t.Fatalf("expected ErrOperatorNotEnabled, got %v", err)
	}
}

func TestCreateRejectsUnknownKind(t *testing.T) {
	f := newTestFixture(t)
	if _, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		Kind:       core.ActionKind("send_carrier_pigeon"),
		Inputs:     map[string]any{},
	}); !errors.Is(err, actions.ErrUnknownActionKind) {
		t.Fatalf("expected ErrUnknownActionKind, got %v", err)
	}
}

func TestCreateValidatesInputs(t *testing.T) {
	f := newTestFixture(t)
	if _, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{},
	}); err == nil {
		t.Fatal("expected validation error for missing message")
	}
}

func TestHappyPath_CreateExecuteCompletesRequest(t *testing.T) {
	f := newTestFixture(t)
	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "acknowledged — digging in"},
		RiskLevel:  core.RiskLevelL0,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if action.State != core.ActionStateApproved {
		t.Fatalf("expected approved state, got %v", action.State)
	}

	executed, err := f.svc.Execute(f.ctx, f.agent, action.ActionID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if executed.State != core.ActionStateExecuted {
		t.Fatalf("expected executed state, got %v (reason=%s)", executed.State, executed.FailureReason)
	}

	req, ok, _ := f.store.FindRequest(f.ctx, f.request.RequestID)
	if !ok {
		t.Fatal("request vanished")
	}
	if req.State != core.RequestStateCompleted {
		t.Fatalf("expected request to be completed, got %v", req.State)
	}
	if req.ResponseMessage != "acknowledged — digging in" {
		t.Fatalf("unexpected response_message: %q", req.ResponseMessage)
	}
}

func TestExecuteReplayIsNoOp(t *testing.T) {
	f := newTestFixture(t)
	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "ok"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.svc.Execute(f.ctx, f.agent, action.ActionID); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	// The second execute must be a no-op: state is terminal, so the
	// service rejects before retrying the executor. Replays therefore
	// cannot duplicate side effects.
	if _, err := f.svc.Execute(f.ctx, f.agent, action.ActionID); !errors.Is(err, actions.ErrActionNotExecutable) {
		t.Fatalf("expected ErrActionNotExecutable on replay, got %v", err)
	}
}

func TestRiskPolicyCanRequireApproval(t *testing.T) {
	f := newTestFixture(t)
	// Stand up a policy that always requires approval for this request
	// type so Create returns a pending action.
	policySvc := riskpolicy.NewService(f.store, nil)
	if _, err := policySvc.Apply(f.ctx, f.agent, "strict", []byte(`{"rules":[
		{"when":{"request_type":"blocker"},"then":"require_approval","reason":"review first"},
		{"when":{},"then":"allow"}
	]}`)); err != nil {
		t.Fatalf("apply policy: %v", err)
	}
	f.svc = f.svc.WithRiskPolicyEvaluator(policySvc)

	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:       f.user.OrgID,
		OwnerUser:   f.user,
		OwnerAgent:  f.agent,
		RequestID:   f.request.RequestID,
		Kind:        core.ActionKindAcknowledgeBlocker,
		Inputs:      map[string]any{"message": "ok"},
		RequestType: "blocker",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if action.State != core.ActionStatePending {
		t.Fatalf("expected pending state under require_approval policy, got %v", action.State)
	}

	// Executing a pending action must fail.
	if _, err := f.svc.Execute(f.ctx, f.agent, action.ActionID); !errors.Is(err, actions.ErrActionNotExecutable) {
		t.Fatalf("expected ErrActionNotExecutable, got %v", err)
	}

	// After approval, execute succeeds.
	if _, err := f.svc.Approve(f.ctx, f.agent, action.ActionID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := f.svc.Execute(f.ctx, f.agent, action.ActionID); err != nil {
		t.Fatalf("Execute after approve: %v", err)
	}
}

func TestRiskPolicyCanDenyAtCreateTime(t *testing.T) {
	f := newTestFixture(t)
	policySvc := riskpolicy.NewService(f.store, nil)
	if _, err := policySvc.Apply(f.ctx, f.agent, "deny", []byte(`{"rules":[{"when":{},"then":"deny","reason":"kill switch"}]}`)); err != nil {
		t.Fatalf("apply policy: %v", err)
	}
	f.svc = f.svc.WithRiskPolicyEvaluator(policySvc)

	_, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "ok"},
	})
	if !errors.Is(err, actions.ErrActionPolicyDenied) {
		t.Fatalf("expected ErrActionPolicyDenied, got %v", err)
	}
}

func TestFindOwnershipIsolation(t *testing.T) {
	f := newTestFixture(t)
	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "ok"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	otherAgent := core.Agent{AgentID: id.New("agent"), OrgID: f.user.OrgID, OwnerUserID: id.New("user")}
	if _, err := f.svc.Approve(f.ctx, otherAgent, action.ActionID); !errors.Is(err, actions.ErrActionForbidden) {
		t.Fatalf("expected ErrActionForbidden for non-owner, got %v", err)
	}
}

func TestExecuteOfExpiredActionTransitionsToExpired(t *testing.T) {
	f := newTestFixture(t)
	f.svc = f.svc.WithTTL(time.Millisecond)
	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "ok"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_, err = f.svc.Execute(f.ctx, f.agent, action.ActionID)
	if !errors.Is(err, actions.ErrActionNotExecutable) {
		t.Fatalf("expected expired action to be not-executable, got %v", err)
	}
	got, _, _ := f.store.FindActionByID(f.ctx, action.ActionID)
	if got.State != core.ActionStateExpired {
		t.Fatalf("expected action to be expired, got %v", got.State)
	}
}

func TestSetOperatorEnabledFlipsFlag(t *testing.T) {
	f := newTestFixture(t)
	if err := f.svc.SetOperatorEnabled(f.ctx, f.agent, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	user, _, _ := f.store.FindUserByID(f.ctx, f.user.UserID)
	if user.OperatorEnabled {
		t.Fatal("expected OperatorEnabled=false after disable")
	}
	if err := f.svc.SetOperatorEnabled(f.ctx, f.agent, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	user, _, _ = f.store.FindUserByID(f.ctx, f.user.UserID)
	if !user.OperatorEnabled {
		t.Fatal("expected OperatorEnabled=true after enable")
	}
}

func TestCancel(t *testing.T) {
	f := newTestFixture(t)
	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "cancelling"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if action.State != core.ActionStateApproved {
		t.Fatalf("expected approved state, got %v", action.State)
	}

	cancelled, err := f.svc.Cancel(f.ctx, f.agent, action.ActionID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.State != core.ActionStateCancelled {
		t.Fatalf("expected cancelled state, got %v", cancelled.State)
	}

	// Cancel on a non-existent action returns ErrActionNotFound.
	if _, err := f.svc.Cancel(f.ctx, f.agent, id.New("action")); !errors.Is(err, actions.ErrActionNotFound) {
		t.Fatalf("expected ErrActionNotFound, got %v", err)
	}

	// Cancel on a terminal action returns ErrActionInTerminalState (wrapped).
	_, cancelErr := f.svc.Cancel(f.ctx, f.agent, action.ActionID)
	if cancelErr == nil {
		t.Fatal("expected error cancelling terminal action")
	}

	// Cancel on action owned by another agent returns ErrActionForbidden.
	other := core.Agent{AgentID: id.New("agent"), OrgID: f.user.OrgID, OwnerUserID: id.New("user")}
	if _, err := f.svc.Cancel(f.ctx, other, action.ActionID); !errors.Is(err, actions.ErrActionForbidden) {
		t.Fatalf("expected ErrActionForbidden, got %v", err)
	}
}

func TestList(t *testing.T) {
	f := newTestFixture(t)

	// List with empty store returns empty slice.
	items, err := f.svc.List(f.ctx, f.agent, storage.ActionFilter{})
	if err != nil {
		t.Fatalf("List empty: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	// Create an action.
	action, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "listing"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	items, err = f.svc.List(f.ctx, f.agent, storage.ActionFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("List after create: len=%d err=%v", len(items), err)
	}
	if items[0].ActionID != action.ActionID {
		t.Fatalf("unexpected action id: %s", items[0].ActionID)
	}

	// Filter by state: no match.
	filtered, err := f.svc.List(f.ctx, f.agent, storage.ActionFilter{State: core.ActionStatePending})
	if err != nil || len(filtered) != 0 {
		t.Fatalf("List state filter no match: len=%d err=%v", len(filtered), err)
	}

	// Filter by state: match.
	matched, err := f.svc.List(f.ctx, f.agent, storage.ActionFilter{State: core.ActionStateApproved})
	if err != nil || len(matched) != 1 {
		t.Fatalf("List state filter match: len=%d err=%v", len(matched), err)
	}
}

func TestCreateFromServicesParams(t *testing.T) {
	f := newTestFixture(t)

	action, err := f.svc.CreateFromServicesParams(f.ctx, services.ActionCreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "adapter test"},
		RiskLevel:  core.RiskLevelL0,
	})
	if err != nil {
		t.Fatalf("CreateFromServicesParams: %v", err)
	}
	if action.State != core.ActionStateApproved {
		t.Fatalf("expected approved state, got %v", action.State)
	}
}

func TestJoinReasons_ViaCreate(t *testing.T) {
	// Trigger the joinReasons path via a deny policy with a reason.
	f := newTestFixture(t)
	policySvc := riskpolicy.NewService(f.store, nil)
	if _, err := policySvc.Apply(f.ctx, f.agent, "deny-with-reason", []byte(`{"rules":[{"when":{},"then":"deny","reason":"kill switch active"}]}`)); err != nil {
		t.Fatalf("apply policy: %v", err)
	}
	f.svc = f.svc.WithRiskPolicyEvaluator(policySvc)

	_, err := f.svc.Create(f.ctx, actions.CreateParams{
		OrgID:      f.user.OrgID,
		OwnerUser:  f.user,
		OwnerAgent: f.agent,
		RequestID:  f.request.RequestID,
		Kind:       core.ActionKindAcknowledgeBlocker,
		Inputs:     map[string]any{"message": "ok"},
	})
	if !errors.Is(err, actions.ErrActionPolicyDenied) {
		t.Fatalf("expected ErrActionPolicyDenied, got %v", err)
	}
}
