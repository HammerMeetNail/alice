package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

type Store struct {
	mu sync.RWMutex

	organizations    map[string]core.Organization
	orgsBySlug       map[string]string
	users            map[string]core.User
	usersByOrgEmail  map[string]string
	agents           map[string]core.Agent
	agentsByUser     map[string]string
	challenges       map[string]core.AgentRegistrationChallenge
	tokens           map[string]core.AgentToken
	artifacts        map[string]core.Artifact
	artifactsByUser  map[string][]string
	grants           map[string]core.PolicyGrant
	queries          map[string]core.Query
	responses        map[string]core.QueryResponse
	requests         map[string]core.Request
	requestsByAgent     map[string][]string
	requestsByFromAgent map[string][]string
	approvals        map[string]core.Approval
	approvalsByAgent map[string][]string
	auditEvents      []core.AuditEvent
	emailVerifications map[string]core.EmailVerification // verificationID → verification
	emailVerifsByAgent map[string]string                // agentID → verificationID (pending)
	agentApprovals   map[string]core.AgentApproval      // approvalID → approval
	agentApprovalsByAgent map[string]string             // agentID → approvalID
	riskPolicies     map[string]core.RiskPolicy         // policyID → policy
	actions          map[string]core.Action             // actionID → action
	actionsByOwner   map[string][]string                // ownerUserID → actionIDs
}

var (
	_ storage.OrganizationRepository               = (*Store)(nil)
	_ storage.UserRepository                       = (*Store)(nil)
	_ storage.AgentRepository                      = (*Store)(nil)
	_ storage.AgentRegistrationChallengeRepository = (*Store)(nil)
	_ storage.AgentTokenRepository                 = (*Store)(nil)
	_ storage.ArtifactRepository                   = (*Store)(nil)
	_ storage.PolicyGrantRepository                = (*Store)(nil)
	_ storage.QueryRepository                      = (*Store)(nil)
	_ storage.RequestRepository                    = (*Store)(nil)
	_ storage.ApprovalRepository                   = (*Store)(nil)
	_ storage.AuditRepository                      = (*Store)(nil)
	_ storage.EmailVerificationRepository          = (*Store)(nil)
	_ storage.AgentApprovalRepository              = (*Store)(nil)
	_ storage.RiskPolicyRepository                 = (*Store)(nil)
	_ storage.ActionRepository                     = (*Store)(nil)
	_ storage.UserPreferencesRepository            = (*Store)(nil)
)

