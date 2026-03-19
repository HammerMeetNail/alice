package agents

import (
	"fmt"
	"strings"
	"time"

	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

type Service struct {
	orgs   storage.OrganizationRepository
	users  storage.UserRepository
	agents storage.AgentRepository
	cfg    config.Config
}

func NewService(orgs storage.OrganizationRepository, users storage.UserRepository, agents storage.AgentRepository, cfg config.Config) *Service {
	return &Service{
		orgs:   orgs,
		users:  users,
		agents: agents,
		cfg:    cfg,
	}
}

func (s *Service) RegisterAgent(orgSlug, ownerEmail, agentName, clientType, publicKey string, capabilities []string) (core.Organization, core.User, core.Agent, error) {
	if err := core.ValidateAgentRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey); err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, err
	}

	now := time.Now().UTC()
	org, ok, err := s.orgs.FindOrganizationBySlug(orgSlug)
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, fmt.Errorf("find organization by slug: %w", err)
	}
	if !ok {
		org = core.Organization{
			OrgID:     id.New("org"),
			Name:      s.cfg.DefaultOrgName,
			Slug:      strings.ToLower(strings.TrimSpace(orgSlug)),
			CreatedAt: now,
			Status:    "active",
		}
		org, err = s.orgs.UpsertOrganization(org)
		if err != nil {
			return core.Organization{}, core.User{}, core.Agent{}, fmt.Errorf("upsert organization: %w", err)
		}
	}

	user, ok, err := s.users.FindUserByEmail(ownerEmail)
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, fmt.Errorf("find user by email: %w", err)
	}
	if !ok {
		user = core.User{
			UserID:      id.New("user"),
			OrgID:       org.OrgID,
			Email:       strings.ToLower(strings.TrimSpace(ownerEmail)),
			DisplayName: ownerEmail,
			CreatedAt:   now,
			Status:      "active",
		}
		user, err = s.users.UpsertUser(user)
		if err != nil {
			return core.Organization{}, core.User{}, core.Agent{}, fmt.Errorf("upsert user: %w", err)
		}
	}

	agent, ok, err := s.agents.FindAgentByUserID(user.UserID)
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, fmt.Errorf("find agent by user id: %w", err)
	}
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

	agent, err = s.agents.UpsertAgent(agent)
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, fmt.Errorf("upsert agent: %w", err)
	}
	return org, user, agent, nil
}

func (s *Service) RequireAgent(agentID string) (core.Agent, core.User, error) {
	agent, ok, err := s.agents.FindAgentByID(agentID)
	if err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("find agent by id: %w", err)
	}
	if !ok {
		return core.Agent{}, core.User{}, ErrUnknownAgent
	}
	user, ok, err := s.users.FindUserByID(agent.OwnerUserID)
	if err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("find user by id: %w", err)
	}
	if !ok {
		return core.Agent{}, core.User{}, ErrUnknownAgentOwner
	}
	return agent, user, nil
}

func (s *Service) FindUserByEmail(email string) (core.User, bool, error) {
	return s.users.FindUserByEmail(email)
}

func (s *Service) FindUserByID(userID string) (core.User, bool, error) {
	return s.users.FindUserByID(userID)
}

func (s *Service) FindAgentByUserID(userID string) (core.Agent, bool, error) {
	return s.agents.FindAgentByUserID(userID)
}
