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

// ErrVerificationNotFound is returned when no pending verification exists for an agent.
var ErrVerificationNotFound = errors.New("email verification not found")

// ErrOrgNotFound is returned when no organization matches the given criteria.
var ErrOrgNotFound = errors.New("organization not found")

// ErrOrgSlugTaken is returned by UpsertOrganization when the requested slug
// is already held by an existing (or soft-deleted) organization.  Org deletion
// is terminal; slugs cannot be reused.
var ErrOrgSlugTaken = errors.New("org slug already taken and cannot be reused")

// ErrAgentApprovalNotFound is returned when no agent approval record exists.
var ErrAgentApprovalNotFound = errors.New("agent approval not found")

type OrganizationRepository interface {
	UpsertOrganization(ctx context.Context, org core.Organization) (core.Organization, error)
	FindOrganizationBySlug(ctx context.Context, slug string) (core.Organization, bool, error)
	FindOrganizationByID(ctx context.Context, orgID string) (core.Organization, bool, error)
	UpdateOrgVerificationMode(ctx context.Context, orgID, mode string) error
	SetOrgInviteTokenHash(ctx context.Context, orgID, hash string) error
	// UpdateGatekeeperTuning sets (or clears, when either pointer is nil) the
	// per-org gatekeeper overrides. A nil threshold clears the persisted
	// override; same for window. Callers use this to express "revert to the
	// server-wide default".
	UpdateGatekeeperTuning(ctx context.Context, orgID string, threshold *float64, window *time.Duration) error
	FindOrgBySlug(ctx context.Context, slug string) (core.Organization, error)
	// SoftDeleteOrg marks the org as deleted. All users and agents within the
	// org must be soft-deleted by the caller before (or within the same tx).
	SoftDeleteOrg(ctx context.Context, orgID string) error
	// ListUserIDsByOrg returns all userIDs that belong to an org. Used by the
	// delete-org flow to cascade user/agent soft-deletion.
	ListUserIDsByOrg(ctx context.Context, orgID string) ([]string, error)
}

type UserRepository interface {
	UpsertUser(ctx context.Context, user core.User) (core.User, error)
	FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error)
	FindUserByID(ctx context.Context, userID string) (core.User, bool, error)
	UpdateUserRole(ctx context.Context, userID, role string) error
	// SoftDeleteUser marks the user as deleted and clears PII fields (email,
	// display_name). Audit references to the userID remain intact.
	SoftDeleteUser(ctx context.Context, userID string) error
}

type AgentApprovalRepository interface {
	SaveAgentApproval(ctx context.Context, approval core.AgentApproval) error
	FindPendingAgentApprovals(ctx context.Context, orgID string, limit, offset int) ([]core.AgentApproval, error)
	FindAgentApprovalByAgentID(ctx context.Context, agentID string) (core.AgentApproval, error)
	UpdateAgentApproval(ctx context.Context, approvalID, decision, reason, reviewedBy string, reviewedAt time.Time) error
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
	RevokeAllTokensForAgent(ctx context.Context, agentID string, revokedAt time.Time) error
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
	ListSentRequests(ctx context.Context, fromAgentID string, limit, offset int) ([]core.Request, error)
	UpdateRequestState(ctx context.Context, requestID string, state core.RequestState, approvalState core.ApprovalState, responseMessage string) (core.Request, bool, error)
}

type ApprovalRepository interface {
	SaveApproval(ctx context.Context, approval core.Approval) (core.Approval, error)
	FindApproval(ctx context.Context, approvalID string) (core.Approval, bool, error)
	ListPendingApprovals(ctx context.Context, agentID string, limit, offset int) ([]core.Approval, error)
	ResolveApproval(ctx context.Context, approvalID string, state core.ApprovalState, resolvedAt time.Time) (core.Approval, bool, error)
}

type EmailVerificationRepository interface {
	SaveEmailVerification(ctx context.Context, v core.EmailVerification) (core.EmailVerification, error)
	FindPendingVerification(ctx context.Context, agentID string) (core.EmailVerification, bool, error)
	MarkEmailVerified(ctx context.Context, verificationID string, verifiedAt time.Time) error
	IncrementVerificationAttempts(ctx context.Context, verificationID string) error
}

// ErrRiskPolicyNotFound is returned when no risk policy matches the given criteria.
var ErrRiskPolicyNotFound = errors.New("risk policy not found")

// ErrActionNotFound is returned when no action matches the given criteria.
var ErrActionNotFound = errors.New("action not found")

// ErrActionInTerminalState is returned when a caller tries to transition an
// action out of a terminal state (executed, failed, cancelled, expired).
// The storage layer enforces the invariant centrally so multiple callers
// race-test consistently.
var ErrActionInTerminalState = errors.New("action is already in a terminal state")

// ActionFilter is a small filter shape for listing actions; zero values
// mean "no constraint".
type ActionFilter struct {
	OwnerUserID string
	State       core.ActionState
	Limit       int
	Offset      int
}