func New() *Store {
	return &Store{
		organizations:    make(map[string]core.Organization),
		orgsBySlug:       make(map[string]string),
		users:            make(map[string]core.User),
		usersByOrgEmail:  make(map[string]string),
		agents:           make(map[string]core.Agent),
		agentsByUser:     make(map[string]string),
		challenges:       make(map[string]core.AgentRegistrationChallenge),
		tokens:           make(map[string]core.AgentToken),
		artifacts:        make(map[string]core.Artifact),
		artifactsByUser:  make(map[string][]string),
		grants:           make(map[string]core.PolicyGrant),
		queries:          make(map[string]core.Query),
		responses:        make(map[string]core.QueryResponse),
		requests:         make(map[string]core.Request),
		requestsByAgent:     make(map[string][]string),
		requestsByFromAgent: make(map[string][]string),
		approvals:        make(map[string]core.Approval),
		approvalsByAgent: make(map[string][]string),
		emailVerifications: make(map[string]core.EmailVerification),
		emailVerifsByAgent: make(map[string]string),
		agentApprovals:       make(map[string]core.AgentApproval),
		agentApprovalsByAgent: make(map[string]string),
		riskPolicies:         make(map[string]core.RiskPolicy),
		actions:              make(map[string]core.Action),
		actionsByOwner:       make(map[string][]string),
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func (s *Store) UpsertOrganization(_ context.Context, org core.Organization) (core.Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.organizations[org.OrgID] = org
	s.orgsBySlug[normalizeSlug(org.Slug)] = org.OrgID
	return org, nil
}

func (s *Store) FindOrganizationBySlug(_ context.Context, slug string) (core.Organization, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgID, ok := s.orgsBySlug[normalizeSlug(slug)]
	if !ok {
		return core.Organization{}, false, nil
	}
	org, ok := s.organizations[orgID]
	return org, ok, nil
}

func (s *Store) FindOrganizationByID(_ context.Context, orgID string) (core.Organization, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	org, ok := s.organizations[orgID]
	return org, ok, nil
}

func (s *Store) FindOrgBySlug(_ context.Context, slug string) (core.Organization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgID, ok := s.orgsBySlug[normalizeSlug(slug)]
	if !ok {
		return core.Organization{}, storage.ErrOrgNotFound
	}
	org, ok := s.organizations[orgID]
	if !ok {
		return core.Organization{}, storage.ErrOrgNotFound
	}
	return org, nil
}

func (s *Store) UpdateOrgVerificationMode(_ context.Context, orgID, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	org, ok := s.organizations[orgID]
	if !ok {
		return storage.ErrOrgNotFound
	}
	org.VerificationMode = mode
	s.organizations[orgID] = org
	return nil
}

func (s *Store) SetOrgInviteTokenHash(_ context.Context, orgID, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	org, ok := s.organizations[orgID]
	if !ok {
		return storage.ErrOrgNotFound
	}
	org.InviteTokenHash = hash
	s.organizations[orgID] = org
	return nil
}

func (s *Store) UpdateGatekeeperTuning(_ context.Context, orgID string, threshold *float64, window *time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	org, ok := s.organizations[orgID]
	if !ok {
		return storage.ErrOrgNotFound
	}
	// Copy the pointers so callers can't mutate stored state by holding onto the input.
	if threshold != nil {
		v := *threshold
		org.GatekeeperConfidenceThreshold = &v
	} else {
		org.GatekeeperConfidenceThreshold = nil
	}
	if window != nil {
		d := *window
		org.GatekeeperLookbackWindow = &d
	} else {
		org.GatekeeperLookbackWindow = nil
	}
	s.organizations[orgID] = org
	return nil
}

func (s *Store) UpsertUser(_ context.Context, user core.User) (core.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[user.UserID] = user
	s.usersByOrgEmail[user.OrgID+":"+normalizeEmail(user.Email)] = user.UserID
	return user, nil
}

func (s *Store) FindUserByEmail(_ context.Context, orgID, email string) (core.User, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userID, ok := s.usersByOrgEmail[orgID+":"+normalizeEmail(email)]
	if !ok {
		return core.User{}, false, nil
	}
	user, ok := s.users[userID]
	return user, ok, nil
}

func (s *Store) FindUserByID(_ context.Context, userID string) (core.User, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[userID]
	return user, ok, nil
}

func (s *Store) UpdateUserRole(_ context.Context, userID, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return fmt.Errorf("user not found")
	}
	user.Role = role
	s.users[userID] = user
	return nil
}

func (s *Store) UpsertAgent(_ context.Context, agent core.Agent) (core.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agents[agent.AgentID] = agent
	s.agentsByUser[agent.OwnerUserID] = agent.AgentID
	return agent, nil
}

func (s *Store) FindAgentByID(_ context.Context, agentID string) (core.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, ok := s.agents[agentID]
	return agent, ok, nil
}

func (s *Store) FindAgentByUserID(_ context.Context, userID string) (core.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentID, ok := s.agentsByUser[userID]
	if !ok {
		return core.Agent{}, false, nil
	}
	agent, ok := s.agents[agentID]
	return agent, ok, nil
}

func (s *Store) SaveAgentRegistrationChallenge(_ context.Context, challenge core.AgentRegistrationChallenge) (core.AgentRegistrationChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Atomic check-and-set: if the caller is marking the challenge as used and
	// it is already marked used (concurrent CompleteRegistration race), reject it.
	if challenge.UsedAt != nil {
		if existing, ok := s.challenges[challenge.ChallengeID]; ok && existing.UsedAt != nil {
			return core.AgentRegistrationChallenge{}, storage.ErrChallengeAlreadyUsed
		}
	}

	s.challenges[challenge.ChallengeID] = challenge
	return challenge, nil
}

func (s *Store) FindAgentRegistrationChallenge(_ context.Context, challengeID string) (core.AgentRegistrationChallenge, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	challenge, ok := s.challenges[challengeID]
	return challenge, ok, nil
}

func (s *Store) SaveAgentToken(_ context.Context, token core.AgentToken) (core.AgentToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[token.TokenID] = token
	return token, nil
}

func (s *Store) FindAgentTokenByID(_ context.Context, tokenID string) (core.AgentToken, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token, ok := s.tokens[tokenID]
	return token, ok, nil
}

func (s *Store) RevokeAllTokensForAgent(_ context.Context, agentID string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for tokenID, token := range s.tokens {
		if token.AgentID == agentID && token.RevokedAt == nil {
			token.RevokedAt = &revokedAt
			s.tokens[tokenID] = token
		}
	}
	return nil
}

func (s *Store) SaveArtifact(_ context.Context, artifact core.Artifact) (core.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.artifacts[artifact.ArtifactID] = artifact
	s.artifactsByUser[artifact.OwnerUserID] = append(s.artifactsByUser[artifact.OwnerUserID], artifact.ArtifactID)
	return artifact, nil
}

func (s *Store) FindArtifactByID(_ context.Context, artifactID string) (core.Artifact, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	artifact, ok := s.artifacts[artifactID]
	return artifact, ok, nil
}

func (s *Store) ListArtifactsByOwner(_ context.Context, userID string) ([]core.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.artifactsByUser[userID]
	artifacts := make([]core.Artifact, 0, len(ids))
	for _, artifactID := range ids {
		artifact, ok := s.artifacts[artifactID]
		if !ok {
			continue
		}
		artifacts = append(artifacts, artifact)
	}

	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt)
	})

	return artifacts, nil
}

