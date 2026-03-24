package approvals

import "errors"

var (
	ErrUnknownApproval    = errors.New("unknown approval")
	ErrApprovalNotVisible = errors.New("approval is not visible to this agent")
	ErrApprovalResolved   = errors.New("approval is already resolved")
	ErrExpiredApproval    = errors.New("approval has expired")
	ErrUnknownRequest     = errors.New("unknown request")
)
