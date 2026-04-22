package riskpolicy_test

import (
	"strings"
	"testing"

	"alice/internal/core"
	"alice/internal/riskpolicy"
)

func TestParseRejectsEmptyRules(t *testing.T) {
	if _, err := riskpolicy.Parse([]byte(`{"rules": []}`)); err == nil {
		t.Fatal("expected error for zero-rule policy")
	}
}

func TestParseRejectsUnknownRiskLevel(t *testing.T) {
	src := []byte(`{"rules":[{"when":{"risk_level_at_least":"L9"},"then":"allow"}]}`)
	_, err := riskpolicy.Parse(src)
	if err == nil || !strings.Contains(err.Error(), "L9") {
		t.Fatalf("expected risk-level validation error, got %v", err)
	}
}

func TestParseRejectsUnknownAction(t *testing.T) {
	src := []byte(`{"rules":[{"when":{},"then":"maybe"}]}`)
	if _, err := riskpolicy.Parse(src); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestEvaluateFirstMatchWins(t *testing.T) {
	policy, err := riskpolicy.Parse([]byte(`{
		"rules": [
			{"when": {"purpose": "status_check"}, "then": "allow", "reason": "status always ok"},
			{"when": {"risk_level_at_least": "L3"}, "then": "require_approval"},
			{"when": {}, "then": "deny"}
		]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cases := []struct {
		name     string
		inputs   riskpolicy.Inputs
		want     core.RiskDecisionAction
		wantWord string
	}{
		{"status matches first rule", riskpolicy.Inputs{Purpose: core.QueryPurposeStatusCheck, RiskLevel: core.RiskLevelL4}, core.RiskDecisionAllow, "status always ok"},
		{"high risk triggers approval", riskpolicy.Inputs{Purpose: core.QueryPurposeRequestContext, RiskLevel: core.RiskLevelL3}, core.RiskDecisionRequireApproval, "rule 1 matched"},
		{"fallthrough denies", riskpolicy.Inputs{Purpose: core.QueryPurposeRequestContext, RiskLevel: core.RiskLevelL0}, core.RiskDecisionDeny, "rule 2 matched"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := riskpolicy.Evaluate(policy, tc.inputs)
			if d.Action != tc.want {
				t.Fatalf("action=%v, want %v", d.Action, tc.want)
			}
			if len(d.Reasons) == 0 || !strings.Contains(d.Reasons[0], tc.wantWord) {
				t.Fatalf("reasons=%v, expected mention of %q", d.Reasons, tc.wantWord)
			}
		})
	}
}

func TestEvaluateSensitivityAndRequestTypeIn(t *testing.T) {
	policy, err := riskpolicy.Parse([]byte(`{
		"rules": [
			{"when": {"request_type_in": ["question","status_check"], "sensitivity_at_least": "high"}, "then": "require_approval"},
			{"when": {}, "then": "allow"}
		]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	d := riskpolicy.Evaluate(policy, riskpolicy.Inputs{RequestType: "question", Sensitivity: core.SensitivityHigh})
	if d.Action != core.RiskDecisionRequireApproval {
		t.Fatalf("want require_approval, got %v", d.Action)
	}

	d = riskpolicy.Evaluate(policy, riskpolicy.Inputs{RequestType: "question", Sensitivity: core.SensitivityLow})
	if d.Action != core.RiskDecisionAllow {
		t.Fatalf("want allow for low sensitivity, got %v", d.Action)
	}

	d = riskpolicy.Evaluate(policy, riskpolicy.Inputs{RequestType: "ask_for_time", Sensitivity: core.SensitivityHigh})
	if d.Action != core.RiskDecisionAllow {
		t.Fatalf("want allow when request_type not in list, got %v", d.Action)
	}
}

func TestEvaluateDefaultPolicyAllows(t *testing.T) {
	d := riskpolicy.Evaluate(riskpolicy.DefaultPolicy, riskpolicy.Inputs{
		Purpose:     core.QueryPurposeStatusCheck,
		RiskLevel:   core.RiskLevelL4,
		Sensitivity: core.SensitivityRestricted,
	})
	if d.Action != core.RiskDecisionAllow {
		t.Fatalf("default policy should allow, got %v", d.Action)
	}
}
