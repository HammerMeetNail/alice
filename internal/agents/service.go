package agents

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"alice/internal/config"
	"alice/internal/core"
	"alice/internal/email"
	"alice/internal/id"
	"alice/internal/storage"
)

type Service struct {
	orgs          storage.OrganizationRepository
	users         storage.UserRepository
	agents        storage.AgentRepository
	challenges    storage.AgentRegistrationChallengeRepository
	tokens        storage.AgentTokenRepository
	verifications storage.EmailVerificationRepository
	approvals     storage.AgentApprovalRepository
	cfg           config.Config
	tx            storage.Transactor
	emailSender   email.Sender // nil means OTP flow disabled
}

func NewService(
	orgs storage.OrganizationRepository,
	users storage.UserRepository,
	agents storage.AgentRepository,
	challenges storage.AgentRegistrationChallengeRepository,
	tokens storage.AgentTokenRepository,
	cfg config.Config,
	tx storage.Transactor,
) *Service {
	return &Service{
		orgs:       orgs,
		users:      users,
		agents:     agents,
		challenges: challenges,
		tokens:     tokens,
		cfg:        cfg,
		tx:         tx,
	}
}

// WithApprovalRepository attaches an agent approval repository to the service.
func (s *Service) WithApprovalRepository(approvals storage.AgentApprovalRepository) *Service {
	s.approvals = approvals
	return s
}

// WithEmailSender attaches an email sender to the service. When sender is non-nil,
// CompleteRegistration will trigger OTP email verification.
func (s *Service) WithEmailSender(sender email.Sender, verifications storage.EmailVerificationRepository) *Service {
	s.emailSender = sender
	s.verifications = verifications
	return s
}

// BeginRegistrationResult carries the challenge plus optional data visible only at creation time.
type BeginRegistrationResult struct {
	Challenge core.AgentRegistrationChallenge
	Payload   string
}

func (s *Service) BeginRegistration(ctx context.Context, orgSlug, ownerEmail, agentName, clientType, publicKey, inviteToken string) (BeginRegistrationResult, error) {
	if err := core.ValidateAgentRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey); err != nil {
		return BeginRegistrationResult{}, err
	}
	decodedPublicKey, err := decodeBase64Key(publicKey)
	if err != nil || len(decodedPublicKey) != ed25519.PublicKeySize {
		return BeginRegistrationResult{}, core.ValidationError{Message: "public_key must be a base64-encoded ed25519 public key"}
	}

	// Check whether the org already exists.
	normalizedSlug := strings.ToLower(strings.TrimSpace(orgSlug))

	existingOrg, orgErr := s.orgs.FindOrgBySlug(ctx, normalizedSlug)
	if orgErr != nil && !errors.Is(orgErr, storage.ErrOrgNotFound) {
		return BeginRegistrationResult{}, fmt.Errorf("find org by slug: %w", orgErr)
	}

	if orgErr == nil {
		// Org exists — enforce invite token check if required.
		if core.OrgRequiresInviteToken(existingOrg.VerificationMode) {
			if strings.TrimSpace(inviteToken) == "" {
				return BeginRegistrationResult{}, ErrInviteTokenRequired
			}
			tokenHash := hashToken(inviteToken)
			if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(existingOrg.InviteTokenHash)) != 1 {
				return BeginRegistrationResult{}, ErrInvalidInviteToken
			}
		}
	}
	// Org does not exist: first registration creates it; invite token is generated at CompleteRegistration.

	now := time.Now().UTC()
	nonce, err := randomToken(32)
	if err != nil {
		return BeginRegistrationResult{}, fmt.Errorf("generate registration challenge nonce: %w", err)
	}

	challenge := core.AgentRegistrationChallenge{
		ChallengeID:  id.New("regch"),
		OrgSlug:      normalizedSlug,
		OwnerEmail:   strings.ToLower(strings.TrimSpace(ownerEmail)),
		AgentName:    strings.TrimSpace(agentName),
		ClientType:   strings.TrimSpace(clientType),
		PublicKey:    strings.TrimSpace(publicKey),
		Nonce:        nonce,
		CreatedAt:    now,
		ExpiresAt:    now.Add(s.cfg.AuthChallengeTTL),
	}

	challenge, err = s.challenges.SaveAgentRegistrationChallenge(ctx, challenge)
	if err != nil {
		return BeginRegistrationResult{}, fmt.Errorf("save registration challenge: %w", err)
	}

	return BeginRegistrationResult{
		Challenge: challenge,
		Payload:   formatRegistrationChallenge(challenge),
	}, nil
}

