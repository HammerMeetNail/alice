package memory

import (
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
	usersByEmail     map[string]string
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
	requestsByAgent  map[string][]string
	approvals        map[string]core.Approval
	approvalsByAgent map[string][]string
	auditEvents      []core.AuditEvent
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
)

func New() *Store {
	return &Store{
		organizations:    make(map[string]core.Organization),
		orgsBySlug:       make(map[string]string),
		users:            make(map[string]core.User),
		usersByEmail:     make(map[string]string),
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
		requestsByAgent:  make(map[string][]string),
		approvals:        make(map[string]core.Approval),
		approvalsByAgent: make(map[string][]string),
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func (s *Store) UpsertOrganization(org core.Organization) (core.Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.organizations[org.OrgID] = org
	s.orgsBySlug[normalizeSlug(org.Slug)] = org.OrgID
	return org, nil
}

func (s *Store) FindOrganizationBySlug(slug string) (core.Organization, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgID, ok := s.orgsBySlug[normalizeSlug(slug)]
	if !ok {
		return core.Organization{}, false, nil
	}
	org, ok := s.organizations[orgID]
	return org, ok, nil
}

func (s *Store) UpsertUser(user core.User) (core.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[user.UserID] = user
	s.usersByEmail[normalizeEmail(user.Email)] = user.UserID
	return user, nil
}

func (s *Store) FindUserByEmail(email string) (core.User, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userID, ok := s.usersByEmail[normalizeEmail(email)]
	if !ok {
		return core.User{}, false, nil
	}
	user, ok := s.users[userID]
	return user, ok, nil
}

func (s *Store) FindUserByID(userID string) (core.User, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[userID]
	return user, ok, nil
}

func (s *Store) UpsertAgent(agent core.Agent) (core.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agents[agent.AgentID] = agent
	s.agentsByUser[agent.OwnerUserID] = agent.AgentID
	return agent, nil
}

func (s *Store) FindAgentByID(agentID string) (core.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, ok := s.agents[agentID]
	return agent, ok, nil
}

func (s *Store) FindAgentByUserID(userID string) (core.Agent, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentID, ok := s.agentsByUser[userID]
	if !ok {
		return core.Agent{}, false, nil
	}
	agent, ok := s.agents[agentID]
	return agent, ok, nil
}

func (s *Store) SaveAgentRegistrationChallenge(challenge core.AgentRegistrationChallenge) (core.AgentRegistrationChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.challenges[challenge.ChallengeID] = challenge
	return challenge, nil
}

func (s *Store) FindAgentRegistrationChallenge(challengeID string) (core.AgentRegistrationChallenge, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	challenge, ok := s.challenges[challengeID]
	return challenge, ok, nil
}

func (s *Store) SaveAgentToken(token core.AgentToken) (core.AgentToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[token.TokenID] = token
	return token, nil
}

func (s *Store) FindAgentTokenByID(tokenID string) (core.AgentToken, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	token, ok := s.tokens[tokenID]
	return token, ok, nil
}

func (s *Store) SaveArtifact(artifact core.Artifact) (core.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.artifacts[artifact.ArtifactID] = artifact
	s.artifactsByUser[artifact.OwnerUserID] = append(s.artifactsByUser[artifact.OwnerUserID], artifact.ArtifactID)
	return artifact, nil
}

func (s *Store) ListArtifactsByOwner(userID string) ([]core.Artifact, error) {
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

func (s *Store) SaveGrant(grant core.PolicyGrant) (core.PolicyGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.grants[grant.PolicyGrantID] = grant
	return grant, nil
}

func (s *Store) ListGrantsForPair(grantorUserID, granteeUserID string) ([]core.PolicyGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	grants := make([]core.PolicyGrant, 0)
	for _, grant := range s.grants {
		if grant.GrantorUserID == grantorUserID && grant.GranteeUserID == granteeUserID {
			grants = append(grants, grant)
		}
	}

	sort.SliceStable(grants, func(i, j int) bool {
		return grants[i].CreatedAt.Before(grants[j].CreatedAt)
	})

	return grants, nil
}

func (s *Store) ListIncomingGrantsForUser(granteeUserID string) ([]core.PolicyGrant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	grants := make([]core.PolicyGrant, 0)
	for _, grant := range s.grants {
		if grant.GranteeUserID == granteeUserID {
			grants = append(grants, grant)
		}
	}

	sort.SliceStable(grants, func(i, j int) bool {
		return grants[i].CreatedAt.Before(grants[j].CreatedAt)
	})

	return grants, nil
}

func (s *Store) SaveQuery(query core.Query) (core.Query, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.queries[query.QueryID] = query
	return query, nil
}

func (s *Store) SaveQueryResponse(response core.QueryResponse) (core.QueryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.responses[response.QueryID] = response
	return response, nil
}

func (s *Store) UpdateQueryState(queryID string, state core.QueryState) (core.Query, bool, error) {
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

func (s *Store) FindQuery(queryID string) (core.Query, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query, ok := s.queries[queryID]
	return query, ok, nil
}

func (s *Store) FindQueryResponse(queryID string) (core.QueryResponse, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response, ok := s.responses[queryID]
	return response, ok, nil
}

func (s *Store) SaveRequest(request core.Request) (core.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.requests[request.RequestID]; ok {
		if existing.ToAgentID != request.ToAgentID {
			s.requestsByAgent[existing.ToAgentID] = removeID(s.requestsByAgent[existing.ToAgentID], request.RequestID)
			s.requestsByAgent[request.ToAgentID] = append(s.requestsByAgent[request.ToAgentID], request.RequestID)
		}
	} else {
		s.requestsByAgent[request.ToAgentID] = append(s.requestsByAgent[request.ToAgentID], request.RequestID)
	}
	s.requests[request.RequestID] = request
	return request, nil
}

func (s *Store) FindRequest(requestID string) (core.Request, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	request, ok := s.requests[requestID]
	return request, ok, nil
}

func (s *Store) ListIncomingRequests(toAgentID string) ([]core.Request, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.requestsByAgent[toAgentID]
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
	return requests, nil
}

func (s *Store) UpdateRequestState(requestID string, state core.RequestState, approvalState core.ApprovalState, responseMessage string) (core.Request, bool, error) {
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

func (s *Store) SaveApproval(approval core.Approval) (core.Approval, error) {
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

func (s *Store) FindApproval(approvalID string) (core.Approval, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	approval, ok := s.approvals[approvalID]
	return approval, ok, nil
}

func (s *Store) ListPendingApprovals(agentID string) ([]core.Approval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.approvalsByAgent[agentID]
	approvals := make([]core.Approval, 0, len(ids))
	for _, approvalID := range ids {
		approval, ok := s.approvals[approvalID]
		if !ok || approval.State != core.ApprovalStatePending {
			continue
		}
		approvals = append(approvals, approval)
	}

	sort.SliceStable(approvals, func(i, j int) bool {
		return approvals[i].CreatedAt.Before(approvals[j].CreatedAt)
	})
	return approvals, nil
}

func (s *Store) ResolveApproval(approvalID string, state core.ApprovalState, resolvedAt time.Time) (core.Approval, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[approvalID]
	if !ok {
		return core.Approval{}, false, nil
	}
	approval.State = state
	approval.ResolvedAt = &resolvedAt
	s.approvals[approvalID] = approval
	return approval, true, nil
}

func (s *Store) AppendAuditEvent(event core.AuditEvent) (core.AuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.auditEvents = append(s.auditEvents, event)
	return event, nil
}

func (s *Store) ListAuditEvents(agentID string, since time.Time) ([]core.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := make([]core.AuditEvent, 0)
	for _, event := range s.auditEvents {
		if !since.IsZero() && event.CreatedAt.Before(since) {
			continue
		}
		if agentID == "" || event.ActorAgentID == agentID || event.TargetAgentID == agentID {
			events = append(events, event)
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})

	return events, nil
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
