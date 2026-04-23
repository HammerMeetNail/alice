package services

import (
	"context"
	"time"

	"alice/internal/agents"
	"alice/internal/audit"
	"alice/internal/core"
	"alice/internal/storage"
)

type AgentService interface {
	BeginRegistration(ctx context.Context, orgSlug, ownerEmail, agentName, clientType, publicKey, inviteToken string) (agents.BeginRegistrationResult, error)
	CompleteRegistration(ctx context.Context, challengeID, challengeSignature string) (agents.CompleteRegistrationResult, error)
	AuthenticateAgent(ctx context.Context, accessToken string) (core.Agent, core.User, error)
	RequireAgent(ctx context.Context, agentID string) (core.Agent, core.User, error)
	FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error)
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
	FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error)
	VerifyEmail(ctx context.Context, agentID, code string) error
	ResendVerificationEmail(ctx context.Context, agentID string) error
	RotateInviteToken(ctx context.Context, orgID, callerAgentID string) (string, error)
	UpdateVerificationMode(ctx context.Context, agent core.Agent, mode string) (core.Organization, error)
	UpdateGatekeeperTuning(ctx context.Context, agent core.Agent, threshold *float64, window *time.Duration) (core.Organization, error)
	ListPendingAgentApprovals(ctx context.Context, orgID, callerAgentID string, limit, offset int) ([]core.AgentApproval, error)
	ReviewAgentApproval(ctx context.Context, orgID, targetAgentID, callerAgentID, decision, reason string) error
}

type ArtifactService interface {
	PublishArtifact(ctx context.Context, agent core.Agent, user core.User, artifact core.Artifact) (core.Artifact, error)
	CorrectArtifact(ctx context.Context, agent core.Agent, user core.User, originalArtifactID string, correction core.Artifact) (core.Artifact, error)
	ListArtifactsByOwner(ctx context.Context, userID string) ([]core.Artifact, error)
}

type PolicyService interface {
	Grant(ctx context.Context, orgID string, grantorUser core.User, granteeUser core.User, scopeType, scopeRef string, artifactTypes []core.ArtifactType, maxSensitivity core.Sensitivity, purposes []core.QueryPurpose) (core.PolicyGrant, error)
	RevokeGrant(ctx context.Context, grantID, grantorUserID string) (core.PolicyGrant, error)
	ListAllowedPeers(ctx context.Context, granteeUserID string, limit, offset int) ([]core.PolicyGrant, error)
}

type QueryService interface {
	Evaluate(ctx context.Context, query core.Query) (core.QueryResponse, error)
	FindResult(ctx context.Context, queryID string) (core.Query, core.QueryResponse, bool, error)
}

type RequestService interface {
	Send(ctx context.Context, request core.Request) (core.Request, error)
	ListIncoming(ctx context.Context, agentID string, limit, offset int) ([]core.Request, error)
	ListSent(ctx context.Context, agentID string, limit, offset int) ([]core.Request, error)
	Respond(ctx context.Context, agent core.Agent, requestID string, action core.RequestResponseAction, message string) (core.Request, *core.Approval, error)
}

type ApprovalService interface {
	ListPending(ctx context.Context, agentID string, limit, offset int) ([]core.Approval, error)
	Resolve(ctx context.Context, agent core.Agent, approvalID string, decision core.ApprovalState) (core.Approval, core.Request, error)
}

type AuditService interface {
	Record(ctx context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) (core.AuditEvent, error)
	Summary(ctx context.Context, agentID string, since time.Time, limit, offset int, filter audit.SummaryFilter) ([]core.AuditEvent, error)
}

// RiskPolicyService is the admin-gated management surface for per-org risk
// policies. The router uses this directly; the queries path consumes the
// evaluator adapter (see riskpolicy.AsQueriesEvaluator) and does not touch
// this interface.
type RiskPolicyService interface {
	Apply(ctx context.Context, agent core.Agent, name string, source []byte) (core.RiskPolicy, error)
	Activate(ctx context.Context, agent core.Agent, policyID string) (core.RiskPolicy, error)
	History(ctx context.Context, agent core.Agent, limit, offset int) ([]core.RiskPolicy, error)
}

// ActionService is the surface the HTTP/CLI/MCP layers use to manage
// operator-phase actions. The concrete actions package depends on the
// storage and riskpolicy layers; this interface keeps httpapi free of
// those transitive imports.
type ActionService interface {
	CreateFromServicesParams(ctx context.Context, params ActionCreateParams) (core.Action, error)
	Approve(ctx context.Context, agent core.Agent, actionID string) (core.Action, error)
	Cancel(ctx context.Context, agent core.Agent, actionID string) (core.Action, error)
	Execute(ctx context.Context, agent core.Agent, actionID string) (core.Action, error)
	List(ctx context.Context, agent core.Agent, filter storage.ActionFilter) ([]core.Action, error)
	SetOperatorEnabled(ctx context.Context, agent core.Agent, enabled bool) error
}

// ActionCreateParams mirrors actions.CreateParams but lives in this
// interface-only package so httpapi can populate it without importing
// actions directly.
type ActionCreateParams struct {
	OrgID       string
	OwnerUser   core.User
	OwnerAgent  core.Agent
	RequestID   string
	Kind        core.ActionKind
	Inputs      map[string]any
	RiskLevel   core.RiskLevel
	RequestType string
}

type Container struct {
	Agents     AgentService
	Artifacts  ArtifactService
	Policy     PolicyService
	Queries    QueryService
	Requests   RequestService
	Approvals  ApprovalService
	Audit      AuditService
	RiskPolicy RiskPolicyService
	Actions    ActionService
}
