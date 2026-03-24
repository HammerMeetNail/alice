package requests

import (
	"context"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

type Service struct {
	requests  storage.RequestRepository
	approvals storage.ApprovalRepository
}

func NewService(requests storage.RequestRepository, approvals storage.ApprovalRepository) *Service {
	return &Service{
		requests:  requests,
		approvals: approvals,
	}
}

func (s *Service) Send(ctx context.Context, request core.Request) (core.Request, error) {
	request.RequestID = id.New("request")
	request.State = core.RequestStatePending
	request.ApprovalState = core.ApprovalStateNotRequired
	request.CreatedAt = time.Now().UTC()
	if request.ExpiresAt.IsZero() {
		request.ExpiresAt = request.CreatedAt.Add(24 * time.Hour)
	}

	saved, err := s.requests.SaveRequest(ctx, request)
	if err != nil {
		return core.Request{}, fmt.Errorf("save request: %w", err)
	}
	return saved, nil
}

func (s *Service) ListIncoming(ctx context.Context, agentID string) ([]core.Request, error) {
	requests, err := s.requests.ListIncomingRequests(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list incoming requests: %w", err)
	}
	return requests, nil
}

func (s *Service) Respond(ctx context.Context, agent core.Agent, requestID string, action core.RequestResponseAction, message string) (core.Request, *core.Approval, error) {
	request, found, err := s.requests.FindRequest(ctx, requestID)
	if err != nil {
		return core.Request{}, nil, fmt.Errorf("find request: %w", err)
	}
	if !found {
		return core.Request{}, nil, ErrUnknownRequest
	}
	if request.ToAgentID != agent.AgentID {
		return core.Request{}, nil, ErrRequestNotVisible
	}
	if request.State != core.RequestStatePending {
		return core.Request{}, nil, ErrRequestAlreadyClosed
	}
	if !request.ExpiresAt.IsZero() && request.ExpiresAt.Before(time.Now().UTC()) {
		return core.Request{}, nil, ErrExpiredRequest
	}

	if action == core.RequestResponseRequireApproval {
		approval := core.Approval{
			ApprovalID:  id.New("approval"),
			OrgID:       request.OrgID,
			AgentID:     agent.AgentID,
			OwnerUserID: agent.OwnerUserID,
			SubjectType: "request",
			SubjectID:   request.RequestID,
			Reason:      "Request response requires user approval before disclosure or acceptance.",
			State:       core.ApprovalStatePending,
			CreatedAt:   time.Now().UTC(),
			ExpiresAt:   time.Now().UTC().Add(2 * time.Hour),
		}
		savedApproval, err := s.approvals.SaveApproval(ctx, approval)
		if err != nil {
			return core.Request{}, nil, fmt.Errorf("save approval: %w", err)
		}
		updated, found, err := s.requests.UpdateRequestState(ctx, request.RequestID, request.State, core.ApprovalStatePending, message)
		if err != nil {
			return core.Request{}, nil, fmt.Errorf("mark request approval pending: %w", err)
		}
		if !found {
			return core.Request{}, nil, ErrUnknownRequest
		}
		return updated, &savedApproval, nil
	}

	nextState := actionToState(action)
	updated, found, err := s.requests.UpdateRequestState(ctx, request.RequestID, nextState, request.ApprovalState, message)
	if err != nil {
		return core.Request{}, nil, fmt.Errorf("update request state: %w", err)
	}
	if !found {
		return core.Request{}, nil, ErrUnknownRequest
	}
	return updated, nil, nil
}

func actionToState(action core.RequestResponseAction) core.RequestState {
	switch action {
	case core.RequestResponseAccept:
		return core.RequestStateAccepted
	case core.RequestResponseDefer:
		return core.RequestStateDeferred
	case core.RequestResponseDeny:
		return core.RequestStateDenied
	case core.RequestResponseComplete:
		return core.RequestStateCompleted
	default:
		return core.RequestStatePending
	}
}
