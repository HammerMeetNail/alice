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
)
