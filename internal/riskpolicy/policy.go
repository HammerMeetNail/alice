// Package riskpolicy implements the coordination server's risk-policy
// evaluator: a tiny, deterministic, JSON-driven rules engine that decides
// whether an incoming query / request should be allowed, require approval,
// or be denied.
//
// The DSL is intentionally narrow: a policy is a list of rules; each rule
// has a `when` matcher (all conditions AND together) and a `then` action.
// The first matching rule wins. Unmatched input falls through to the
// built-in default, which mirrors the legacy `Grant.RequiresApprovalAboveRisk`
// ladder so existing deployments continue to behave unchanged until an admin
// applies a custom policy.
//
// Fails closed: any parse or evaluation error translates to
// RiskDecisionDeny with a reason describing why, so an admin pushing a bad
// policy never accidentally loosens enforcement.
package riskpolicy

import (
	"encoding/json"
	"fmt"
	"strings"

	"alice/internal/core"
)

// Rule is one policy rule: all conditions in When must match, at which
// point the rule's Then action applies. Every field in When is optional;
// an empty When matches every input (use this as the final catch-all).
type Rule struct {
	When   Matcher                 `json:"when"`
	Then   core.RiskDecisionAction `json:"then"`
	Reason string                  `json:"reason,omitempty"`
}

// Matcher is the AND-combined set of conditions under a rule's When key.
// Fields left at zero value are "not checked".
type Matcher struct {
	// Purpose, when non-empty, requires an exact match on the query's
	// purpose string (e.g. "status_check", "request_context").
	Purpose string `json:"purpose,omitempty"`
	// PurposeIn, when non-empty, requires the purpose to be one of the
	// listed values. Mutually cooperative with Purpose — both are AND'd.
	PurposeIn []string `json:"purpose_in,omitempty"`
	// RequestType exact match on the request kind (e.g. "question",
	// "ask_for_time"). Ignored for query-driven inputs that carry no
	// request type.
	RequestType string `json:"request_type,omitempty"`
	// RequestTypeIn is the any-of equivalent of RequestType.
	RequestTypeIn []string `json:"request_type_in,omitempty"`
	// RiskLevelAtLeast matches inputs whose risk level is at or above the
	// given level (L0 < L1 < L2 < L3 < L4). An empty value is ignored.
	RiskLevelAtLeast core.RiskLevel `json:"risk_level_at_least,omitempty"`
	// SensitivityAtLeast matches inputs whose sensitivity is at or above
	// the given level (low < medium < high < restricted). An empty value
	// is ignored.
	SensitivityAtLeast core.Sensitivity `json:"sensitivity_at_least,omitempty"`
	// ScopeType, when non-empty, requires an exact match on the grant's
	// scope type.
	ScopeType string `json:"scope_type,omitempty"`
}

// Policy is the top-level document an admin applies.
type Policy struct {
	// Version is informational; the active-record's version column is
	// what authoritative code uses for audit.
	Version int `json:"version,omitempty"`
	// Name is optional but recommended.
	Name string `json:"name,omitempty"`
	// Rules are evaluated top to bottom; first match wins.
	Rules []Rule `json:"rules"`
}

// Inputs are the values the evaluator matches against rule conditions.
// Callers populate only the fields their context knows about; the evaluator
// treats missing fields as "no value" so rules that require them do not
// match.
type Inputs struct {
	Purpose     core.QueryPurpose
	RequestType string
	RiskLevel   core.RiskLevel
	Sensitivity core.Sensitivity
	ScopeType   string
}

// Decision is the evaluator's verdict. PolicyID and Version identify which
// policy produced it so the caller can pin it in the audit record.
type Decision struct {
	Action        core.RiskDecisionAction
	Reasons       []string
	PolicyID      string
	PolicyVersion int
}

