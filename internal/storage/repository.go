package storage

import (
	"time"

	"alice/internal/core"
)

type OrganizationRepository interface {
	UpsertOrganization(org core.Organization) (core.Organization, error)
	FindOrganizationBySlug(slug string) (core.Organization, bool, error)
}

type UserRepository interface {
	UpsertUser(user core.User) (core.User, error)
	FindUserByEmail(email string) (core.User, bool, error)
	FindUserByID(userID string) (core.User, bool, error)
}

type AgentRepository interface {
	UpsertAgent(agent core.Agent) (core.Agent, error)
	FindAgentByID(agentID string) (core.Agent, bool, error)
	FindAgentByUserID(userID string) (core.Agent, bool, error)
}

type AgentRegistrationChallengeRepository interface {
	SaveAgentRegistrationChallenge(challenge core.AgentRegistrationChallenge) (core.AgentRegistrationChallenge, error)
	FindAgentRegistrationChallenge(challengeID string) (core.AgentRegistrationChallenge, bool, error)
}

type AgentTokenRepository interface {
	SaveAgentToken(token core.AgentToken) (core.AgentToken, error)
	FindAgentTokenByID(tokenID string) (core.AgentToken, bool, error)
}

type ArtifactRepository interface {
	SaveArtifact(artifact core.Artifact) (core.Artifact, error)
	ListArtifactsByOwner(userID string) ([]core.Artifact, error)
}

type PolicyGrantRepository interface {
	SaveGrant(grant core.PolicyGrant) (core.PolicyGrant, error)
	ListGrantsForPair(grantorUserID, granteeUserID string) ([]core.PolicyGrant, error)
	ListIncomingGrantsForUser(granteeUserID string) ([]core.PolicyGrant, error)
}

type QueryRepository interface {
	SaveQuery(query core.Query) (core.Query, error)
	SaveQueryResponse(response core.QueryResponse) (core.QueryResponse, error)
	UpdateQueryState(queryID string, state core.QueryState) (core.Query, bool, error)
	FindQuery(queryID string) (core.Query, bool, error)
	FindQueryResponse(queryID string) (core.QueryResponse, bool, error)
}

type RequestRepository interface {
	SaveRequest(request core.Request) (core.Request, error)
	FindRequest(requestID string) (core.Request, bool, error)
	ListIncomingRequests(toAgentID string) ([]core.Request, error)
	UpdateRequestState(requestID string, state core.RequestState, approvalState core.ApprovalState, responseMessage string) (core.Request, bool, error)
}

type ApprovalRepository interface {
	SaveApproval(approval core.Approval) (core.Approval, error)
	FindApproval(approvalID string) (core.Approval, bool, error)
	ListPendingApprovals(agentID string) ([]core.Approval, error)
	ResolveApproval(approvalID string, state core.ApprovalState, resolvedAt time.Time) (core.Approval, bool, error)
}

type AuditRepository interface {
	AppendAuditEvent(event core.AuditEvent) (core.AuditEvent, error)
	ListAuditEvents(agentID string, since time.Time) ([]core.AuditEvent, error)
}
