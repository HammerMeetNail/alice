package agents

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

type Service struct {
	orgs       storage.OrganizationRepository
	users      storage.UserRepository
	agents     storage.AgentRepository
	challenges storage.AgentRegistrationChallengeRepository
	tokens     storage.AgentTokenRepository
	cfg        config.Config
}

func NewService(
	orgs storage.OrganizationRepository,
	users storage.UserRepository,
	agents storage.AgentRepository,
	challenges storage.AgentRegistrationChallengeRepository,
	tokens storage.AgentTokenRepository,
	cfg config.Config,
) *Service {
	return &Service{
		orgs:       orgs,
		users:      users,
		agents:     agents,
		challenges: challenges,
		tokens:     tokens,
		cfg:        cfg,
	}
}

func (s *Service) BeginRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey string, capabilities []string) (core.AgentRegistrationChallenge, string, error) {
	if err := core.ValidateAgentRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey); err != nil {
		return core.AgentRegistrationChallenge{}, "", err
	}
	decodedPublicKey, err := decodeBase64Key(publicKey)
	if err != nil || len(decodedPublicKey) != ed25519.PublicKeySize {
		return core.AgentRegistrationChallenge{}, "", core.ValidationError{Message: "public_key must be a base64-encoded ed25519 public key"}
	}

	now := time.Now().UTC()
	nonce, err := randomToken(32)
	if err != nil {
		return core.AgentRegistrationChallenge{}, "", fmt.Errorf("generate registration challenge nonce: %w", err)
	}

	challenge := core.AgentRegistrationChallenge{
		ChallengeID:  id.New("regch"),
		OrgSlug:      strings.ToLower(strings.TrimSpace(orgSlug)),
		OwnerEmail:   strings.ToLower(strings.TrimSpace(ownerEmail)),
		AgentName:    strings.TrimSpace(agentName),
		ClientType:   strings.TrimSpace(clientType),
		PublicKey:    strings.TrimSpace(publicKey),
		Capabilities: capabilities,
		Nonce:        nonce,
		CreatedAt:    now,
		ExpiresAt:    now.Add(s.cfg.AuthChallengeTTL),
	}

	challenge, err = s.challenges.SaveAgentRegistrationChallenge(challenge)
	if err != nil {
		return core.AgentRegistrationChallenge{}, "", fmt.Errorf("save registration challenge: %w", err)
	}

	return challenge, formatRegistrationChallenge(challenge), nil
}

func (s *Service) CompleteRegistration(challengeID, challengeSignature string) (core.Organization, core.User, core.Agent, string, time.Time, error) {
	if err := core.ValidateRegistrationCompletion(challengeID, challengeSignature); err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, err
	}

	challenge, ok, err := s.challenges.FindAgentRegistrationChallenge(strings.TrimSpace(challengeID))
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, fmt.Errorf("find registration challenge: %w", err)
	}
	if !ok {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, ErrUnknownRegistrationChallenge
	}
	if challenge.UsedAt != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, ErrUsedRegistrationChallenge
	}

	now := time.Now().UTC()
	if now.After(challenge.ExpiresAt) {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, ErrExpiredRegistrationChallenge
	}
	if err := verifyRegistrationSignature(challenge, challengeSignature); err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, err
	}

	challenge.UsedAt = &now
	if _, err := s.challenges.SaveAgentRegistrationChallenge(challenge); err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, fmt.Errorf("mark registration challenge used: %w", err)
	}

	org, user, agent, err := s.upsertRegisteredAgent(challenge.OrgSlug, challenge.OwnerEmail, challenge.AgentName, challenge.ClientType, challenge.PublicKey, challenge.Capabilities, now)
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, err
	}

	token, rawToken, err := s.issueToken(agent.AgentID, now)
	if err != nil {
		return core.Organization{}, core.User{}, core.Agent{}, "", time.Time{}, err
	}

	return org, user, agent, rawToken, token.ExpiresAt, nil
}

func (s *Service) upsertRegisteredAgent(orgSlug, ownerEmail, agentName, clientType, publicKey string, capabilities []string, now time.Time) (core.Organization, core.User, core.Agent, error) {
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

func (s *Service) AuthenticateAgent(accessToken string) (core.Agent, core.User, error) {
	tokenID, ok := splitToken(accessToken)
	if !ok {
		return core.Agent{}, core.User{}, ErrInvalidAgentToken
	}

	token, found, err := s.tokens.FindAgentTokenByID(tokenID)
	if err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("find agent token: %w", err)
	}
	if !found {
		return core.Agent{}, core.User{}, ErrUnknownAgentToken
	}

	now := time.Now().UTC()
	switch {
	case token.RevokedAt != nil:
		return core.Agent{}, core.User{}, ErrRevokedAgentToken
	case now.After(token.ExpiresAt):
		return core.Agent{}, core.User{}, ErrExpiredAgentToken
	case subtle.ConstantTimeCompare([]byte(token.TokenHash), []byte(hashToken(accessToken))) != 1:
		return core.Agent{}, core.User{}, ErrInvalidAgentToken
	}

	token.LastUsedAt = now
	if _, err := s.tokens.SaveAgentToken(token); err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("update token last used at: %w", err)
	}

	return s.RequireAgent(token.AgentID)
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

func (s *Service) issueToken(agentID string, now time.Time) (core.AgentToken, string, error) {
	secret, err := randomToken(32)
	if err != nil {
		return core.AgentToken{}, "", fmt.Errorf("generate token secret: %w", err)
	}

	tokenID := id.New("atok")
	rawToken := tokenID + "~" + secret
	token := core.AgentToken{
		TokenID:    tokenID,
		AgentID:    agentID,
		TokenHash:  hashToken(rawToken),
		IssuedAt:   now,
		ExpiresAt:  now.Add(s.cfg.AuthTokenTTL),
		LastUsedAt: now,
	}

	token, err = s.tokens.SaveAgentToken(token)
	if err != nil {
		return core.AgentToken{}, "", fmt.Errorf("save agent token: %w", err)
	}

	return token, rawToken, nil
}

func formatRegistrationChallenge(challenge core.AgentRegistrationChallenge) string {
	return fmt.Sprintf("alice/register:%s:%s:%d", challenge.ChallengeID, challenge.Nonce, challenge.ExpiresAt.Unix())
}

func verifyRegistrationSignature(challenge core.AgentRegistrationChallenge, encodedSignature string) error {
	publicKey, err := decodeBase64Key(challenge.PublicKey)
	if err != nil {
		return core.ValidationError{Message: "stored public_key is invalid"}
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return core.ValidationError{Message: "stored public_key is invalid"}
	}

	signature, err := decodeBase64Key(encodedSignature)
	if err != nil {
		return ErrInvalidRegistrationSignature
	}
	if len(signature) != ed25519.SignatureSize {
		return ErrInvalidRegistrationSignature
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(formatRegistrationChallenge(challenge)), signature) {
		return ErrInvalidRegistrationSignature
	}
	return nil
}

func splitToken(value string) (string, bool) {
	tokenID, secret, ok := strings.Cut(strings.TrimSpace(value), "~")
	return tokenID, ok && tokenID != "" && secret != ""
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func decodeBase64Key(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(trimmed)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("decode base64 value")
}
