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
	tx        storage.Transactor
}

func NewService(approvals storage.ApprovalRepository, requests storage.RequestRepository, tx storage.Transactor) *Service {
	return &Service{
		approvals: approvals,
		requests:  requests,
		tx:        tx,
	}
}

func (s *Service) ListPending(ctx context.Context, agentID string, limit, offset int) ([]core.Approval, error) {
	approvals, err := s.approvals.ListPendingApprovals(ctx, agentID, limit, offset)
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

	requestState := core.RequestStateAccepted
	if decision == core.ApprovalStateDenied {
		requestState = core.RequestStateDenied
	}

	var resolvedApproval core.Approval
	var updatedRequest core.Request

	if err := s.tx.WithTx(ctx, func(tx storage.StoreTx) error {
		var ok bool
		var txErr error
		resolvedApproval, ok, txErr = tx.ResolveApproval(ctx, approval.ApprovalID, decision, time.Now().UTC())
		if txErr != nil {
			return fmt.Errorf("resolve approval: %w", txErr)
		}
		if !ok {
			return ErrUnknownApproval
		}

		updatedRequest, ok, txErr = tx.UpdateRequestState(ctx, approval.SubjectID, requestState, decision, "")
		if txErr != nil {
			return fmt.Errorf("update request after approval: %w", txErr)
		}
		if !ok {
			return ErrUnknownRequest
		}
		return nil
	}); err != nil {
		return core.Approval{}, core.Request{}, err
	}

	return resolvedApproval, updatedRequest, nil
}
