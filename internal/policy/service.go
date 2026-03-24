package policy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

type Service struct {
	repo storage.PolicyGrantRepository
}

func NewService(repo storage.PolicyGrantRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Grant(ctx context.Context, orgID string, grantorUser core.User, granteeUser core.User, scopeType, scopeRef string, artifactTypes []core.ArtifactType, maxSensitivity core.Sensitivity, purposes []core.QueryPurpose) (core.PolicyGrant, error) {
	if err := core.ValidateGrantInput(granteeUser.Email, scopeType, scopeRef, artifactTypes, maxSensitivity, purposes); err != nil {
		return core.PolicyGrant{}, err
	}

	grant := core.PolicyGrant{
		PolicyGrantID:             id.New("grant"),
		OrgID:                     orgID,
		GrantorUserID:             grantorUser.UserID,
		GranteeUserID:             granteeUser.UserID,
		ScopeType:                 strings.TrimSpace(scopeType),
		ScopeRef:                  strings.TrimSpace(scopeRef),
		AllowedArtifactTypes:      artifactTypes,
		MaxSensitivity:            maxSensitivity,
		AllowedPurposes:           purposes,
		VisibilityMode:            core.VisibilityModeExplicitGrantsOnly,
		RequiresApprovalAboveRisk: core.RiskLevelL1,
		CreatedAt:                 time.Now().UTC(),
	}

	saved, err := s.repo.SaveGrant(ctx, grant)
	if err != nil {
		return core.PolicyGrant{}, fmt.Errorf("save grant: %w", err)
	}
	return saved, nil
}

func (s *Service) ListGrantsForPair(ctx context.Context, grantorUserID, granteeUserID string) ([]core.PolicyGrant, error) {
	return s.repo.ListGrantsForPair(ctx, grantorUserID, granteeUserID)
}

func (s *Service) ListAllowedPeers(ctx context.Context, granteeUserID string) ([]core.PolicyGrant, error) {
	return s.repo.ListIncomingGrantsForUser(ctx, granteeUserID)
}