// ActionRepository persists operator-phase actions. SaveAction writes or
// overwrites an action; UpdateActionState enforces the one-way transition
// from pending to a terminal state. Callers should prefer UpdateActionState
// for lifecycle transitions so the terminal-state check is not duplicated.
type ActionRepository interface {
	SaveAction(ctx context.Context, action core.Action) (core.Action, error)
	FindActionByID(ctx context.Context, actionID string) (core.Action, bool, error)
	ListActions(ctx context.Context, filter ActionFilter) ([]core.Action, error)
	UpdateActionState(ctx context.Context, action core.Action) (core.Action, error)
}

// UserPreferencesRepository persists the small set of per-user toggles that
// govern agent behaviour. Each toggle is represented by its own method so
// the wire contract stays explicit.
type UserPreferencesRepository interface {
	SetOperatorEnabled(ctx context.Context, userID string, enabled bool) error
}

// RiskPolicyRepository persists per-org risk policies. SavePolicy writes a
// new version; ActivatePolicy atomically flips the active_at flag, ensuring
// only one policy per org is active at a time. Rollback works by calling
// ActivatePolicy with an older version's ID.
type RiskPolicyRepository interface {
	SavePolicy(ctx context.Context, policy core.RiskPolicy) (core.RiskPolicy, error)
	FindActivePolicyForOrg(ctx context.Context, orgID string) (core.RiskPolicy, bool, error)
	FindPolicyByID(ctx context.Context, policyID string) (core.RiskPolicy, bool, error)
	ListPoliciesForOrg(ctx context.Context, orgID string, limit, offset int) ([]core.RiskPolicy, error)
	ActivatePolicy(ctx context.Context, policyID string, activeAt time.Time) error
	NextPolicyVersionForOrg(ctx context.Context, orgID string) (int, error)
}

// ErrTeamNotFound is returned when no team matches the given ID.
var ErrTeamNotFound = errors.New("team not found")

// ErrTeamMemberNotFound is returned when a (team, user) pair has no membership row.
var ErrTeamMemberNotFound = errors.New("team member not found")

// ErrManagerEdgeCycle is returned by SaveManagerEdge when the proposed edge
// would form a cycle in the reporting graph. Detected lazily at write time
// by walking upward from the proposed manager.
var ErrManagerEdgeCycle = errors.New("manager edge would form a cycle")

// OrgGraphRepository persists teams, team memberships, and the append-only
// manager reporting graph. All methods operate within a single org because
// cross-org edges are not permitted; callers must validate OrgID at the
// service layer.
//
// ManagerEdges are append-only. SaveManagerEdge revokes the prior active
// edge for the same user atomically — the implementation sets RevokedAt on
// any matching row before inserting the new one. Read-side helpers skip
// revoked rows so viewers always see the current chain.
type OrgGraphRepository interface {
	SaveTeam(ctx context.Context, team core.Team) (core.Team, error)
	FindTeamByID(ctx context.Context, teamID string) (core.Team, bool, error)
	ListTeamsForOrg(ctx context.Context, orgID string, limit, offset int) ([]core.Team, error)
	DeleteTeam(ctx context.Context, teamID string) error

	SaveTeamMember(ctx context.Context, member core.TeamMember) error
	DeleteTeamMember(ctx context.Context, teamID, userID string) error
	ListTeamMembers(ctx context.Context, teamID string, limit, offset int) ([]core.TeamMember, error)
	ListTeamsForUser(ctx context.Context, userID string) ([]core.Team, error)
	UsersShareTeam(ctx context.Context, userAID, userBID string) (bool, error)

	SaveManagerEdge(ctx context.Context, edge core.ManagerEdge) (core.ManagerEdge, error)
	RevokeCurrentManagerEdge(ctx context.Context, userID string, revokedAt time.Time) error
	FindCurrentManagerEdge(ctx context.Context, userID string) (core.ManagerEdge, bool, error)
	// WalkManagerChain walks upward from userID up to maxDepth hops. The
	// returned slice starts at userID's direct manager (if any) and ends at
	// either the root (a user with no manager edge) or the depth limit. The
	// walk stops early on a revisit so cycles in legacy data don't loop
	// forever.
	WalkManagerChain(ctx context.Context, userID string, maxDepth int) ([]core.ManagerEdge, error)
}

// AuditFilter groups the parameters accepted by ListAuditEvents.
// Non-empty string fields are combined as AND conditions.
type AuditFilter struct {
	AgentID     string
	Since       time.Time
	EventKind   string
	SubjectType string
	Decision    string
	Limit       int
	Offset      int
}

type AuditRepository interface {
	AppendAuditEvent(ctx context.Context, event core.AuditEvent) (core.AuditEvent, error)
	ListAuditEvents(ctx context.Context, filter AuditFilter) ([]core.AuditEvent, error)
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
	EmailVerificationRepository
	AgentApprovalRepository
	RiskPolicyRepository
	ActionRepository
	UserPreferencesRepository
	OrgGraphRepository
}

// Transactor runs fn inside a single atomic transaction.
// On PostgreSQL, the underlying database transaction is committed on success
// and rolled back on error. On memory, fn is called directly.
type Transactor interface {
	WithTx(ctx context.Context, fn func(tx StoreTx) error) error
}