// UpdateVerificationMode changes the org's verification mode. The caller must be an org admin.
func (s *Service) UpdateVerificationMode(ctx context.Context, agent core.Agent, mode string) (core.Organization, error) {
	if err := core.ValidateVerificationMode(mode); err != nil {
		return core.Organization{}, err
	}

	user, ok, err := s.users.FindUserByID(ctx, agent.OwnerUserID)
	if err != nil {
		return core.Organization{}, fmt.Errorf("find owner user: %w", err)
	}
	if !ok {
		return core.Organization{}, ErrUnknownAgentOwner
	}
	if user.Role != core.UserRoleAdmin {
		return core.Organization{}, ErrNotOrgAdmin
	}

	if err := s.orgs.UpdateOrgVerificationMode(ctx, agent.OrgID, mode); err != nil {
		return core.Organization{}, fmt.Errorf("update org verification mode: %w", err)
	}

	org, ok, err := s.orgs.FindOrganizationByID(ctx, agent.OrgID)
	if err != nil {
		return core.Organization{}, fmt.Errorf("find org after update: %w", err)
	}
	if !ok {
		return core.Organization{}, fmt.Errorf("org not found after update")
	}

	slog.Info("org verification mode updated", "org_id", agent.OrgID, "mode", mode)
	return org, nil
}

// RotateInviteToken generates a new invite token for an org, replacing the previous one.
// The caller's agent must belong to the org.
func (s *Service) RotateInviteToken(ctx context.Context, orgID, callerAgentID string) (string, error) {
	callerAgent, ok, err := s.agents.FindAgentByID(ctx, callerAgentID)
	if err != nil {
		return "", fmt.Errorf("find caller agent: %w", err)
	}
	if !ok {
		return "", ErrUnknownAgent
	}
	if callerAgent.OrgID != orgID {
		return "", core.ForbiddenError{Message: "caller does not belong to this org"}
	}

	rawToken, err := randomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	tokenHash := hashToken(rawToken)

	if err := s.orgs.SetOrgInviteTokenHash(ctx, orgID, tokenHash); err != nil {
		return "", fmt.Errorf("set org invite token hash: %w", err)
	}

	slog.Info("org invite token rotated", "org_id", orgID, "caller_agent_id", callerAgentID)
	return rawToken, nil
}

// CompleteRegistrationResult holds all the outputs of CompleteRegistration.
type CompleteRegistrationResult struct {
	Org              core.Organization
	User             core.User
	Agent            core.Agent
	AccessToken      string
	TokenExpiresAt   time.Time
	FirstInviteToken string // non-empty only on first registration in the org
}

