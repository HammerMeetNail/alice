package core

import "time"

type ArtifactType string

const (
	ArtifactTypeSummary     ArtifactType = "summary"
	ArtifactTypeCommitment  ArtifactType = "commitment"
	ArtifactTypeBlocker     ArtifactType = "blocker"
	ArtifactTypeStatusDelta ArtifactType = "status_delta"
	ArtifactTypeRequest     ArtifactType = "request"
)

type QueryPurpose string

const (
	QueryPurposeStatusCheck     QueryPurpose = "status_check"
	QueryPurposeDependencyCheck QueryPurpose = "dependency_check"
	QueryPurposeHandoff         QueryPurpose = "handoff"
	QueryPurposeManagerUpdate   QueryPurpose = "manager_update"
	QueryPurposeRequestContext  QueryPurpose = "request_context"
)

type RiskLevel string

const (
	RiskLevelL0 RiskLevel = "L0"
	RiskLevelL1 RiskLevel = "L1"
	RiskLevelL2 RiskLevel = "L2"
	RiskLevelL3 RiskLevel = "L3"
	RiskLevelL4 RiskLevel = "L4"
)

type ApprovalState string

const (
	ApprovalStateNotRequired ApprovalState = "not_required"
	ApprovalStatePending     ApprovalState = "pending"
	ApprovalStateApproved    ApprovalState = "approved"
	ApprovalStateDenied      ApprovalState = "denied"
	ApprovalStateExpired     ApprovalState = "expired"
)

type Sensitivity string

const (
	SensitivityLow        Sensitivity = "low"
	SensitivityMedium     Sensitivity = "medium"
	SensitivityHigh       Sensitivity = "high"
	SensitivityRestricted Sensitivity = "restricted"
)

type TrustClass string

const (
	TrustClassTrustedPolicy    TrustClass = "trusted_policy"
	TrustClassStructuredSystem TrustClass = "structured_system"
	TrustClassUntrustedContent TrustClass = "untrusted_content"
)

type DeliveryStatus string

const (
	DeliveryStatusQueued    DeliveryStatus = "queued"
	DeliveryStatusSent      DeliveryStatus = "sent"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusFailed    DeliveryStatus = "failed"
	DeliveryStatusExpired   DeliveryStatus = "expired"
)

type RequestState string

const (
	RequestStatePending   RequestState = "pending"
	RequestStateAccepted  RequestState = "accepted"
	RequestStateDeferred  RequestState = "deferred"
	RequestStateDenied    RequestState = "denied"
	RequestStateCompleted RequestState = "completed"
	RequestStateExpired   RequestState = "expired"
)

type RequestResponseAction string

const (
	RequestResponseAccept          RequestResponseAction = "accepted"
	RequestResponseDefer           RequestResponseAction = "deferred"
	RequestResponseDeny            RequestResponseAction = "denied"
	RequestResponseComplete        RequestResponseAction = "completed"
	RequestResponseRequireApproval RequestResponseAction = "require_approval"
)

type VisibilityMode string

const (
	VisibilityModePrivate            VisibilityMode = "private"
	VisibilityModeExplicitGrantsOnly VisibilityMode = "explicit_grants_only"
	VisibilityModeTeamScope          VisibilityMode = "team_scope"
	VisibilityModeManagerScope       VisibilityMode = "manager_scope"
)

type QueryState string

const (
	QueryStateQueued    QueryState = "queued"
	QueryStateCompleted QueryState = "completed"
	QueryStateDenied    QueryState = "denied"
)

type Organization struct {
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
}