func (s *Store) SaveGrant(_ context.Context, grant core.PolicyGrant) (core.PolicyGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.grants[grant.PolicyGrantID] = grant
	return grant, nil
}

func (s *Store) FindGrant(_ context.Context, grantID string) (core.PolicyGrant, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	grant, ok := s.grants[grantID]
	return grant, ok, nil
}

func (s *Store) RevokeGrant(_ context.Context, grantID, grantorUserID string) (core.PolicyGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	grant, ok := s.grants[grantID]
	if !ok || grant.GrantorUserID != grantorUserID || grant.RevokedAt != nil {
		return core.PolicyGrant{}, fmt.Errorf("grant not found or not owned by grantor")
	}
	now := time.Now().UTC()
	grant.RevokedAt = &now
	s.grants[grantID] = grant
	return grant, nil
}

func (s *Store) ListGrantsForPair(_ context.Context, grantorUserID, granteeUserID string) ([]core.PolicyGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	grants := make([]core.PolicyGrant, 0)
	for _, grant := range s.grants {
		if grant.GrantorUserID != grantorUserID || grant.GranteeUserID != granteeUserID {
			continue
		}
		if grant.RevokedAt != nil {
			continue
		}
		if grant.ExpiresAt != nil && now.After(*grant.ExpiresAt) {
			continue
		}
		grants = append(grants, grant)
	}

	sort.SliceStable(grants, func(i, j int) bool {
		return grants[i].CreatedAt.Before(grants[j].CreatedAt)
	})

	return grants, nil
}

