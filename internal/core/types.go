package core

import (
	"strings"
	"time"
)

// Agent status values.
const (
	AgentStatusActive                  = "active"
	AgentStatusPendingEmailVerification = "pending_email_verification"
	AgentStatusPendingAdminApproval    = "pending_admin_approval"
	AgentStatusRejected                = "rejected"
)

// User role values.
const (
	UserRoleMember = "member"
	UserRoleAdmin  = "admin"
)

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
	RequestStatePending      RequestState = "pending"
	RequestStateAccepted     RequestState = "accepted"
	RequestStateDeferred     RequestState = "deferred"
	RequestStateDenied       RequestState = "denied"
	RequestStateCompleted    RequestState = "completed"
	RequestStateExpired      RequestState = "expired"
	// RequestStateAutoAnswered indicates the recipient's agent answered the
	// request from existing derived artifacts without interrupting the human.
	// The sender can treat this as a low-confidence Reporter-style answer and
	// follow up if they need more.
	RequestStateAutoAnswered RequestState = "auto_answered"
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
	OrgID            string    `json:"org_id"`
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	CreatedAt        time.Time `json:"created_at"`
	Status           string    `json:"status"`
	VerificationMode string    `json:"verification_mode"`
	InviteTokenHash  string    `json:"-"` // SHA-256 hex of the raw token; never exposed in JSON

	// GatekeeperConfidenceThreshold, when non-nil, overrides the server-wide
	// ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD for this org. nil means fall
	// through to env, then to the compile-time default.
	GatekeeperConfidenceThreshold *float64 `json:"gatekeeper_confidence_threshold,omitempty"`
	// GatekeeperLookbackWindow, when non-nil, overrides the server-wide
	// ALICE_GATEKEEPER_LOOKBACK_WINDOW for this org. nil means fall through
	// to env, then to the compile-time default.
	GatekeeperLookbackWindow *time.Duration `json:"-"`
}

// OrgRequiresInviteToken returns true when the org's verification mode includes "invite_token".
func OrgRequiresInviteToken(mode string) bool {
	return strings.Contains(mode, "invite_token")
}

// OrgRequiresAdminApproval returns true when the org's verification mode includes "admin_approval".
func OrgRequiresAdminApproval(mode string) bool {
	return strings.Contains(mode, "admin_approval")
}

type User struct {
	UserID          string    `json:"user_id"`
	OrgID           string    `json:"org_id"`
	Email           string    `json:"email"`
	DisplayName     string    `json:"display_name"`
	RoleTitles      []string  `json:"role_titles"`
	ManagerUserID   string    `json:"manager_user_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	Status          string    `json:"status"`
	Role            string    `json:"role"` // "member" or "admin"
	OperatorEnabled bool      `json:"operator_enabled"`
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
	ArtifactID  string            `json:"artifact_id"`
	Type        ArtifactType      `json:"type"`
	Title       string            `json:"title"`
	Content     string            `json:"content"`
	Sensitivity Sensitivity       `json:"sensitivity"`
	Confidence  float64           `json:"confidence"`
	CreatedAt   time.Time         `json:"created_at"`
	ObservedAt  time.Time         `json:"observed_at,omitempty"`
	SourceRefs  []SourceReference `json:"source_refs,omitempty"`
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

type AgentApproval struct {
	ApprovalID  string     `json:"approval_id"`
	AgentID     string     `json:"agent_id"`
	OrgID       string     `json:"org_id"`
	RequestedAt time.Time  `json:"requested_at"`
	ReviewedBy  string     `json:"reviewed_by,omitempty"` // user ID
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
	Decision    string     `json:"decision,omitempty"` // "approved" or "rejected"
	Reason      string     `json:"reason,omitempty"`
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

// EmailVerification holds the state for a pending email OTP check.
type EmailVerification struct {
	VerificationID string     `json:"verification_id"`
	AgentID        string     `json:"agent_id"`
	OrgID          string     `json:"org_id"`
	Email          string     `json:"email"`
	CodeHash       string     `json:"-"` // SHA-256 of the 6-digit code; never exposed in JSON
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	Attempts       int        `json:"attempts"`
}

// ActionKind enumerates the operator-phase side effects an agent may
// execute on the user's behalf. Each kind is implemented by exactly one
// Executor — the kind string is the routing key from request to executor.
type ActionKind string

const (
	// ActionKindAcknowledgeBlocker completes a "blocker" style request by
	// updating the linked request's response_message. Purely internal —
	// no external side effect.
	ActionKindAcknowledgeBlocker ActionKind = "acknowledge_blocker"
	// ActionKindAcceptMeeting is reserved for calendar-writing executors
	// that will land in a later change.
	ActionKindAcceptMeeting ActionKind = "accept_meeting"
	// ActionKindSendReply is reserved for reply-writing executors that
	// will land in a later change.
	ActionKindSendReply ActionKind = "send_reply"
)

// ActionState is the lifecycle of a single Action.
type ActionState string

const (
	ActionStatePending   ActionState = "pending"
	ActionStateApproved  ActionState = "approved"
	ActionStateExecuting ActionState = "executing"
	ActionStateExecuted  ActionState = "executed"
	ActionStateFailed    ActionState = "failed"
	ActionStateCancelled ActionState = "cancelled"
	ActionStateExpired   ActionState = "expired"
)

// Action is one side-effectful unit of work the user's agent may execute
// on the user's behalf. Inputs are the parameters the executor needs;
// Result is the opaque output the executor records when it finishes.
// Actions are execute-once: the storage layer rejects transitions away
// from terminal states, so replays are no-ops.
type Action struct {
	ActionID      string         `json:"action_id"`
	OrgID         string         `json:"org_id"`
	RequestID     string         `json:"request_id,omitempty"`
	OwnerUserID   string         `json:"owner_user_id"`
	OwnerAgentID  string         `json:"owner_agent_id"`
	Kind          ActionKind     `json:"kind"`
	Inputs        map[string]any `json:"inputs,omitempty"`
	RiskLevel     RiskLevel      `json:"risk_level"`
	State         ActionState    `json:"state"`
	Result        map[string]any `json:"result,omitempty"`
	FailureReason string         `json:"failure_reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	ExpiresAt     time.Time      `json:"expires_at"`
	ExecutedAt    *time.Time     `json:"executed_at,omitempty"`
}

// RiskPolicy is one versioned evaluable policy belonging to an org. A policy
// records its Source (the raw JSON document applied by an admin) verbatim so
// audits can show exactly what was evaluated. Only one policy per org may
// have a non-nil ActiveAt at any time; rollback works by activating a prior
// Version.
type RiskPolicy struct {
	PolicyID        string     `json:"policy_id"`
	OrgID           string     `json:"org_id"`
	Name            string     `json:"name"`
	Version         int        `json:"version"`
	Source          string     `json:"source"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedByUserID string     `json:"created_by_user_id,omitempty"`
	ActiveAt        *time.Time `json:"active_at,omitempty"`
}

// RiskDecisionAction is the outcome of evaluating a risk policy against an
// input. The set is intentionally tiny — every consumer knows how to react
// to each action — and new actions are added only when a concrete consumer
// needs them.
type RiskDecisionAction string

const (
	RiskDecisionAllow           RiskDecisionAction = "allow"
	RiskDecisionRequireApproval RiskDecisionAction = "require_approval"
	RiskDecisionDeny            RiskDecisionAction = "deny"
)

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
