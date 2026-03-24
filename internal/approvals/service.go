package approvals

import (
	"context"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

type Service struct {
	approvals storage.ApprovalRepository
	requests  storage.RequestRepository
}

func NewService(approvals storage.ApprovalRepository, requests storage.RequestRepository) *Service {
	return &Service{
		approvals: approvals,
		requests:  requests,
	}
}

func (s *Service) ListPending(ctx context.Context, agentID string) ([]core.Approval, error) {
	approvals, err := s.approvals.ListPendingApprovals(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals: %w", err)
	}
	return approvals, nil
}

func (s *Service) Resolve(ctx context.Context, agent core.Agent, approvalID string, decision core.ApprovalState) (core.Approval, core.Request, error) {
	approval, found, err := s.approvals.FindApproval(ctx, approvalID)
	if err != nil {
		return core.Approval{}, core.Request{}, fmt.Errorf("find approval: %w", err)
	}
	if !found {
		return core.Approval{}, core.Request{}, ErrUnknownApproval
	}
	if approval.AgentID != agent.AgentID {
		return core.Approval{}, core.Request{}, ErrApprovalNotVisible
	}
	if approval.State != core.ApprovalStatePending {
		return core.Approval{}, core.Request{}, ErrApprovalResolved
	}
	if !approval.ExpiresAt.IsZero() && approval.ExpiresAt.Before(time.Now().UTC()) {
		return core.Approval{}, core.Request{}, ErrExpiredApproval
	}

	resolvedApproval, found, err := s.approvals.ResolveApproval(ctx, approval.ApprovalID, decision, time.Now().UTC())
	if err != nil {
		return core.Approval{}, core.Request{}, fmt.Errorf("resolve approval: %w", err)
	}
	if !found {
		return core.Approval{}, core.Request{}, ErrUnknownApproval
	}

	requestState := core.RequestStateAccepted
	if decision == core.ApprovalStateDenied {
		requestState = core.RequestStateDenied
	}

	request, found, err := s.requests.UpdateRequestState(ctx, approval.SubjectID, requestState, decision, "")
	if err != nil {
		return core.Approval{}, core.Request{}, fmt.Errorf("update request after approval: %w", err)
	}
	if !found {
		return core.Approval{}, core.Request{}, ErrUnknownRequest
	}

	return resolvedApproval, request, nil
}
