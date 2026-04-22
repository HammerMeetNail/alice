// Package gatekeeper implements the agent's automatic answering path for
// incoming requests. When a sender asks the recipient a question, the
// gatekeeper synthesises an equivalent permission-checked query against the
// recipient's derived artifacts and — if the answer is confident enough —
// responds on the recipient's behalf without interrupting the human.
//
// This is the feature that earns the name "alice": the agent actually gates
// interruptions instead of just relaying them. A missing permission, low
// confidence, or no matching artifact all fall through to the existing
// human-in-the-loop defer path.
package gatekeeper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/queries"
)

// DefaultConfidenceThreshold is the minimum aggregate confidence required to
// auto-answer a request. Anything below this falls through to the human.
const DefaultConfidenceThreshold = 0.6

// DefaultLookbackWindow is the default artifact-recency window used when the
// synthesised query asks "what has the recipient been doing lately?".
const DefaultLookbackWindow = 14 * 24 * time.Hour

// OrgLookup is the minimal slice of storage.OrganizationRepository the
// gatekeeper needs to pick up per-org overrides on the request path. Passing
// nil disables per-org lookups; the server-wide options then apply uniformly.
type OrgLookup interface {
	FindOrganizationByID(ctx context.Context, orgID string) (core.Organization, bool, error)
}

// Service evaluates incoming requests and decides whether they can be
// answered from the recipient's already-published artifacts.
type Service struct {
	queries             *queries.Service
	orgs                OrgLookup
	confidenceThreshold float64
	lookbackWindow      time.Duration
}

// Options configures a Service.
type Options struct {
	ConfidenceThreshold float64
	LookbackWindow      time.Duration
}

// NewService constructs a gatekeeper that evaluates against the given queries
// service. Pass zero-valued Options to accept defaults.
func NewService(q *queries.Service, opts Options) *Service {
	threshold := opts.ConfidenceThreshold
	if threshold <= 0 {
		threshold = DefaultConfidenceThreshold
	}
	window := opts.LookbackWindow
	if window <= 0 {
		window = DefaultLookbackWindow
	}
	return &Service{
		queries:             q,
		confidenceThreshold: threshold,
		lookbackWindow:      window,
	}
}

// WithOrgLookup attaches an org repository so Evaluate can pick up per-org
// overrides for the confidence threshold and lookback window. Returns the
// receiver for fluent wiring.
func (s *Service) WithOrgLookup(orgs OrgLookup) *Service {
	if s != nil {
		s.orgs = orgs
	}
	return s
}

// AutoAnswer is the gatekeeper's verdict on a single request.
type AutoAnswer struct {
	// Answered reports whether the gatekeeper produced a confident reply.
	Answered bool
	// Summary is a short human-readable response suitable for the request's
	// response_message field. Empty when Answered is false.
	Summary string
	// ArtifactIDs are the derived artifacts that support the answer.
	ArtifactIDs []string
	// Confidence is the aggregate confidence across the supporting artifacts.
	Confidence float64
	// Reason, when Answered is false, describes why the gatekeeper deferred
	// (no grant, no matching artifact, low confidence). Useful for audit.
	Reason string
}

// Evaluate synthesises a query from the given request and asks the queries
// service to answer it. When the answer is confident enough, Evaluate
// returns an AutoAnswer with Answered=true. Any policy, storage, or
// permission error results in Answered=false with Reason populated.
func (s *Service) Evaluate(ctx context.Context, request core.Request) AutoAnswer {
	if s == nil || s.queries == nil {
		return AutoAnswer{Reason: "gatekeeper disabled"}
	}
	if !isAnswerableRequestType(request.RequestType) {
		return AutoAnswer{Reason: "request type not eligible for auto-answer"}
	}

	question := strings.TrimSpace(request.Title)
	if body := strings.TrimSpace(request.Content); body != "" {
		if question != "" {
			question = question + " — " + body
		} else {
			question = body
		}
	}
	if question == "" {
		return AutoAnswer{Reason: "empty question"}
	}

	threshold, window := s.resolveTuning(ctx, request.OrgID)

	now := time.Now().UTC()
	baseQuery := core.Query{
		QueryID:     id.New("query"),
		OrgID:       request.OrgID,
		FromAgentID: request.FromAgentID,
		FromUserID:  request.FromUserID,
		ToAgentID:   request.ToAgentID,
		ToUserID:    request.ToUserID,
		Question:    question,
		RequestedTypes: []core.ArtifactType{
			core.ArtifactTypeSummary,
			core.ArtifactTypeStatusDelta,
			core.ArtifactTypeBlocker,
			core.ArtifactTypeCommitment,
		},
		TimeWindow: core.TimeWindow{
			Start: now.Add(-window),
			End:   now,
		},
		RiskLevel: core.RiskLevelL0,
		State:     core.QueryStateQueued,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}

	// Try each candidate purpose in order, accepting the first that produces
	// any artifacts. Senders don't necessarily grant every purpose the
	// gatekeeper might synthesise; trying alternates keeps the common case
	// (one-purpose grant like `status_check`) working without forcing the
	// recipient to also grant `request_context`.
	var (
		response      core.QueryResponse
		haveResponse  bool
		sawDenied     bool
	)
	purposes := purposesForRequestType(request.RequestType)
	if len(purposes) == 0 {
		return AutoAnswer{Reason: "no eligible purpose for request type"}
	}
	for _, purpose := range purposes {
		q := baseQuery
		q.Purpose = purpose
		resp, err := s.queries.Evaluate(ctx, q)
		if err != nil {
			if errors.Is(err, queries.ErrPermissionDenied) {
				sawDenied = true
				continue
			}
			return AutoAnswer{Reason: "query evaluation failed: " + err.Error()}
		}
		if len(resp.Artifacts) > 0 {
			response = resp
			haveResponse = true
			break
		}
		// Remember the first empty response so we can still return a useful
		// reason if every purpose produces zero artifacts.
		if !haveResponse {
			response = resp
			haveResponse = true
		}
	}
	if !haveResponse {
		if sawDenied {
			return AutoAnswer{Reason: "no active grant from recipient to sender"}
		}
		return AutoAnswer{Reason: "query evaluation produced no response"}
	}
	if len(response.Artifacts) == 0 {
		return AutoAnswer{Reason: "no matching artifacts in lookback window"}
	}
	if response.Confidence < threshold {
		return AutoAnswer{
			Answered:    false,
			Confidence:  response.Confidence,
			ArtifactIDs: artifactIDs(response.Artifacts),
			Reason: fmt.Sprintf("aggregate confidence %.2f below threshold %.2f",
				response.Confidence, threshold),
		}
	}

	return AutoAnswer{
		Answered:    true,
		Summary:     summarise(response.Artifacts, response.Confidence),
		ArtifactIDs: artifactIDs(response.Artifacts),
		Confidence:  response.Confidence,
	}
}

