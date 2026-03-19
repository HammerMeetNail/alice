package policy

import (
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage/memory"
)

type Service struct {
	store *memory.Store
}

func NewService(store *memory.Store) *Service {
	return &Service{store: store}
}

func (s *Service) Grant(orgID string, grantorUser core.User, granteeUser core.User, scopeType, scopeRef string, artifactTypes []core.ArtifactType, maxSensitivity core.Sensitivity, purposes []core.QueryPurpose) (core.PolicyGrant, error) {
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

	return s.store.SaveGrant(grant), nil
}

func (s *Service) ListGrantsForPair(grantorUserID, granteeUserID string) []core.PolicyGrant {
	return s.store.ListGrantsForPair(grantorUserID, granteeUserID)
}

func (s *Service) ListAllowedPeers(granteeUserID string) []core.PolicyGrant {
	return s.store.ListIncomingGrantsForUser(granteeUserID)
}