func (s *Store) ListIncomingGrantsForUser(_ context.Context, granteeUserID string, limit, offset int) ([]core.PolicyGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	grants := make([]core.PolicyGrant, 0)
	for _, grant := range s.grants {
		if grant.GranteeUserID != granteeUserID || grant.RevokedAt != nil {
			continue
		}
		if grant.ExpiresAt != nil && now.After(*grant.ExpiresAt) {
			continue
		}
		grants = append(grants, grant)
	}

	sort.SliceStable(grants, func(i, j int) bool {
		return grants[i].CreatedAt.Before(grants[j].CreatedAt)
	})

	return pageSlice(grants, limit, offset), nil
}

func (s *Store) SaveQuery(_ context.Context, query core.Query) (core.Query, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.queries[query.QueryID] = query
	return query, nil
}

func (s *Store) SaveQueryResponse(_ context.Context, response core.QueryResponse) (core.QueryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.responses[response.QueryID] = response
	return response, nil
}

func (s *Store) UpdateQueryState(_ context.Context, queryID string, state core.QueryState) (core.Query, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query, ok := s.queries[queryID]
	if !ok {
		return core.Query{}, false, nil
	}
	query.State = state
	s.queries[queryID] = query
	return query, true, nil
}

func (s *Store) FindQuery(_ context.Context, queryID string) (core.Query, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query, ok := s.queries[queryID]
	return query, ok, nil
}

func (s *Store) FindQueryResponse(_ context.Context, queryID string) (core.QueryResponse, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response, ok := s.responses[queryID]
	return response, ok, nil
}

func (s *Store) UpdateQueryResponseApprovalState(_ context.Context, queryID string, state core.ApprovalState) (core.QueryResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	response, ok := s.responses[queryID]
	if !ok {
		return core.QueryResponse{}, false, nil
	}
	response.ApprovalState = state
	s.responses[queryID] = response
	return response, true, nil
}

func (s *Store) SaveRequest(_ context.Context, request core.Request) (core.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.requests[request.RequestID]; ok {
		if existing.ToAgentID != request.ToAgentID {
			s.requestsByAgent[existing.ToAgentID] = removeID(s.requestsByAgent[existing.ToAgentID], request.RequestID)
			s.requestsByAgent[request.ToAgentID] = append(s.requestsByAgent[request.ToAgentID], request.RequestID)
		}
		if existing.FromAgentID != request.FromAgentID {
			s.requestsByFromAgent[existing.FromAgentID] = removeID(s.requestsByFromAgent[existing.FromAgentID], request.RequestID)
			s.requestsByFromAgent[request.FromAgentID] = append(s.requestsByFromAgent[request.FromAgentID], request.RequestID)
		}
	} else {
		s.requestsByAgent[request.ToAgentID] = append(s.requestsByAgent[request.ToAgentID], request.RequestID)
		s.requestsByFromAgent[request.FromAgentID] = append(s.requestsByFromAgent[request.FromAgentID], request.RequestID)
	}
	s.requests[request.RequestID] = request
	return request, nil
}

func (s *Store) ListSentRequests(_ context.Context, fromAgentID string, limit, offset int) ([]core.Request, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.requestsByFromAgent[fromAgentID]
	requests := make([]core.Request, 0, len(ids))
	for _, requestID := range ids {
		request, ok := s.requests[requestID]
		if !ok {
			continue
		}
		requests = append(requests, request)
	}

	sort.SliceStable(requests, func(i, j int) bool {
		return requests[i].CreatedAt.Before(requests[j].CreatedAt)
	})
	return pageSlice(requests, limit, offset), nil
}

func (s *Store) FindRequest(_ context.Context, requestID string) (core.Request, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	request, ok := s.requests[requestID]
	return request, ok, nil
}

func (s *Store) ListIncomingRequests(_ context.Context, toAgentID string, limit, offset int) ([]core.Request, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	ids := s.requestsByAgent[toAgentID]
	requests := make([]core.Request, 0, len(ids))
	for _, requestID := range ids {
		request, ok := s.requests[requestID]
		if !ok {
			continue
		}
		if !request.ExpiresAt.IsZero() && request.ExpiresAt.Before(now) {
			continue
		}
		requests = append(requests, request)
	}

	sort.SliceStable(requests, func(i, j int) bool {
		return requests[i].CreatedAt.Before(requests[j].CreatedAt)
	})
	return pageSlice(requests, limit, offset), nil
}

