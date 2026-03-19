package agents

import "errors"

var (
	ErrUnknownAgent      = errors.New("unknown agent")
	ErrUnknownAgentOwner = errors.New("unknown agent owner")
)