func (s *Service) CompleteRegistration(ctx context.Context, challengeID, challengeSignature string) (CompleteRegistrationResult, error) {
	if err := core.ValidateRegistrationCompletion(challengeID, challengeSignature); err != nil {
		return CompleteRegistrationResult{}, err
	}

	challenge, ok, err := s.challenges.FindAgentRegistrationChallenge(ctx, strings.TrimSpace(challengeID))
	if err != nil {
		return CompleteRegistrationResult{}, fmt.Errorf("find registration challenge: %w", err)
	}
	if !ok {
		return CompleteRegistrationResult{}, ErrUnknownRegistrationChallenge
	}
	if challenge.UsedAt != nil {
		return CompleteRegistrationResult{}, ErrUsedRegistrationChallenge
	}

	now := time.Now().UTC()
	if now.After(challenge.ExpiresAt) {
		return CompleteRegistrationResult{}, ErrExpiredRegistrationChallenge
	}
	if err := verifyRegistrationSignature(challenge, challengeSignature); err != nil {
		return CompleteRegistrationResult{}, err
	}

	challenge.UsedAt = &now

	otpEnabled := s.emailSender != nil && s.verifications != nil

	var (
		org              core.Organization
		user             core.User
		agent            core.Agent
		rawToken         string
		tokenExp         time.Time
		isFirstOrg       bool
		firstInviteToken string
	)

	if err := s.tx.WithTx(ctx, func(tx storage.StoreTx) error {
		if _, err := tx.SaveAgentRegistrationChallenge(ctx, challenge); err != nil {
			if errors.Is(err, storage.ErrChallengeAlreadyUsed) {
				return ErrUsedRegistrationChallenge
			}
			return fmt.Errorf("mark registration challenge used: %w", err)
		}

		var txErr error
		agentStatus := core.AgentStatusActive
		if otpEnabled {
			agentStatus = core.AgentStatusPendingEmailVerification
		}

		// Generate an invite token for first-time org creation.
		generatedInviteToken, genErr := randomToken(32)
		if genErr != nil {
			return fmt.Errorf("generate invite token: %w", genErr)
		}

		var result upsertRegisteredAgentTxResult
		result, txErr = upsertRegisteredAgentTx(ctx, tx, challenge.OrgSlug, challenge.OwnerEmail, challenge.AgentName, challenge.ClientType, challenge.PublicKey, s.cfg.DefaultOrgName, agentStatus, now, generatedInviteToken)
		if txErr != nil {
			return txErr
		}
		org = result.Org
		user = result.User
		agent = result.Agent
		isFirstOrg = result.IsFirstOrg
		if isFirstOrg {
			firstInviteToken = generatedInviteToken
		}

		// If org requires admin approval and this is not the first agent (first gets admin auto-approval),
		// set agent to pending_admin_approval.
		if !isFirstOrg && !otpEnabled && core.OrgRequiresAdminApproval(org.VerificationMode) {
			agent.Status = core.AgentStatusPendingAdminApproval
			agent, txErr = tx.UpsertAgent(ctx, agent)
			if txErr != nil {
				return fmt.Errorf("set pending admin approval status: %w", txErr)
			}
			if s.approvals != nil {
				approvalRecord := core.AgentApproval{
					ApprovalID:  id.New("aapproval"),
					AgentID:     agent.AgentID,
					OrgID:       org.OrgID,
					RequestedAt: now,
				}
				if saveErr := tx.SaveAgentApproval(ctx, approvalRecord); saveErr != nil {
					return fmt.Errorf("save agent approval: %w", saveErr)
				}
			}
		}

		var token core.AgentToken
		token, rawToken, txErr = issueTokenTx(ctx, tx, agent.AgentID, s.cfg.AuthTokenTTL, now)
		if txErr != nil {
			return txErr
		}
		tokenExp = token.ExpiresAt
		return nil
	}); err != nil {
		return CompleteRegistrationResult{}, err
	}

	// Send OTP verification email outside the transaction.
	if otpEnabled {
		if sendErr := s.sendVerificationEmail(ctx, agent, challenge.OwnerEmail, now); sendErr != nil {
			slog.Error("failed to send verification email", "agent_id", agent.AgentID, "err", sendErr)
			// Do not block registration; agent stays pending_email_verification.
		}
	}

	return CompleteRegistrationResult{
		Org:              org,
		User:             user,
		Agent:            agent,
		AccessToken:      rawToken,
		TokenExpiresAt:   tokenExp,
		FirstInviteToken: firstInviteToken,
	}, nil
}