func (s *Store) UpdateRequestState(_ context.Context, requestID string, state core.RequestState, approvalState core.ApprovalState, responseMessage string) (core.Request, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.requests[requestID]
	if !ok {
		return core.Request{}, false, nil
	}
	request.State = state
	request.ApprovalState = approvalState
	request.ResponseMessage = responseMessage
	s.requests[requestID] = request
	return request, true, nil
}

func (s *Store) SaveApproval(_ context.Context, approval core.Approval) (core.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.approvals[approval.ApprovalID]; ok {
		if existing.AgentID != approval.AgentID {
			s.approvalsByAgent[existing.AgentID] = removeID(s.approvalsByAgent[existing.AgentID], approval.ApprovalID)
			s.approvalsByAgent[approval.AgentID] = append(s.approvalsByAgent[approval.AgentID], approval.ApprovalID)
		}
	} else {
		s.approvalsByAgent[approval.AgentID] = append(s.approvalsByAgent[approval.AgentID], approval.ApprovalID)
	}
	s.approvals[approval.ApprovalID] = approval
	return approval, nil
}

func (s *Store) FindApproval(_ context.Context, approvalID string) (core.Approval, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	approval, ok := s.approvals[approvalID]
	return approval, ok, nil
}

func (s *Store) ListPendingApprovals(_ context.Context, agentID string, limit, offset int) ([]core.Approval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	ids := s.approvalsByAgent[agentID]
	approvals := make([]core.Approval, 0, len(ids))
	for _, approvalID := range ids {
		approval, ok := s.approvals[approvalID]
		if !ok || approval.State != core.ApprovalStatePending {
			continue
		}
		if !approval.ExpiresAt.IsZero() && approval.ExpiresAt.Before(now) {
			continue
		}
		approvals = append(approvals, approval)
	}

	sort.SliceStable(approvals, func(i, j int) bool {
		return approvals[i].CreatedAt.Before(approvals[j].CreatedAt)
	})
	return pageSlice(approvals, limit, offset), nil
}

func (s *Store) ResolveApproval(_ context.Context, approvalID string, state core.ApprovalState, resolvedAt time.Time) (core.Approval, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[approvalID]
	if !ok || approval.State != core.ApprovalStatePending {
		return core.Approval{}, false, nil
	}
	approval.State = state
	approval.ResolvedAt = &resolvedAt
	s.approvals[approvalID] = approval
	return approval, true, nil
}

func (s *Store) AppendAuditEvent(_ context.Context, event core.AuditEvent) (core.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.auditEvents = append(s.auditEvents, event)
	return event, nil
}

func (s *Store) ListAuditEvents(_ context.Context, filter storage.AuditFilter) ([]core.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := make([]core.AuditEvent, 0)
	for _, event := range s.auditEvents {
		if !filter.Since.IsZero() && event.CreatedAt.Before(filter.Since) {
			continue
		}
		if filter.AgentID != "" && event.ActorAgentID != filter.AgentID && event.TargetAgentID != filter.AgentID {
			continue
		}
		if filter.EventKind != "" && event.EventKind != filter.EventKind {
			continue
		}
		if filter.SubjectType != "" && event.SubjectType != filter.SubjectType {
			continue
		}
		if filter.Decision != "" && event.Decision != filter.Decision {
			continue
		}
		events = append(events, event)
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})

	return pageSlice(events, filter.Limit, filter.Offset), nil
}

// --- EmailVerificationRepository ---

