package services

import (
	"time"

	"alice/internal/core"
)

type AgentService interface {
	BeginRegistration(orgSlug, ownerEmail, agentName, clientType, publicKey string, capabilities []string) (core.AgentRegistrationChallenge, string, error)
	CompleteRegistration(challengeID, challengeSignature string) (core.Organization, core.User, core.Agent, string, time.Time, error)
	AuthenticateAgent(accessToken string) (core.Agent, core.User, error)
	RequireAgent(agentID string) (core.Agent, core.User, error)
	FindUserByEmail(email string) (core.User, bool, error)
	FindUserByID(userID string) (core.User, bool, error)
	FindAgentByUserID(userID string) (core.Agent, bool, error)
}

type ArtifactService interface {
	PublishArtifact(agent core.Agent, user core.User, artifact core.Artifact) (core.Artifact, error)
	ListArtifactsByOwner(userID string) ([]core.Artifact, error)
}

type PolicyService interface {
	Grant(orgID string, grantorUser core.User, granteeUser core.User, scopeType, scopeRef string, artifactTypes []core.ArtifactType, maxSensitivity core.Sensitivity, purposes []core.QueryPurpose) (core.PolicyGrant, error)
	ListAllowedPeers(granteeUserID string) ([]core.PolicyGrant, error)
}

type QueryService interface {
	Evaluate(query core.Query) (core.QueryResponse, error)
	FindResult(queryID string) (core.Query, core.QueryResponse, bool, error)
}

type AuditService interface {
	Record(eventKind, subjectType, subjectID, orgID, actorAgentID, targetAgentID, decision string, riskLevel core.RiskLevel, policyBasis []string, metadata map[string]any) (core.AuditEvent, error)
	Summary(agentID string, since time.Time) ([]core.AuditEvent, error)
}

type Container struct {
	Agents    AgentService
	Artifacts ArtifactService
	Policy    PolicyService
	Queries   QueryService
	Audit     AuditService
}
