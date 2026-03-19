package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	allowedArtifactTypes = []ArtifactType{
		ArtifactTypeSummary,
		ArtifactTypeCommitment,
		ArtifactTypeBlocker,
		ArtifactTypeStatusDelta,
		ArtifactTypeRequest,
	}
	allowedQueryPurposes = []QueryPurpose{
		QueryPurposeStatusCheck,
		QueryPurposeDependencyCheck,
		QueryPurposeHandoff,
		QueryPurposeManagerUpdate,
		QueryPurposeRequestContext,
	}
	allowedSensitivities = []Sensitivity{
		SensitivityLow,
		SensitivityMedium,
		SensitivityHigh,
		SensitivityRestricted,
	}
	allowedVisibilityModes = []VisibilityMode{
		VisibilityModePrivate,
		VisibilityModeExplicitGrantsOnly,
		VisibilityModeTeamScope,
		VisibilityModeManagerScope,
	}
)

func ValidateAgentRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey string) error {
	switch {
	case strings.TrimSpace(orgSlug) == "":
		return errors.New("org_slug is required")
	case strings.TrimSpace(ownerEmail) == "":
		return errors.New("owner_email is required")
	case strings.TrimSpace(agentName) == "":
		return errors.New("agent_name is required")
	case strings.TrimSpace(clientType) == "":
		return errors.New("client_type is required")
	case strings.TrimSpace(publicKey) == "":
		return errors.New("public_key is required")
	default:
		return nil
	}
}

func ValidateArtifactInput(artifact Artifact) error {
	if !slices.Contains(allowedArtifactTypes, artifact.Type) {
		return fmt.Errorf("invalid artifact type %q", artifact.Type)
	}
	if strings.TrimSpace(artifact.Title) == "" {
		return errors.New("artifact title is required")
	}
	if strings.TrimSpace(artifact.Content) == "" {
		return errors.New("artifact content is required")
	}
	if artifact.Confidence < 0 || artifact.Confidence > 1 {
		return errors.New("artifact confidence must be between 0 and 1")
	}
	if !slices.Contains(allowedSensitivities, artifact.Sensitivity) {
		return fmt.Errorf("invalid sensitivity %q", artifact.Sensitivity)
	}
	if !slices.Contains(allowedVisibilityModes, artifact.VisibilityMode) {
		return fmt.Errorf("invalid visibility mode %q", artifact.VisibilityMode)
	}
	if len(artifact.SourceRefs) == 0 {
		return errors.New("artifact requires at least one source_ref")
	}
	for _, ref := range artifact.SourceRefs {
		if strings.TrimSpace(ref.SourceSystem) == "" || strings.TrimSpace(ref.SourceType) == "" || strings.TrimSpace(ref.SourceID) == "" {
			return errors.New("source_ref requires source_system, source_type, and source_id")
		}
	}
	return nil
}

func ValidateGrantInput(granteeUserEmail, scopeType, scopeRef string, artifactTypes []ArtifactType, maxSensitivity Sensitivity, purposes []QueryPurpose) error {
	switch {
	case strings.TrimSpace(granteeUserEmail) == "":
		return errors.New("grantee_user_email is required")
	case strings.TrimSpace(scopeType) == "":
		return errors.New("scope_type is required")
	case strings.TrimSpace(scopeRef) == "":
		return errors.New("scope_ref is required")
	case len(artifactTypes) == 0:
		return errors.New("allowed_artifact_types is required")
	case len(purposes) == 0:
		return errors.New("allowed_purposes is required")
	}

	for _, artifactType := range artifactTypes {
		if !slices.Contains(allowedArtifactTypes, artifactType) {
			return fmt.Errorf("invalid artifact type %q", artifactType)
		}
	}
	for _, purpose := range purposes {
		if !slices.Contains(allowedQueryPurposes, purpose) {
			return fmt.Errorf("invalid query purpose %q", purpose)
		}
	}
	if !slices.Contains(allowedSensitivities, maxSensitivity) {
		return fmt.Errorf("invalid max_sensitivity %q", maxSensitivity)
	}

	return nil
}

func ValidateQueryInput(toUserEmail string, purpose QueryPurpose, requestedTypes []ArtifactType, window TimeWindow) error {
	switch {
	case strings.TrimSpace(toUserEmail) == "":
		return errors.New("to_user_email is required")
	case strings.TrimSpace(string(purpose)) == "":
		return errors.New("purpose is required")
	case len(requestedTypes) == 0:
		return errors.New("requested_types is required")
	case window.Start.IsZero() || window.End.IsZero():
		return errors.New("time_window start and end are required")
	case window.End.Before(window.Start):
		return errors.New("time_window end must not be before start")
	}

	if !slices.Contains(allowedQueryPurposes, purpose) {
		return fmt.Errorf("invalid query purpose %q", purpose)
	}
	for _, artifactType := range requestedTypes {
		if !slices.Contains(allowedArtifactTypes, artifactType) {
			return fmt.Errorf("invalid requested type %q", artifactType)
		}
	}

	return nil
}

var sensitivityOrder = map[Sensitivity]int{
	SensitivityLow:        0,
	SensitivityMedium:     1,
	SensitivityHigh:       2,
	SensitivityRestricted: 3,
}

func SensitivityAllowed(actual, ceiling Sensitivity) bool {
	return sensitivityOrder[actual] <= sensitivityOrder[ceiling]
}
