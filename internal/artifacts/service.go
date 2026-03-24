package artifacts

import (
	"context"
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

func (s *Service) PublishArtifact(ctx context.Context, agent core.Agent, user core.User, artifact core.Artifact) (core.Artifact, error) {
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

	saved, err := s.repo.SaveArtifact(ctx, artifact)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("save artifact: %w", err)
	}
	return saved, nil
}

// CorrectArtifact publishes a new artifact that supersedes a previously published one.
// The caller must own the artifact being corrected (same agent).
func (s *Service) CorrectArtifact(ctx context.Context, agent core.Agent, user core.User, originalArtifactID string, correction core.Artifact) (core.Artifact, error) {
	original, ok, err := s.repo.FindArtifactByID(ctx, originalArtifactID)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("find original artifact: %w", err)
	}
	if !ok {
		return core.Artifact{}, fmt.Errorf("artifact %s not found", originalArtifactID)
	}
	if original.OwnerAgentID != agent.AgentID {
		return core.Artifact{}, fmt.Errorf("artifact %s is not owned by the authenticated agent", originalArtifactID)
	}

	correction.SupersedesArtifactID = &originalArtifactID
	return s.PublishArtifact(ctx, agent, user, correction)
}

func (s *Service) ListArtifactsByOwner(ctx context.Context, userID string) ([]core.Artifact, error) {
	return s.repo.ListArtifactsByOwner(ctx, userID)
}
