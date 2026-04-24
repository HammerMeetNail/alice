package agents

import "errors"

var (
	ErrUnknownAgent                 = errors.New("unknown agent")
	ErrUnknownAgentOwner            = errors.New("unknown agent owner")
	ErrUnknownRegistrationChallenge = errors.New("unknown registration challenge")
	ErrExpiredRegistrationChallenge = errors.New("expired registration challenge")
	ErrUsedRegistrationChallenge    = errors.New("used registration challenge")
	ErrInvalidRegistrationSignature = errors.New("invalid registration signature")
	ErrUnknownAgentToken            = errors.New("unknown agent token")
	ErrInvalidAgentToken            = errors.New("invalid agent token")
	ErrExpiredAgentToken            = errors.New("expired agent token")
	ErrRevokedAgentToken            = errors.New("revoked agent token")

	// Email OTP verification errors.
	ErrVerificationNotFound    = errors.New("email verification not found")
	ErrVerificationExpired     = errors.New("email verification code expired")
	ErrVerificationMaxAttempts = errors.New("email verification max attempts exceeded")
	ErrInvalidVerificationCode = errors.New("invalid email verification code")
	ErrResendTooSoon           = errors.New("verification email resend too soon; wait 60 seconds")

	// Invite token errors.
	ErrInvalidInviteToken  = errors.New("invalid or missing invite token")
	ErrInviteTokenRequired = errors.New("invite token required to join this org")

	// Admin approval errors.
	ErrNotOrgAdmin             = errors.New("caller is not an org admin")
	ErrAgentRejected           = errors.New("agent has been rejected")
	ErrAgentApprovalNotFound   = errors.New("agent approval not found")

	// Org lifecycle errors.
	ErrOrgSlugTaken = errors.New("org slug already taken and cannot be reused")
)
