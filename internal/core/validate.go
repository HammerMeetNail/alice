package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func IsValidationError(err error) bool {
	var target ValidationError
	return errors.As(err, &target)
}

func invalidf(format string, args ...any) error {
	return ValidationError{Message: fmt.Sprintf(format, args...)}
}

func invalid(message string) error {
	return ValidationError{Message: message}
}

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
		return invalid("org_slug is required")
	case strings.TrimSpace(ownerEmail) == "":
		return invalid("owner_email is required")
	case strings.TrimSpace(agentName) == "":
		return invalid("agent_name is required")
	case strings.TrimSpace(clientType) == "":
		return invalid("client_type is required")
	case strings.TrimSpace(publicKey) == "":
		return invalid("public_key is required")
	default:
		return nil
	}
}

func ValidateRegistrationCompletion(challengeID, challengeSignature string) error {
	switch {
	case strings.TrimSpace(challengeID) == "":
		return invalid("challenge_id is required")
	case strings.TrimSpace(challengeSignature) == "":
		return invalid("challenge_signature is required")
	default:
		return nil
	}
}

func ValidateArtifactInput(artifact Artifact) error {
	if !slices.Contains(allowedArtifactTypes, artifact.Type) {
		return invalidf("invalid artifact type %q", artifact.Type)
	}
	if strings.TrimSpace(artifact.Title) == "" {
		return invalid("artifact title is required")
	}
	if strings.TrimSpace(artifact.Content) == "" {
		return invalid("artifact content is required")
	}
	if artifact.Confidence < 0 || artifact.Confidence > 1 {
		return invalid("artifact confidence must be between 0 and 1")
	}
	if !slices.Contains(allowedSensitivities, artifact.Sensitivity) {
		return invalidf("invalid sensitivity %q", artifact.Sensitivity)
	}
	if !slices.Contains(allowedVisibilityModes, artifact.VisibilityMode) {
		return invalidf("invalid visibility mode %q", artifact.VisibilityMode)
	}
	if len(artifact.SourceRefs) == 0 {
		return invalid("artifact requires at least one source_ref")
	}
	for _, ref := range artifact.SourceRefs {
		if strings.TrimSpace(ref.SourceSystem) == "" || strings.TrimSpace(ref.SourceType) == "" || strings.TrimSpace(ref.SourceID) == "" {
			return invalid("source_ref requires source_system, source_type, and source_id")
		}
	}
	return nil
}

func ValidateGrantInput(granteeUserEmail, scopeType, scopeRef string, artifactTypes []ArtifactType, maxSensitivity Sensitivity, purposes []QueryPurpose) error {
	switch {
	case strings.TrimSpace(granteeUserEmail) == "":
		return invalid("grantee_user_email is required")
	case strings.TrimSpace(scopeType) == "":
		return invalid("scope_type is required")
	case strings.TrimSpace(scopeRef) == "":
		return invalid("scope_ref is required")
	case len(artifactTypes) == 0:
		return invalid("allowed_artifact_types is required")
	case len(purposes) == 0:
		return invalid("allowed_purposes is required")
	}

	for _, artifactType := range artifactTypes {
		if !slices.Contains(allowedArtifactTypes, artifactType) {
			return invalidf("invalid artifact type %q", artifactType)
		}
	}
	for _, purpose := range purposes {
		if !slices.Contains(allowedQueryPurposes, purpose) {
			return invalidf("invalid query purpose %q", purpose)
		}
	}
	if !slices.Contains(allowedSensitivities, maxSensitivity) {
		return invalidf("invalid max_sensitivity %q", maxSensitivity)
	}

	return nil
}

func ValidateQueryInput(toUserEmail string, purpose QueryPurpose, requestedTypes []ArtifactType, window TimeWindow) error {
	switch {
	case strings.TrimSpace(toUserEmail) == "":
		return invalid("to_user_email is required")
	case strings.TrimSpace(string(purpose)) == "":
		return invalid("purpose is required")
	case len(requestedTypes) == 0:
		return invalid("requested_types is required")
	case window.Start.IsZero() || window.End.IsZero():
		return invalid("time_window start and end are required")
	case window.End.Before(window.Start):
		return invalid("time_window end must not be before start")
	}

	if !slices.Contains(allowedQueryPurposes, purpose) {
		return invalidf("invalid query purpose %q", purpose)
	}
	for _, artifactType := range requestedTypes {
		if !slices.Contains(allowedArtifactTypes, artifactType) {
			return invalidf("invalid requested type %q", artifactType)
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