// sendVerificationEmail generates a 6-digit OTP, saves the verification record, and emails it.
func (s *Service) sendVerificationEmail(ctx context.Context, agent core.Agent, email string, now time.Time) error {
	code, err := generateOTPCode()
	if err != nil {
		return fmt.Errorf("generate OTP code: %w", err)
	}
	codeHash := hashOTPCode(code)

	v := core.EmailVerification{
		VerificationID: id.New("ev"),
		AgentID:        agent.AgentID,
		OrgID:          agent.OrgID,
		Email:          email,
		CodeHash:       codeHash,
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.cfg.EmailOTPTTL),
		Attempts:       0,
	}

	if _, err := s.verifications.SaveEmailVerification(ctx, v); err != nil {
		return fmt.Errorf("save email verification: %w", err)
	}

	subject := "Your Alice verification code"
	body := fmt.Sprintf(
		"Your Alice verification code is: %s\n\nThis code expires in %s.\n\nDo not share this code with anyone.",
		code,
		s.cfg.EmailOTPTTL.String(),
	)

	if err := s.emailSender.Send(ctx, email, subject, body); err != nil {
		return fmt.Errorf("send OTP email: %w", err)
	}

	slog.Info("email verification sent", "agent_id", agent.AgentID, "email", email)
	return nil
}

// VerifyEmail checks the OTP code and promotes the agent to active on success.
func (s *Service) VerifyEmail(ctx context.Context, agentID, code string) error {
	if s.verifications == nil {
		return core.ValidationError{Message: "email verification is not configured"}
	}

	v, ok, err := s.verifications.FindPendingVerification(ctx, agentID)
	if err != nil {
		return fmt.Errorf("find pending verification: %w", err)
	}
	if !ok {
		return ErrVerificationNotFound
	}

	now := time.Now().UTC()
	if now.After(v.ExpiresAt) {
		return ErrVerificationExpired
	}

	if v.Attempts >= s.cfg.EmailOTPMaxAttempts {
		return ErrVerificationMaxAttempts
	}

	// Constant-time comparison on the SHA-256 hash to prevent timing attacks.
	expected := []byte(v.CodeHash)
	actual := []byte(hashOTPCode(strings.TrimSpace(code)))
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		if incErr := s.verifications.IncrementVerificationAttempts(ctx, v.VerificationID); incErr != nil {
			slog.Error("increment verification attempts failed", "verification_id", v.VerificationID, "err", incErr)
		}
		return ErrInvalidVerificationCode
	}

	if err := s.verifications.MarkEmailVerified(ctx, v.VerificationID, now); err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}

	// Promote agent to active (or pending_admin_approval if org requires it).
	agent, ok, err := s.agents.FindAgentByID(ctx, agentID)
	if err != nil {
		return fmt.Errorf("find agent: %w", err)
	}
	if !ok {
		return ErrUnknownAgent
	}

	newStatus := core.AgentStatusActive

	// Check whether this org requires admin approval and whether this user is an admin.
	ownerUser, userFound, userErr := s.users.FindUserByID(ctx, agent.OwnerUserID)
	if userErr != nil {
		return fmt.Errorf("find owner user: %w", userErr)
	}
	if userFound && ownerUser.Role != core.UserRoleAdmin {
		org, orgFound, orgErr := s.orgs.FindOrganizationByID(ctx, agent.OrgID)
		if orgErr != nil {
			return fmt.Errorf("find org: %w", orgErr)
		}
		if orgFound && core.OrgRequiresAdminApproval(org.VerificationMode) {
			newStatus = core.AgentStatusPendingAdminApproval
			// Create approval record if the approvals repo is wired.
			if s.approvals != nil {
				approvalRecord := core.AgentApproval{
					ApprovalID:  id.New("aapproval"),
					AgentID:     agent.AgentID,
					OrgID:       agent.OrgID,
					RequestedAt: now,
				}
				if saveErr := s.approvals.SaveAgentApproval(ctx, approvalRecord); saveErr != nil {
					return fmt.Errorf("save agent approval: %w", saveErr)
				}
			}
		}
	}

	agent.Status = newStatus
	if _, err := s.agents.UpsertAgent(ctx, agent); err != nil {
		return fmt.Errorf("promote agent to active: %w", err)
	}

	slog.Info("email verified", "agent_id", agentID)
	return nil
}

