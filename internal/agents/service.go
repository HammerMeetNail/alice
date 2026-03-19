package agents

import (
	"strings"
	"time"

	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

type Service struct {
	store *memory.Store
	cfg   config.Config
}

func NewService(store *memory.Store, cfg config.Config) *Service {
	return &Service{
		store: store,
		cfg:   cfg,
	}
}

func (s *Service) RegisterAgent(orgSlug, ownerEmail, agentName, clientType, publicKey string, capabilities []string) (core.Organization, core.User, core.Agent, error) {
	if err := core.ValidateAgentRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey); err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, err
	}

	now := time.Now().UTC()
	org, ok := s.store.FindOrganizationBySlug(orgSlug)
	if !ok {
		org = core.Organization{
			OrgID:     id.New("org"),
			Name:      s.cfg.DefaultOrgName,
			Slug:      strings.ToLower(strings.TrimSpace(orgSlug)),
			CreatedAt: now,
			Status:    "active",
		}
		s.store.UpsertOrganization(org)
	}

	user, ok := s.store.FindUserByEmail(ownerEmail)
	if !ok {
		user = core.User{
			UserID:      id.New("user"),
			OrgID:       org.OrgID,
			Email:       strings.ToLower(strings.TrimSpace(ownerEmail)),
			DisplayName: ownerEmail,
			CreatedAt:   now,
			Status:      "active",
		}
		s.store.UpsertUser(user)
	}

	agent, ok := s.store.FindAgentByUserID(user.UserID)
	if ok {
		agent.AgentName = agentName
		agent.ClientType = clientType
		agent.PublicKey = publicKey
		agent.Capabilities = capabilities
		agent.LastSeenAt = now
	} else {
		agent = core.Agent{
			AgentID:      id.New("agent"),
			OrgID:        org.OrgID,
			OwnerUserID:  user.UserID,
			AgentName:    agentName,
			RuntimeKind:  "edge",
			ClientType:   clientType,
			PublicKey:    publicKey,
			Capabilities: capabilities,
			Status:       "active",
			LastSeenAt:   now,
		}
	}

	s.store.UpsertAgent(agent)
	return org, user, agent, nil
}

func (s *Service) RequireAgent(agentID string) (core.Agent, core.User, error) {
	agent, ok := s.store.FindAgentByID(agentID)
	if !ok {
		return core.Agent{}, core.User{}, ErrUnknownAgent
	}
	user, ok := s.store.FindUserByID(agent.OwnerUserID)
	if !ok {
		return core.Agent{}, core.User{}, ErrUnknownAgentOwner
	}
	return agent, user, nil
}

func (s *Service) FindUserByEmail(email string) (core.User, bool) {
	return s.store.FindUserByEmail(email)
}

func (s *Service) FindUserByID(userID string) (core.User, bool) {
	return s.store.FindUserByID(userID)
}

func (s *Service) FindAgentByUserID(userID string) (core.Agent, bool) {
	return s.store.FindAgentByUserID(userID)
}
