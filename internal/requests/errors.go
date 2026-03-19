package requests

import "errors"

var (
	ErrUnknownRequest       = errors.New("unknown request")
	ErrRequestNotVisible    = errors.New("request is not visible to this agent")
	ErrRequestAlreadyClosed = errors.New("request is already closed")
)
