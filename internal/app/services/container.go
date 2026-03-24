package services

import (
	"context"
	"time"

	"alice/internal/core"
)

type AgentService interface {
	BeginRegistration(ctx context.Context, orgSlug, ownerEmail, agentName, clientType, publicKey string) (core.AgentRegistrationChallenge, string, error)
	CompleteRegistration(ctx context.Context, challengeID, challengeSignature string) (core.Organization, core.User, core.Agent, string, time.Time, error)
	AuthenticateAgent(ctx context.Context, accessToken string) (core.Agent, core.User, error)
	RequireAgent(ctx context.Context, agentID string) (core.Agent, core.User, error)
	FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error)
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
	FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error)
}

type ArtifactService interface {
	PublishArtifact(ctx context.Context, agent core.Agent, user core.User, artifact core.Artifact) (core.Artifact, error)
	ListArtifactsByOwner(ctx context.Context, userID string) ([]core.Artifact, error)
}

type PolicyService interface {
	Grant(ctx context.Context, orgID string, grantorUser core.User, granteeUser core.User, scopeType, scopeRef string, artifactTypes []core.ArtifactType, maxSensitivity core.Sensitivity, purposes []core.QueryPurpose) (core.PolicyGrant, error)
	RevokeGrant(ctx context.Context, grantID, grantorUserID string) (core.PolicyGrant, error)
	ListAllowedPeers(ctx context.Context, granteeUserID string) ([]core.PolicyGrant, error)
}

type QueryService interface {
	Evaluate(ctx context.Context, query core.Query) (core.QueryResponse, error)
	FindResult(ctx context.Context, queryID string) (core.Query, core.QueryResponse, bool, error)
}

type RequestService interface {
	Send(ctx context.Context, request core.Request) (core.Request, error)
	ListIncoming(ctx context.Context, agentID string) ([]core.Request, error)
	Respond(ctx context.Context, agent core.Agent, requestID string, action core.RequestResponseAction, message string) (core.Request, *core.Approval, error)
}

type ApprovalService interface {
	ListPending(ctx context.Context, agentID string) ([]core.Approval, error)
	Resolve(ctx context.Context, agent core.Agent, approvalID string, decision core.ApprovalState) (core.Approval, core.Request, error)
}

type AuditService interface {
	Record(ctx context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) (core.AuditEvent, error)
	Summary(ctx context.Context, agentID string, since time.Time) ([]core.AuditEvent, error)
}

type Container struct {
	Agents    AgentService
	Artifacts ArtifactService
	Policy    PolicyService
	Queries   QueryService
	Requests  RequestService
	Approvals ApprovalService
	Audit     AuditService
}
