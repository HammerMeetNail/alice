package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

// AcknowledgeBlockerExecutor updates the linked request's response_message
// to acknowledge a teammate's blocker without waking the human. It is the
// smallest possible executor — purely internal, no external API — and
// exists both as a useful primitive and as a proving ground for the
// operator-phase rails.
type AcknowledgeBlockerExecutor struct {
	requests storage.RequestRepository
}

// NewAcknowledgeBlockerExecutor wires the executor to the requests store.
func NewAcknowledgeBlockerExecutor(requests storage.RequestRepository) *AcknowledgeBlockerExecutor {
	return &AcknowledgeBlockerExecutor{requests: requests}
}

// Kind implements Executor.
func (*AcknowledgeBlockerExecutor) Kind() core.ActionKind {
	return core.ActionKindAcknowledgeBlocker
}

// Validate ensures the caller supplied a message and that it is within a
// reasonable length. A too-long message is rejected at creation time so
// pending actions don't carry payloads that can't actually be executed.
func (*AcknowledgeBlockerExecutor) Validate(inputs map[string]any) error {
	raw, ok := inputs["message"]
	if !ok {
		return fmt.Errorf("message is required")
	}
	message, ok := raw.(string)
	if !ok {
		return fmt.Errorf("message must be a string")
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message must not be empty")
	}
	if len(message) > 2000 {
		return fmt.Errorf("message is %d bytes, max allowed is 2000", len(message))
	}
	return nil
}

// Execute updates the linked request's response_message and marks it
// completed. A missing request_id or a non-matching owner returns an
// error, keeping the action in the failed state; the caller's original
// request stays actionable by the human.
func (e *AcknowledgeBlockerExecutor) Execute(ctx context.Context, action core.Action, user core.User) (map[string]any, error) {
	if strings.TrimSpace(action.RequestID) == "" {
		return nil, fmt.Errorf("request_id is required for acknowledge_blocker")
	}
	request, ok, err := e.requests.FindRequest(ctx, action.RequestID)
	if err != nil {
		return nil, fmt.Errorf("find request: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("request %s not found", action.RequestID)
	}
	if request.ToUserID != user.UserID {
		return nil, fmt.Errorf("request %s is not owned by the action's user", action.RequestID)
	}

	message, _ := action.Inputs["message"].(string)
	updated, ok, err := e.requests.UpdateRequestState(ctx, request.RequestID, core.RequestStateCompleted, core.ApprovalStateNotRequired, message)
	if err != nil {
		return nil, fmt.Errorf("update request state: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("request %s vanished between lookups", action.RequestID)
	}
	return map[string]any{
		"request_id":        updated.RequestID,
		"response_message":  message,
		"request_state":     updated.State,
		"acknowledged_at":   time.Now().UTC(),
	}, nil
}