// ResendVerificationEmail invalidates the existing verification and sends a new code.
// Rate-limited to one resend per 60 seconds.
func (s *Service) ResendVerificationEmail(ctx context.Context, agentID string) error {
	if s.verifications == nil || s.emailSender == nil {
		return core.ValidationError{Message: "email verification is not configured"}
	}

	v, ok, err := s.verifications.FindPendingVerification(ctx, agentID)
	if err != nil {
		return fmt.Errorf("find pending verification: %w", err)
	}
	if !ok {
		return ErrVerificationNotFound
	}

	now := time.Now().UTC()

	// Rate-limit: one resend per 60 seconds.
	if now.Sub(v.CreatedAt) < 60*time.Second {
		return ErrResendTooSoon
	}

	agent, ok, err := s.agents.FindAgentByID(ctx, agentID)
	if err != nil {
		return fmt.Errorf("find agent: %w", err)
	}
	if !ok {
		return ErrUnknownAgent
	}

	return s.sendVerificationEmail(ctx, agent, v.Email, now)
}

// generateOTPCode returns a cryptographically random 6-digit code string.
func generateOTPCode() (string, error) {
	max := big.NewInt(1000000) // 6 digits: [000000, 999999]
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// hashOTPCode returns the hex-encoded SHA-256 of the code.
func hashOTPCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// upsertRegisteredAgentTxResult holds the outputs of upsertRegisteredAgentTx.
type upsertRegisteredAgentTxResult struct {
	Org        core.Organization
	User       core.User
	Agent      core.Agent
	IsFirstOrg bool // true when the org was just created (first ever registration)
}

func upsertRegisteredAgentTx(ctx context.Context, tx storage.StoreTx, orgSlug, ownerEmail, agentName, clientType, publicKey, defaultOrgName, agentStatus string, now time.Time, firstInviteToken string) (upsertRegisteredAgentTxResult, error) {
	org, ok, err := tx.FindOrganizationBySlug(ctx, orgSlug)
	if err != nil {
		return upsertRegisteredAgentTxResult{}, fmt.Errorf("find organization by slug: %w", err)
	}
	isFirstOrg := false
	if !ok {
		isFirstOrg = true
		inviteTokenHash := ""
		if firstInviteToken != "" {
			inviteTokenHash = hashToken(firstInviteToken)
		}
		org = core.Organization{
			OrgID:            id.New("org"),
			Name:             defaultOrgName,
			Slug:             strings.ToLower(strings.TrimSpace(orgSlug)),
			CreatedAt:        now,
			Status:           "active",
			VerificationMode: "email_otp",
			InviteTokenHash:  inviteTokenHash,
		}
		org, err = tx.UpsertOrganization(ctx, org)
		if err != nil {
			return upsertRegisteredAgentTxResult{}, fmt.Errorf("upsert organization: %w", err)
		}
	}

	user, ok, err := tx.FindUserByEmail(ctx, org.OrgID, ownerEmail)
	if err != nil {
		return upsertRegisteredAgentTxResult{}, fmt.Errorf("find user by email: %w", err)
	}
	if !ok {
		userRole := core.UserRoleMember
		if isFirstOrg {
			userRole = core.UserRoleAdmin
		}
		user = core.User{
			UserID:      id.New("user"),
			OrgID:       org.OrgID,
			Email:       strings.ToLower(strings.TrimSpace(ownerEmail)),
			DisplayName: ownerEmail,
			CreatedAt:   now,
			Status:      "active",
			Role:        userRole,
		}
		user, err = tx.UpsertUser(ctx, user)
		if err != nil {
			return upsertRegisteredAgentTxResult{}, fmt.Errorf("upsert user: %w", err)
		}
	}

	agent, ok, err := tx.FindAgentByUserID(ctx, user.UserID)
	if err != nil {
		return upsertRegisteredAgentTxResult{}, fmt.Errorf("find agent by user id: %w", err)
	}
	if ok {
		agent.AgentName = agentName
		agent.ClientType = clientType
		agent.PublicKey = publicKey
		agent.LastSeenAt = now
		// Preserve existing status for re-registration (agent already active → stay active).
		if agent.Status == "" {
			agent.Status = agentStatus
		}
	} else {
		agent = core.Agent{
			AgentID:     id.New("agent"),
			OrgID:       org.OrgID,
			OwnerUserID: user.UserID,
			AgentName:   agentName,
			RuntimeKind: "edge",
			ClientType:  clientType,
			PublicKey:   publicKey,
			Status:      agentStatus,
			LastSeenAt:  now,
		}
	}

	agent, err = tx.UpsertAgent(ctx, agent)
	if err != nil {
		return upsertRegisteredAgentTxResult{}, fmt.Errorf("upsert agent: %w", err)
	}
	return upsertRegisteredAgentTxResult{Org: org, User: user, Agent: agent, IsFirstOrg: isFirstOrg}, nil
}

func (s *Service) AuthenticateAgent(ctx context.Context, accessToken string) (core.Agent, core.User, error) {
	tokenID, ok := splitToken(accessToken)
	if !ok {
		return core.Agent{}, core.User{}, ErrInvalidAgentToken
	}

	token, found, err := s.tokens.FindAgentTokenByID(ctx, tokenID)
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
	if _, err := s.tokens.SaveAgentToken(ctx, token); err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("update token last used at: %w", err)
	}

	return s.RequireAgent(ctx, token.AgentID)
}

