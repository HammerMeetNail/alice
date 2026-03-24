package storage

import (
	"context"
	"errors"
	"time"

	"alice/internal/core"
)

// ErrChallengeAlreadyUsed is returned by SaveAgentRegistrationChallenge when
// the challenge has already been marked used by a concurrent completion attempt.
var ErrChallengeAlreadyUsed = errors.New("registration challenge already used")

type OrganizationRepository interface {
	UpsertOrganization(ctx context.Context, org core.Organization) (core.Organization, error)
	FindOrganizationBySlug(ctx context.Context, slug string) (core.Organization, bool, error)
}

type UserRepository interface {
	UpsertUser(ctx context.Context, user core.User) (core.User, error)
	FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error)
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
}

type AgentRepository interface {
	UpsertAgent(ctx context.Context, agent core.Agent) (core.Agent, error)
	FindAgentByID(ctx context.Context, agentID string) (core.Agent, bool, error)
	FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error)
}

type AgentRegistrationChallengeRepository interface {
	SaveAgentRegistrationChallenge(ctx context.Context, challenge core.AgentRegistrationChallenge) (core.AgentRegistrationChallenge, error)
	FindAgentRegistrationChallenge(ctx context.Context, challengeID string) (core.AgentRegistrationChallenge, bool, error)
}

type AgentTokenRepository interface {
	SaveAgentToken(ctx context.Context, token core.AgentToken) (core.AgentToken, error)
	FindAgentTokenByID(ctx context.Context, tokenID string) (core.AgentToken, bool, error)
}

type ArtifactRepository interface {
	SaveArtifact(ctx context.Context, artifact core.Artifact) (core.Artifact, error)
	FindArtifactByID(ctx context.Context, artifactID string) (core.Artifact, bool, error)
	ListArtifactsByOwner(ctx context.Context, userID string) ([]core.Artifact, error)
}

type PolicyGrantRepository interface {
	SaveGrant(ctx context.Context, grant core.PolicyGrant) (core.PolicyGrant, error)
	FindGrant(ctx context.Context, grantID string) (core.PolicyGrant, bool, error)
	RevokeGrant(ctx context.Context, grantID, grantorUserID string) (core.PolicyGrant, error)
	ListGrantsForPair(ctx context.Context, grantorUserID, granteeUserID string) ([]core.PolicyGrant, error)
	ListIncomingGrantsForUser(ctx context.Context, granteeUserID string, limit, offset int) ([]core.PolicyGrant, error)
}

type QueryRepository interface {
	SaveQuery(ctx context.Context, query core.Query) (core.Query, error)
	SaveQueryResponse(ctx context.Context, response core.QueryResponse) (core.QueryResponse, error)
	UpdateQueryState(ctx context.Context, queryID string, state core.QueryState) (core.Query, bool, error)
	UpdateQueryResponseApprovalState(ctx context.Context, queryID string, state core.ApprovalState) (core.QueryResponse, bool, error)
	FindQuery(ctx context.Context, queryID string) (core.Query, bool, error)
	FindQueryResponse(ctx context.Context, queryID string) (core.QueryResponse, bool, error)
}

type RequestRepository interface {
	SaveRequest(ctx context.Context, request core.Request) (core.Request, error)
	FindRequest(ctx context.Context, requestID string) (core.Request, bool, error)
	ListIncomingRequests(ctx context.Context, toAgentID string, limit, offset int) ([]core.Request, error)
	UpdateRequestState(ctx context.Context, requestID string, state core.RequestState, approvalState core.ApprovalState, responseMessage string) (core.Request, bool, error)
}

type ApprovalRepository interface {
	SaveApproval(ctx context.Context, approval core.Approval) (core.Approval, error)
	FindApproval(ctx context.Context, approvalID string) (core.Approval, bool, error)
	ListPendingApprovals(ctx context.Context, agentID string, limit, offset int) ([]core.Approval, error)
	ResolveApproval(ctx context.Context, approvalID string, state core.ApprovalState, resolvedAt time.Time) (core.Approval, bool, error)
}

type AuditRepository interface {
	AppendAuditEvent(ctx context.Context, event core.AuditEvent) (core.AuditEvent, error)
	ListAuditEvents(ctx context.Context, agentID string, since time.Time, limit, offset int) ([]core.AuditEvent, error)
}

// StoreTx is the combined repository surface available within a transaction scope.
type StoreTx interface {
	OrganizationRepository
	UserRepository
	AgentRepository
	AgentRegistrationChallengeRepository
	AgentTokenRepository
	ArtifactRepository
	PolicyGrantRepository
	QueryRepository
	RequestRepository
	ApprovalRepository
	AuditRepository
}

// Transactor runs fn inside a single atomic transaction.
// On PostgreSQL, the underlying database transaction is committed on success
// and rolled back on error. On memory, fn is called directly.
type Transactor interface {
	WithTx(ctx context.Context, fn func(tx StoreTx) error) error
}