// Parse validates and decodes a raw policy document. A policy with no
// rules is rejected — even an all-allow policy must say so explicitly, so
// there is no ambiguity between "admin forgot to write rules" and
// "everything allowed".
func Parse(source []byte) (Policy, error) {
	var policy Policy
	decoder := json.NewDecoder(strings.NewReader(string(source)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	if len(policy.Rules) == 0 {
		return Policy{}, fmt.Errorf("policy must declare at least one rule")
	}
	for i, rule := range policy.Rules {
		if err := validateAction(rule.Then); err != nil {
			return Policy{}, fmt.Errorf("rule %d: %w", i, err)
		}
		if rule.When.RiskLevelAtLeast != "" {
			if _, ok := riskRank(rule.When.RiskLevelAtLeast); !ok {
				return Policy{}, fmt.Errorf("rule %d: risk_level_at_least=%q is not a known risk level", i, rule.When.RiskLevelAtLeast)
			}
		}
		if rule.When.SensitivityAtLeast != "" {
			if _, ok := sensitivityRank(rule.When.SensitivityAtLeast); !ok {
				return Policy{}, fmt.Errorf("rule %d: sensitivity_at_least=%q is not a known sensitivity", i, rule.When.SensitivityAtLeast)
			}
		}
	}
	return policy, nil
}

func validateAction(action core.RiskDecisionAction) error {
	switch action {
	case core.RiskDecisionAllow, core.RiskDecisionRequireApproval, core.RiskDecisionDeny:
		return nil
	default:
		return fmt.Errorf("then=%q must be one of allow|require_approval|deny", action)
	}
}

// Evaluate walks the policy's rules in order and returns the first match's
// action. An input that matches no rule returns RiskDecisionAllow — the
// catch-all rule is the caller's responsibility, consistent with how the
// legacy ladder behaves (no constraint ⇒ allow).
func Evaluate(policy Policy, inputs Inputs) Decision {
	for i, rule := range policy.Rules {
		if matches(rule.When, inputs) {
			reasons := make([]string, 0, 1)
			if rule.Reason != "" {
				reasons = append(reasons, rule.Reason)
			} else {
				reasons = append(reasons, fmt.Sprintf("rule %d matched", i))
			}
			return Decision{
				Action:        rule.Then,
				Reasons:       reasons,
				PolicyVersion: policy.Version,
			}
		}
	}
	return Decision{
		Action:        core.RiskDecisionAllow,
		Reasons:       []string{"no rule matched — default allow"},
		PolicyVersion: policy.Version,
	}
}

func matches(m Matcher, in Inputs) bool {
	if m.Purpose != "" && !strings.EqualFold(m.Purpose, string(in.Purpose)) {
		return false
	}
	if len(m.PurposeIn) > 0 && !containsFold(m.PurposeIn, string(in.Purpose)) {
		return false
	}
	if m.RequestType != "" && !strings.EqualFold(m.RequestType, in.RequestType) {
		return false
	}
	if len(m.RequestTypeIn) > 0 && !containsFold(m.RequestTypeIn, in.RequestType) {
		return false
	}
	if m.RiskLevelAtLeast != "" {
		want, _ := riskRank(m.RiskLevelAtLeast)
		got, ok := riskRank(in.RiskLevel)
		if !ok || got < want {
			return false
		}
	}
	if m.SensitivityAtLeast != "" {
		want, _ := sensitivityRank(m.SensitivityAtLeast)
		got, ok := sensitivityRank(in.Sensitivity)
		if !ok || got < want {
			return false
		}
	}
	if m.ScopeType != "" && !strings.EqualFold(m.ScopeType, in.ScopeType) {
		return false
	}
	return true
}

func containsFold(values []string, needle string) bool {
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func riskRank(level core.RiskLevel) (int, bool) {
	switch level {
	case core.RiskLevelL0:
		return 0, true
	case core.RiskLevelL1:
		return 1, true
	case core.RiskLevelL2:
		return 2, true
	case core.RiskLevelL3:
		return 3, true
	case core.RiskLevelL4:
		return 4, true
	default:
		return 0, false
	}
}

func sensitivityRank(s core.Sensitivity) (int, bool) {
	switch s {
	case core.SensitivityLow:
		return 1, true
	case core.SensitivityMedium:
		return 2, true
	case core.SensitivityHigh:
		return 3, true
	case core.SensitivityRestricted:
		return 4, true
	default:
		return 0, false
	}
}

// DefaultPolicy is the built-in policy used when an org has not applied a
// custom one. It matches the legacy behaviour: allow everything, leaving
// the downstream grant-level `requires_approval_above_risk` check to gate
// approvals. Callers that invoke Evaluate with this policy get an allow
// verdict with no extra reasons, which is the signal to fall through to
// the existing ladder.
var DefaultPolicy = Policy{
	Name: "builtin-default",
	Rules: []Rule{
		{When: Matcher{}, Then: core.RiskDecisionAllow, Reason: "default policy"},
	},
}
