package artifacts

import (
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/storage"
)

type Service struct {
	repo storage.ArtifactRepository
}

func NewService(repo storage.ArtifactRepository) *Service {
	return &Service{repo: repo}
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

	saved, err := s.repo.SaveArtifact(artifact)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("save artifact: %w", err)
	}
	return saved, nil
}

func (s *Service) ListArtifactsByOwner(userID string) ([]core.Artifact, error) {
	return s.repo.ListArtifactsByOwner(userID)
}