// resolveTuning merges per-org overrides (if any) on top of the Service's
// server-wide defaults. When the org lookup is unconfigured or returns an
// error we log at debug level and fall back to the wider defaults — a broken
// lookup must never stop the gatekeeper from running entirely.
func (s *Service) resolveTuning(ctx context.Context, orgID string) (threshold float64, window time.Duration) {
	threshold = s.confidenceThreshold
	window = s.lookbackWindow
	if s.orgs == nil || strings.TrimSpace(orgID) == "" {
		return threshold, window
	}
	org, ok, err := s.orgs.FindOrganizationByID(ctx, orgID)
	if err != nil || !ok {
		return threshold, window
	}
	if org.GatekeeperConfidenceThreshold != nil {
		v := *org.GatekeeperConfidenceThreshold
		if v > 0 && v <= 1 {
			threshold = v
		}
	}
	if org.GatekeeperLookbackWindow != nil {
		d := *org.GatekeeperLookbackWindow
		if d > 0 {
			window = d
		}
	}
	return threshold, window
}

// purposesForRequestType returns the QueryPurposes to try when synthesising
// a permission-checked query for an incoming request. The gatekeeper tries
// them in order and uses the first that the recipient's grants allow. This
// keeps the mental model simple: a "question about status" matches a
// `status_check` grant, without forcing senders to also grant
// `request_context`.
func purposesForRequestType(requestType string) []core.QueryPurpose {
	switch strings.TrimSpace(strings.ToLower(requestType)) {
	case "status_check", "status":
		return []core.QueryPurpose{core.QueryPurposeStatusCheck, core.QueryPurposeRequestContext}
	case "question", "context", "info":
		return []core.QueryPurpose{core.QueryPurposeRequestContext, core.QueryPurposeStatusCheck}
	default:
		return nil
	}
}

// isAnswerableRequestType reports whether a request kind is eligible for
// automatic Reporter-style answering. Action-like requests (`ask_for_time`,
// `review`, `approve`) must always reach the human — the agent cannot speak
// for the user on those. Informational requests can be auto-answered.
func isAnswerableRequestType(requestType string) bool {
	switch strings.TrimSpace(strings.ToLower(requestType)) {
	case "question", "status_check", "context", "info", "status":
		return true
	default:
		return false
	}
}

func artifactIDs(artifacts []core.QueryArtifact) []string {
	out := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		if a.ArtifactID != "" {
			out = append(out, a.ArtifactID)
		}
	}
	return out
}

// summarise produces a short Reporter-style response message. It intentionally
// quotes the recipient's own artifacts verbatim rather than rewriting them,
// so sensitive content doesn't leak through paraphrasing and the sender sees
// exactly what was shared.
func summarise(artifacts []core.QueryArtifact, confidence float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Auto-answered from %d derived artifact(s), aggregate confidence %.2f.\n",
		len(artifacts), confidence)
	for _, a := range artifacts {
		fmt.Fprintf(&b, "\n• %s — %s (confidence %.2f)\n", a.Type, a.Title, a.Confidence)
		if a.Content != "" {
			fmt.Fprintf(&b, "  %s\n", a.Content)
		}
		if !a.ObservedAt.IsZero() {
			fmt.Fprintf(&b, "  (observed %s)\n", a.ObservedAt.Format(time.RFC3339))
		}
	}
	b.WriteString("\nFollow up if you need more detail.")
	return b.String()
}