func (s *Service) RequireAgent(ctx context.Context, agentID string) (core.Agent, core.User, error) {
	agent, ok, err := s.agents.FindAgentByID(ctx, agentID)
	if err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("find agent by id: %w", err)
	}
	if !ok {
		return core.Agent{}, core.User{}, ErrUnknownAgent
	}
	user, ok, err := s.users.FindUserByID(ctx, agent.OwnerUserID)
	if err != nil {
		return core.Agent{}, core.User{}, fmt.Errorf("find user by id: %w", err)
	}
	if !ok {
		return core.Agent{}, core.User{}, ErrUnknownAgentOwner
	}
	return agent, user, nil
}

func (s *Service) FindUserByEmail(ctx context.Context, orgID, email string) (core.User, bool, error) {
	return s.users.FindUserByEmail(ctx, orgID, email)
}

// ListPendingAgentApprovals returns pending agent approvals for the org. The caller must be an org admin.
func (s *Service) ListPendingAgentApprovals(ctx context.Context, orgID, callerAgentID string, limit, offset int) ([]core.AgentApproval, error) {
	if s.approvals == nil {
		return nil, nil
	}
	callerAgent, ok, err := s.agents.FindAgentByID(ctx, callerAgentID)
	if err != nil {
		return nil, fmt.Errorf("find caller agent: %w", err)
	}
	if !ok {
		return nil, ErrUnknownAgent
	}
	if callerAgent.OrgID != orgID {
		return nil, core.ForbiddenError{Message: "caller does not belong to this org"}
	}
	callerUser, ok, err := s.users.FindUserByID(ctx, callerAgent.OwnerUserID)
	if err != nil {
		return nil, fmt.Errorf("find caller user: %w", err)
	}
	if !ok {
		return nil, ErrUnknownAgentOwner
	}
	if callerUser.Role != core.UserRoleAdmin {
		return nil, ErrNotOrgAdmin
	}
	return s.approvals.FindPendingAgentApprovals(ctx, orgID, limit, offset)
}