func (s *Store) SaveEmailVerification(_ context.Context, v core.EmailVerification) (core.EmailVerification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove any prior pending verification for the same agent.
	if oldID, ok := s.emailVerifsByAgent[v.AgentID]; ok {
		delete(s.emailVerifications, oldID)
	}

	s.emailVerifications[v.VerificationID] = v
	if v.VerifiedAt == nil {
		s.emailVerifsByAgent[v.AgentID] = v.VerificationID
	}
	return v, nil
}

func (s *Store) FindPendingVerification(_ context.Context, agentID string) (core.EmailVerification, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.emailVerifsByAgent[agentID]
	if !ok {
		return core.EmailVerification{}, false, nil
	}
	v, ok := s.emailVerifications[id]
	if !ok || v.VerifiedAt != nil {
		return core.EmailVerification{}, false, nil
	}
	return v, true, nil
}

func (s *Store) MarkEmailVerified(_ context.Context, verificationID string, verifiedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.emailVerifications[verificationID]
	if !ok {
		return storage.ErrVerificationNotFound
	}
	v.VerifiedAt = &verifiedAt
	s.emailVerifications[verificationID] = v
	delete(s.emailVerifsByAgent, v.AgentID)
	return nil
}

func (s *Store) IncrementVerificationAttempts(_ context.Context, verificationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.emailVerifications[verificationID]
	if !ok {
		return storage.ErrVerificationNotFound
	}
	v.Attempts++
	s.emailVerifications[verificationID] = v
	return nil
}

// --- AgentApprovalRepository ---

func (s *Store) SaveAgentApproval(_ context.Context, approval core.AgentApproval) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agentApprovals[approval.ApprovalID] = approval
	s.agentApprovalsByAgent[approval.AgentID] = approval.ApprovalID
	return nil
}

func (s *Store) FindPendingAgentApprovals(_ context.Context, orgID string, limit, offset int) ([]core.AgentApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	approvals := make([]core.AgentApproval, 0)
	for _, approval := range s.agentApprovals {
		if approval.OrgID != orgID || approval.Decision != "" {
			continue
		}
		approvals = append(approvals, approval)
	}

	sort.SliceStable(approvals, func(i, j int) bool {
		return approvals[i].RequestedAt.Before(approvals[j].RequestedAt)
	})

	return pageSlice(approvals, limit, offset), nil
}

func (s *Store) FindAgentApprovalByAgentID(_ context.Context, agentID string) (core.AgentApproval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	approvalID, ok := s.agentApprovalsByAgent[agentID]
	if !ok {
		return core.AgentApproval{}, storage.ErrAgentApprovalNotFound
	}
	approval, ok := s.agentApprovals[approvalID]
	if !ok {
		return core.AgentApproval{}, storage.ErrAgentApprovalNotFound
	}
	return approval, nil
}

func (s *Store) UpdateAgentApproval(_ context.Context, approvalID, decision, reason, reviewedBy string, reviewedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.agentApprovals[approvalID]
	if !ok {
		return storage.ErrAgentApprovalNotFound
	}
	approval.Decision = decision
	approval.Reason = reason
	approval.ReviewedBy = reviewedBy
	approval.ReviewedAt = &reviewedAt
	s.agentApprovals[approvalID] = approval
	return nil
}

// --- Risk policies ---

func (s *Store) SavePolicy(_ context.Context, policy core.RiskPolicy) (core.RiskPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.riskPolicies[policy.PolicyID] = policy
	return policy, nil
}

func (s *Store) FindActivePolicyForOrg(_ context.Context, orgID string) (core.RiskPolicy, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		active core.RiskPolicy
		found  bool
	)
	for _, policy := range s.riskPolicies {
		if policy.OrgID != orgID {
			continue
		}
		if policy.ActiveAt == nil {
			continue
		}
		if !found || policy.ActiveAt.After(*active.ActiveAt) {
			active = policy
			found = true
		}
	}
	return active, found, nil
}

func (s *Store) FindPolicyByID(_ context.Context, policyID string) (core.RiskPolicy, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	policy, ok := s.riskPolicies[policyID]
	return policy, ok, nil
}

