package actions

import (
	"context"

	"alice/internal/app/services"
	"alice/internal/core"
)

// CreateFromServicesParams lets callers outside the actions package invoke
// Create with the interface-defined param type. It exists so httpapi can
// populate services.ActionCreateParams without importing actions.
func (s *Service) CreateFromServicesParams(ctx context.Context, p services.ActionCreateParams) (core.Action, error) {
	return s.Create(ctx, CreateParams{
		OrgID:       p.OrgID,
		OwnerUser:   p.OwnerUser,
		OwnerAgent:  p.OwnerAgent,
		RequestID:   p.RequestID,
		Kind:        p.Kind,
		Inputs:      p.Inputs,
		RiskLevel:   p.RiskLevel,
		RequestType: p.RequestType,
	})
}