// ReviewAgentApproval approves or rejects a pending agent registration. The caller must be an org admin.
func (s *Service) ReviewAgentApproval(ctx context.Context, orgID, targetAgentID, callerAgentID, decision, reason string) error {
	if s.approvals == nil {
		return ErrAgentApprovalNotFound
	}
	callerAgent, ok, err := s.agents.FindAgentByID(ctx, callerAgentID)
	if err != nil {
		return fmt.Errorf("find caller agent: %w", err)
	}
	if !ok {
		return ErrUnknownAgent
	}
	if callerAgent.OrgID != orgID {
		return core.ForbiddenError{Message: "caller does not belong to this org"}
	}
	callerUser, ok, err := s.users.FindUserByID(ctx, callerAgent.OwnerUserID)
	if err != nil {
		return fmt.Errorf("find caller user: %w", err)
	}
	if !ok {
		return ErrUnknownAgentOwner
	}
	if callerUser.Role != core.UserRoleAdmin {
		return ErrNotOrgAdmin
	}

	approval, err := s.approvals.FindAgentApprovalByAgentID(ctx, targetAgentID)
	if err != nil {
		if errors.Is(err, storage.ErrAgentApprovalNotFound) {
			return ErrAgentApprovalNotFound
		}
		return fmt.Errorf("find agent approval: %w", err)
	}

	now := time.Now().UTC()

	if err := s.tx.WithTx(ctx, func(tx storage.StoreTx) error {
		if txErr := tx.UpdateAgentApproval(ctx, approval.ApprovalID, decision, reason, callerUser.UserID, now); txErr != nil {
			return fmt.Errorf("update agent approval: %w", txErr)
		}

		targetAgent, agentOk, agentErr := tx.FindAgentByID(ctx, targetAgentID)
		if agentErr != nil {
			return fmt.Errorf("find target agent: %w", agentErr)
		}
		if !agentOk {
			return ErrUnknownAgent
		}

		switch decision {
		case "approved":
			targetAgent.Status = core.AgentStatusActive
		case "rejected":
			targetAgent.Status = core.AgentStatusRejected
			if revokeErr := tx.RevokeAllTokensForAgent(ctx, targetAgentID, now); revokeErr != nil {
				return fmt.Errorf("revoke agent tokens: %w", revokeErr)
			}
		default:
			return core.ValidationError{Message: "decision must be 'approved' or 'rejected'"}
		}

		if _, txErr := tx.UpsertAgent(ctx, targetAgent); txErr != nil {
			return fmt.Errorf("update target agent status: %w", txErr)
		}

		eventKind := "agent.approval_approved"
		if decision == "rejected" {
			eventKind = "agent.approval_rejected"
		}
		auditEvent := core.AuditEvent{
			AuditEventID:  id.New("audit"),
			OrgID:         orgID,
			EventKind:     eventKind,
			ActorAgentID:  callerAgentID,
			TargetAgentID: targetAgentID,
			SubjectType:   "agent",
			SubjectID:     targetAgentID,
			Decision:      decision,
			CreatedAt:     now,
			Metadata: map[string]any{
				"reason":      reason,
				"reviewed_by": callerUser.UserID,
			},
		}
		if _, auditErr := tx.AppendAuditEvent(ctx, auditEvent); auditErr != nil {
			slog.Error("audit record failed", "op", "agent_approval", "err", auditErr)
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (s *Service) FindUserByID(ctx context.Context, userID string) (core.User, bool, error) {
	return s.users.FindUserByID(ctx, userID)
}

func (s *Service) FindAgentByUserID(ctx context.Context, userID string) (core.Agent, bool, error) {
	return s.agents.FindAgentByUserID(ctx, userID)
}

func issueTokenTx(ctx context.Context, tx storage.StoreTx, agentID string, ttl time.Duration, now time.Time) (core.AgentToken, string, error) {
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
		ExpiresAt:  now.Add(ttl),
		LastUsedAt: now,
	}

	token, err = tx.SaveAgentToken(ctx, token)
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