func (s *Store) ListPoliciesForOrg(_ context.Context, orgID string, limit, offset int) ([]core.RiskPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]core.RiskPolicy, 0)
	for _, policy := range s.riskPolicies {
		if policy.OrgID == orgID {
			out = append(out, policy)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Newest first: higher version => earlier in the list.
		return out[i].Version > out[j].Version
	})
	return pageSlice(out, limit, offset), nil
}

func (s *Store) ActivatePolicy(_ context.Context, policyID string, activeAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	target, ok := s.riskPolicies[policyID]
	if !ok {
		return storage.ErrRiskPolicyNotFound
	}

	// Deactivate any other active policy in the same org; then activate
	// this one. Callers rely on at-most-one-active invariant.
	for id, policy := range s.riskPolicies {
		if policy.OrgID != target.OrgID {
			continue
		}
		if policy.ActiveAt == nil {
			continue
		}
		policy.ActiveAt = nil
		s.riskPolicies[id] = policy
	}
	target.ActiveAt = &activeAt
	s.riskPolicies[policyID] = target
	return nil
}

func (s *Store) NextPolicyVersionForOrg(_ context.Context, orgID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	max := 0
	for _, policy := range s.riskPolicies {
		if policy.OrgID != orgID {
			continue
		}
		if policy.Version > max {
			max = policy.Version
		}
	}
	return max + 1, nil
}

// --- Actions ---

func (s *Store) SaveAction(_ context.Context, action core.Action) (core.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, existing := s.actions[action.ActionID]; !existing {
		s.actionsByOwner[action.OwnerUserID] = append(s.actionsByOwner[action.OwnerUserID], action.ActionID)
	}
	s.actions[action.ActionID] = action
	return action, nil
}

func (s *Store) FindActionByID(_ context.Context, actionID string) (core.Action, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	action, ok := s.actions[actionID]
	return action, ok, nil
}

func (s *Store) ListActions(_ context.Context, filter storage.ActionFilter) ([]core.Action, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var candidates []core.Action
	if filter.OwnerUserID != "" {
		for _, id := range s.actionsByOwner[filter.OwnerUserID] {
			if a, ok := s.actions[id]; ok {
				candidates = append(candidates, a)
			}
		}
	} else {
		for _, a := range s.actions {
			candidates = append(candidates, a)
		}
	}

	out := candidates[:0]
	for _, a := range candidates {
		if filter.State != "" && a.State != filter.State {
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageSlice(out, filter.Limit, filter.Offset), nil
}

func (s *Store) UpdateActionState(_ context.Context, action core.Action) (core.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.actions[action.ActionID]
	if !ok {
		return core.Action{}, storage.ErrActionNotFound
	}
	if isTerminalActionState(existing.State) {
		return core.Action{}, storage.ErrActionInTerminalState
	}
	s.actions[action.ActionID] = action
	return action, nil
}

func isTerminalActionState(state core.ActionState) bool {
	switch state {
	case core.ActionStateExecuted, core.ActionStateFailed, core.ActionStateCancelled, core.ActionStateExpired:
		return true
	default:
		return false
	}
}

// --- User preferences ---

func (s *Store) SetOperatorEnabled(_ context.Context, userID string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return fmt.Errorf("user not found")
	}
	user.OperatorEnabled = enabled
	s.users[userID] = user
	return nil
}

// WithTx satisfies storage.Transactor. The memory store's mutex-based
// serialisation is sufficient; no real transaction is needed.
func (s *Store) WithTx(_ context.Context, fn func(tx storage.StoreTx) error) error {
	return fn(s)
}

// pageSlice applies offset-based pagination to a slice.
// limit ≤ 0 means no cap; offset ≤ 0 means start from the beginning.
func pageSlice[T any](items []T, limit, offset int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return items[:0]
	}
	items = items[offset:]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func removeID(values []string, target string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value == target {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}
