package artifacts

import (
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

func (s *Service) PublishArtifact(agent core.Agent, user core.User, artifact core.Artifact) (core.Artifact, error) {
	if artifact.VisibilityMode == "" {
		artifact.VisibilityMode = core.VisibilityModeExplicitGrantsOnly
	}
	if artifact.ApprovalState == "" {
		artifact.ApprovalState = core.ApprovalStateNotRequired
	}
	if err := core.ValidateArtifactInput(artifact); err != nil {
		return core.Artifact{}, err
	}

	artifact.ArtifactID = id.New("artifact")
	artifact.OrgID = agent.OrgID
	artifact.OwnerAgentID = agent.AgentID
	artifact.OwnerUserID = user.UserID
	artifact.CreatedAt = time.Now().UTC()

	return s.store.SaveArtifact(artifact), nil
}

func (s *Service) ListArtifactsByOwner(userID string) []core.Artifact {
	return s.store.ListArtifactsByOwner(userID)
}