type User struct {
	UserID        string    `json:"user_id"`
	OrgID         string    `json:"org_id"`
	Email         string    `json:"email"`
	DisplayName   string    `json:"display_name"`
	RoleTitles    []string  `json:"role_titles"`
	ManagerUserID string    `json:"manager_user_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Status        string    `json:"status"`
}

type Agent struct {
	AgentID      string    `json:"agent_id"`
	OrgID        string    `json:"org_id"`
	OwnerUserID  string    `json:"owner_user_id"`
	AgentName    string    `json:"agent_name"`
	RuntimeKind  string    `json:"runtime_kind"`
	ClientType   string    `json:"client_type"`
	PublicKey    string    `json:"public_key"`
	Status       string    `json:"status"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type AgentRegistrationChallenge struct {
	ChallengeID  string     `json:"challenge_id"`
	OrgSlug      string     `json:"org_slug"`
	OwnerEmail   string     `json:"owner_email"`
	AgentName    string     `json:"agent_name"`
	ClientType   string     `json:"client_type"`
	PublicKey    string     `json:"public_key"`
	Nonce        string     `json:"nonce"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
}

type AgentToken struct {
	TokenID    string     `json:"token_id"`
	AgentID    string     `json:"agent_id"`
	TokenHash  string     `json:"-"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt time.Time  `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type SourceReference struct {
	SourceSystem string      `json:"source_system"`
	SourceType   string      `json:"source_type"`
	SourceID     string      `json:"source_id"`
	SourceURL    string      `json:"source_url,omitempty"`
	ObservedAt   time.Time   `json:"observed_at"`
	TrustClass   TrustClass  `json:"trust_class"`
	Sensitivity  Sensitivity `json:"sensitivity"`
}

type Artifact struct {
	ArtifactID           string            `json:"artifact_id"`
	OrgID                string            `json:"org_id"`
	OwnerAgentID         string            `json:"owner_agent_id"`
	OwnerUserID          string            `json:"owner_user_id"`
	Type                 ArtifactType      `json:"type"`
	Title                string            `json:"title"`
	Content              string            `json:"content"`
	StructuredPayload    map[string]any    `json:"structured_payload,omitempty"`
	SourceRefs           []SourceReference `json:"source_refs"`
	VisibilityMode       VisibilityMode    `json:"visibility_mode"`
	Sensitivity          Sensitivity       `json:"sensitivity"`
	Confidence           float64           `json:"confidence"`
	ApprovalState        ApprovalState     `json:"approval_state"`
	CreatedAt            time.Time         `json:"created_at"`
	ExpiresAt            *time.Time        `json:"expires_at,omitempty"`
	SupersedesArtifactID *string           `json:"supersedes_artifact_id,omitempty"`
}

type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type Query struct {
	QueryID        string         `json:"query_id"`
	OrgID          string         `json:"org_id"`
	FromAgentID    string         `json:"from_agent_id"`
	FromUserID     string         `json:"from_user_id"`
	ToAgentID      string         `json:"to_agent_id"`
	ToUserID       string         `json:"to_user_id"`
	Purpose        QueryPurpose   `json:"purpose"`
	Question       string         `json:"question"`
	RequestedTypes []ArtifactType `json:"requested_types"`
	ProjectScope   []string       `json:"project_scope,omitempty"`
	TimeWindow     TimeWindow     `json:"time_window"`
	RiskLevel      RiskLevel      `json:"risk_level"`
	State          QueryState     `json:"state"`
	CreatedAt      time.Time      `json:"created_at"`
	ExpiresAt      time.Time      `json:"expires_at"`
}

type QueryArtifact struct {
	ArtifactID  string       `json:"artifact_id"`
	Type        ArtifactType `json:"type"`
	Title       string       `json:"title"`
	Content     string       `json:"content"`
	Sensitivity Sensitivity  `json:"sensitivity"`
	Confidence  float64      `json:"confidence"`
}

type QueryResponse struct {
	ResponseID    string          `json:"response_id"`
	QueryID       string          `json:"query_id"`
	FromAgentID   string          `json:"from_agent_id"`
	ToAgentID     string          `json:"to_agent_id"`
	Artifacts     []QueryArtifact `json:"artifacts"`
	Redactions    []string        `json:"redactions"`
	PolicyBasis   []string        `json:"policy_basis"`
	ApprovalState ApprovalState   `json:"approval_state"`
	Confidence    float64         `json:"confidence"`
	CreatedAt     time.Time       `json:"created_at"`
}

type Request struct {
	RequestID         string         `json:"request_id"`
	OrgID             string         `json:"org_id"`
	FromAgentID       string         `json:"from_agent_id"`
	FromUserID        string         `json:"from_user_id"`
	ToAgentID         string         `json:"to_agent_id"`
	ToUserID          string         `json:"to_user_id"`
	RequestType       string         `json:"request_type"`
	Title             string         `json:"title"`
	Content           string         `json:"content"`
	StructuredPayload map[string]any `json:"structured_payload,omitempty"`
	RiskLevel         RiskLevel      `json:"risk_level"`
	State             RequestState   `json:"state"`
	ApprovalState     ApprovalState  `json:"approval_state"`
	ResponseMessage   string         `json:"response_message,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	ExpiresAt         time.Time      `json:"expires_at"`
}

type Approval struct {
	ApprovalID  string        `json:"approval_id"`
	OrgID       string        `json:"org_id"`
	AgentID     string        `json:"agent_id"`
	OwnerUserID string        `json:"owner_user_id"`
	SubjectType string        `json:"subject_type"`
	SubjectID   string        `json:"subject_id"`
	Reason      string        `json:"reason"`
	State       ApprovalState `json:"state"`
	CreatedAt   time.Time     `json:"created_at"`
	ExpiresAt   time.Time     `json:"expires_at"`
	ResolvedAt  *time.Time    `json:"resolved_at,omitempty"`
}

type PolicyGrant struct {
	PolicyGrantID             string         `json:"policy_grant_id"`
	OrgID                     string         `json:"org_id"`
	GrantorUserID             string         `json:"grantor_user_id"`
	GranteeUserID             string         `json:"grantee_user_id"`
	ScopeType                 string         `json:"scope_type"`
	ScopeRef                  string         `json:"scope_ref"`
	AllowedArtifactTypes      []ArtifactType `json:"allowed_artifact_types"`
	MaxSensitivity            Sensitivity    `json:"max_sensitivity"`
	AllowedPurposes           []QueryPurpose `json:"allowed_purposes"`
	VisibilityMode            VisibilityMode `json:"visibility_mode"`
	RequiresApprovalAboveRisk RiskLevel      `json:"requires_approval_above_risk"`
	CreatedAt                 time.Time      `json:"created_at"`
	ExpiresAt                 *time.Time     `json:"expires_at,omitempty"`
	RevokedAt                 *time.Time     `json:"revoked_at,omitempty"`
}

type AuditEvent struct {
	AuditEventID  string         `json:"audit_event_id"`
	OrgID         string         `json:"org_id"`
	EventKind     string         `json:"event_kind"`
	ActorAgentID  string         `json:"actor_agent_id,omitempty"`
	TargetAgentID string         `json:"target_agent_id,omitempty"`
	SubjectType   string         `json:"subject_type"`
	SubjectID     string         `json:"subject_id"`
	PolicyBasis   []string       `json:"policy_basis,omitempty"`
	Decision      string         `json:"decision"`
	RiskLevel     RiskLevel      `json:"risk_level,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
