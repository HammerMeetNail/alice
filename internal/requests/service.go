package requests

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

// AutoAnswerer is the subset of the gatekeeper surface the requests service
// needs. It is declared here to avoid importing the gatekeeper package (which
// itself imports queries) and to let tests supply fakes.
type AutoAnswerer interface {
	Evaluate(ctx context.Context, request core.Request) AutoAnswerResult
}

// AutoAnswerResult mirrors gatekeeper.AutoAnswer without importing the
// package. The concrete gatekeeper adapter in internal/gatekeeper wraps this
// into the service via WithAutoAnswerer.
type AutoAnswerResult struct {
	Answered    bool
	Summary     string
	ArtifactIDs []string
	Confidence  float64
	Reason      string
}

// AuditRecorder is the subset of the audit surface the requests service needs
// to record auto-answer decisions. The audit.Service implements it directly.
// Declared here so the requests package doesn't import audit (and so tests
// can supply fakes).
type AuditRecorder interface {
	Record(ctx context.Context, eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) (core.AuditEvent, error)
}

type Service struct {
	requests   storage.RequestRepository
	approvals  storage.ApprovalRepository
	tx         storage.Transactor
	autoAnswer AutoAnswerer
	audit      AuditRecorder
}

func NewService(requests storage.RequestRepository, approvals storage.ApprovalRepository, tx storage.Transactor) *Service {
	return &Service{
		requests:  requests,
		approvals: approvals,
		tx:        tx,
	}
}

// WithAutoAnswerer enables the gatekeeper's auto-answer path. When set, Send
// will attempt to answer eligible requests from the recipient's existing
// derived artifacts before leaving them pending for the human.
func (s *Service) WithAutoAnswerer(a AutoAnswerer) *Service {
	s.autoAnswer = a
	return s
}

// WithAuditRecorder attaches an audit recorder. When set alongside an
// AutoAnswerer, successful auto-answers are recorded as
// `request.auto_answered` events carrying the supporting artifact IDs and
// aggregate confidence — the gatekeeper is acting on the recipient's behalf,
// so the decision needs the same audit trail as a human response.
func (s *Service) WithAuditRecorder(a AuditRecorder) *Service {
	s.audit = a
	return s
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

	// Give the gatekeeper a chance to answer from the recipient's existing
	// derived artifacts before the request sits in the human's inbox. A
	// failure here is never fatal — the request remains pending and the
	// human handles it normally.
	if s.autoAnswer != nil {
		if answer := s.autoAnswer.Evaluate(ctx, saved); answer.Answered {
			message := answer.Summary
			if message == "" {
				message = "Auto-answered by the recipient's agent from derived artifacts."
			}
			updated, ok, updateErr := s.requests.UpdateRequestState(ctx,
				saved.RequestID, core.RequestStateAutoAnswered,
				saved.ApprovalState, message)
			if updateErr == nil && ok {
				s.recordAutoAnswer(ctx, updated, answer)
				return updated, nil
			}
		}
	}

	return saved, nil
}

// recordAutoAnswer best-effort emits a `request.auto_answered` audit event.
// Failure is logged but never propagated — the request has already been
// answered and the caller should not see a 500 because of an audit sink
// issue.
func (s *Service) recordAutoAnswer(ctx context.Context, req core.Request, answer AutoAnswerResult) {
	if s.audit == nil {
		return
	}
	metadata := map[string]any{
		"request_type":  req.RequestType,
		"confidence":    answer.Confidence,
		"artifact_ids":  answer.ArtifactIDs,
		"artifact_count": len(answer.ArtifactIDs),
		"summary_bytes": len(answer.Summary),
	}
	if _, err := s.audit.Record(ctx,
		"request.auto_answered", "request", req.RequestID,
		req.OrgID, req.ToAgentID, req.FromAgentID,
		"allow", req.RiskLevel, nil, metadata); err != nil {
		slog.Error("audit record failed", "op", "request_auto_answered", "request_id", req.RequestID, "err", err)
	}
}

func (s *Service) ListIncoming(ctx context.Context, agentID string, limit, offset int) ([]core.Request, error) {
	requests, err := s.requests.ListIncomingRequests(ctx, agentID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list incoming requests: %w", err)
	}
	return requests, nil
}

func (s *Service) ListSent(ctx context.Context, agentID string, limit, offset int) ([]core.Request, error) {
	requests, err := s.requests.ListSentRequests(ctx, agentID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list sent requests: %w", err)
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
		now := time.Now().UTC()
		approval := core.Approval{
			ApprovalID:  id.New("approval"),
			OrgID:       request.OrgID,
			AgentID:     agent.AgentID,
			OwnerUserID: agent.OwnerUserID,
			SubjectType: "request",
			SubjectID:   request.RequestID,
			Reason:      "Request response requires user approval before disclosure or acceptance.",
			State:       core.ApprovalStatePending,
			CreatedAt:   now,
			ExpiresAt:   now.Add(2 * time.Hour),
		}

		var savedApproval core.Approval
		var updatedRequest core.Request

		if err := s.tx.WithTx(ctx, func(tx storage.StoreTx) error {
			var txErr error
			savedApproval, txErr = tx.SaveApproval(ctx, approval)
			if txErr != nil {
				return fmt.Errorf("save approval: %w", txErr)
			}
			var ok bool
			updatedRequest, ok, txErr = tx.UpdateRequestState(ctx, request.RequestID, request.State, core.ApprovalStatePending, message)
			if txErr != nil {
				return fmt.Errorf("mark request approval pending: %w", txErr)
			}
			if !ok {
				return ErrUnknownRequest
			}
			return nil
		}); err != nil {
			return core.Request{}, nil, err
		}
		return updatedRequest, &savedApproval, nil
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
