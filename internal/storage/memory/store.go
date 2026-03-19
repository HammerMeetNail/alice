package memory

import (
	"sort"
	"strings"
	"sync"
	"time"

	"alice/internal/core"
)

type Store struct {
	mu sync.RWMutex

	organizations   map[string]core.Organization
	orgsBySlug      map[string]string
	users           map[string]core.User
	usersByEmail    map[string]string
	agents          map[string]core.Agent
	agentsByUser    map[string]string
	artifacts       map[string]core.Artifact
	artifactsByUser map[string][]string
	grants          map[string]core.PolicyGrant
	queries         map[string]core.Query
	responses       map[string]core.QueryResponse
	auditEvents     []core.AuditEvent
}

func New() *Store {
	return &Store{
		organizations:   make(map[string]core.Organization),
		orgsBySlug:      make(map[string]string),
		users:           make(map[string]core.User),
		usersByEmail:    make(map[string]string),
		agents:          make(map[string]core.Agent),
		agentsByUser:    make(map[string]string),
		artifacts:       make(map[string]core.Artifact),
		artifactsByUser: make(map[string][]string),
		grants:          make(map[string]core.PolicyGrant),
		queries:         make(map[string]core.Query),
		responses:       make(map[string]core.QueryResponse),
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func (s *Store) UpsertOrganization(org core.Organization) core.Organization {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.organizations[org.OrgID] = org
	s.orgsBySlug[normalizeSlug(org.Slug)] = org.OrgID
	return org
}

func (s *Store) FindOrganizationBySlug(slug string) (core.Organization, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgID, ok := s.orgsBySlug[normalizeSlug(slug)]
	if !ok {
		return core.Organization{}, false
	}
	org, ok := s.organizations[orgID]
	return org, ok
}

func (s *Store) UpsertUser(user core.User) core.User {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[user.UserID] = user
	s.usersByEmail[normalizeEmail(user.Email)] = user.UserID
	return user
}

func (s *Store) FindUserByEmail(email string) (core.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userID, ok := s.usersByEmail[normalizeEmail(email)]
	if !ok {
		return core.User{}, false
	}
	user, ok := s.users[userID]
	return user, ok
}

func (s *Store) FindUserByID(userID string) (core.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[userID]
	return user, ok
}

func (s *Store) UpsertAgent(agent core.Agent) core.Agent {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agents[agent.AgentID] = agent
	s.agentsByUser[agent.OwnerUserID] = agent.AgentID
	return agent
}

func (s *Store) FindAgentByID(agentID string) (core.Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, ok := s.agents[agentID]
	return agent, ok
}

func (s *Store) FindAgentByUserID(userID string) (core.Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentID, ok := s.agentsByUser[userID]
	if !ok {
		return core.Agent{}, false
	}
	agent, ok := s.agents[agentID]
	return agent, ok
}

func (s *Store) SaveArtifact(artifact core.Artifact) core.Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.artifacts[artifact.ArtifactID] = artifact
	s.artifactsByUser[artifact.OwnerUserID] = append(s.artifactsByUser[artifact.OwnerUserID], artifact.ArtifactID)
	return artifact
}

func (s *Store) ListArtifactsByOwner(userID string) []core.Artifact {
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

	return artifacts
}

func (s *Store) SaveGrant(grant core.PolicyGrant) core.PolicyGrant {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.grants[grant.PolicyGrantID] = grant
	return grant
}

func (s *Store) ListGrantsForPair(grantorUserID, granteeUserID string) []core.PolicyGrant {
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

	return grants
}

func (s *Store) ListIncomingGrantsForUser(granteeUserID string) []core.PolicyGrant {
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

	return grants
}

func (s *Store) SaveQuery(query core.Query) core.Query {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.queries[query.QueryID] = query
	return query
}

func (s *Store) SaveQueryResponse(response core.QueryResponse) core.QueryResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.responses[response.QueryID] = response
	return response
}

func (s *Store) UpdateQueryState(queryID string, state core.QueryState) (core.Query, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query, ok := s.queries[queryID]
	if !ok {
		return core.Query{}, false
	}
	query.State = state
	s.queries[queryID] = query
	return query, true
}

func (s *Store) FindQuery(queryID string) (core.Query, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query, ok := s.queries[queryID]
	return query, ok
}

func (s *Store) FindQueryResponse(queryID string) (core.QueryResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	response, ok := s.responses[queryID]
	return response, ok
}

func (s *Store) AppendAuditEvent(event core.AuditEvent) core.AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.auditEvents = append(s.auditEvents, event)
	return event
}

func (s *Store) ListAuditEvents(agentID string, since time.Time) []core.AuditEvent {
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

	return events
}
