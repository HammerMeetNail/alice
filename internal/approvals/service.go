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
	queries   storage.QueryRepository
	tx        storage.Transactor
}

func NewService(approvals storage.ApprovalRepository, requests storage.RequestRepository, queries storage.QueryRepository, tx storage.Transactor) *Service {
	return &Service{
		approvals: approvals,
		requests:  requests,
		queries:   queries,
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

	var resolvedApproval core.Approval
	var updatedRequest core.Request

	if approval.SubjectType == "query" {
		// Risk-based approval: update the query response approval state.
		queryApprovalState := decision // approved or denied maps directly to ApprovalState
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
			queryState := core.QueryStateCompleted
			if decision == core.ApprovalStateDenied {
				queryState = core.QueryStateDenied
			}
			if _, ok, txErr = tx.UpdateQueryResponseApprovalState(ctx, approval.SubjectID, queryApprovalState); txErr != nil {
				return fmt.Errorf("update query response after approval: %w", txErr)
			} else if !ok {
				return ErrUnknownRequest
			}
			if _, ok, txErr = tx.UpdateQueryState(ctx, approval.SubjectID, queryState); txErr != nil {
				return fmt.Errorf("update query state after approval: %w", txErr)
			} else if !ok {
				return ErrUnknownRequest
			}
			return nil
		}); err != nil {
			return core.Approval{}, core.Request{}, err
		}
		return resolvedApproval, core.Request{}, nil
	}

	requestState := core.RequestStateAccepted
	if decision == core.ApprovalStateDenied {
		requestState = core.RequestStateDenied
	}

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
