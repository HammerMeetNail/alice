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

var (
	_ storage.OrganizationRepository = (*Store)(nil)
	_ storage.UserRepository         = (*Store)(nil)
	_ storage.AgentRepository        = (*Store)(nil)
	_ storage.ArtifactRepository     = (*Store)(nil)
	_ storage.PolicyGrantRepository  = (*Store)(nil)
	_ storage.QueryRepository        = (*Store)(nil)
	_ storage.AuditRepository        = (*Store)(nil)
)

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
