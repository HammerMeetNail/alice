package core_test

import (
	"testing"
	"time"

	"alice/internal/core"
)

func TestValidationError_Message(t *testing.T) {
	err := core.ValidationError{Message: "field is required"}
	if err.Error() != "field is required" {
		t.Fatalf("got %q want %q", err.Error(), "field is required")
	}
	if !core.IsValidationError(err) {
		t.Fatal("IsValidationError should return true")
	}
}

func TestForbiddenError_Message(t *testing.T) {
	err := core.ForbiddenError{Message: "access denied"}
	if err.Error() != "access denied" {
		t.Fatalf("got %q want %q", err.Error(), "access denied")
	}
	if !core.IsForbiddenError(err) {
		t.Fatal("IsForbiddenError should return true")
	}
}

func TestSensitivityAllowed(t *testing.T) {
	cases := []struct {
		actual  core.Sensitivity
		ceiling core.Sensitivity
		allowed bool
	}{
		{core.SensitivityLow, core.SensitivityHigh, true},
		{core.SensitivityLow, core.SensitivityLow, true},
		{core.SensitivityHigh, core.SensitivityMedium, false},
		{core.SensitivityRestricted, core.SensitivityMedium, false},
		{core.SensitivityMedium, core.SensitivityRestricted, true},
	}
	for _, c := range cases {
		got := core.SensitivityAllowed(c.actual, c.ceiling)
		if got != c.allowed {
			t.Errorf("SensitivityAllowed(%s, %s) = %v, want %v", c.actual, c.ceiling, got, c.allowed)
		}
	}
}

func TestRiskLevelExceeds(t *testing.T) {
	cases := []struct {
		actual    core.RiskLevel
		threshold core.RiskLevel
		exceeds   bool
	}{
		{core.RiskLevelL0, core.RiskLevelL1, false},
		{core.RiskLevelL2, core.RiskLevelL1, true},
		{core.RiskLevelL1, core.RiskLevelL1, false},
		{core.RiskLevelL4, core.RiskLevelL0, true},
		{core.RiskLevelL0, "", false}, // empty threshold: never requires approval
	}
	for _, c := range cases {
		got := core.RiskLevelExceeds(c.actual, c.threshold)
		if got != c.exceeds {
			t.Errorf("RiskLevelExceeds(%s, %s) = %v, want %v", c.actual, c.threshold, got, c.exceeds)
		}
	}
}

func TestValidateArtifactInput(t *testing.T) {
	validRef := core.SourceReference{SourceSystem: "github", SourceType: "pr", SourceID: "123"}
	valid := core.Artifact{
		Type:           core.ArtifactTypeSummary,
		Title:          "Weekly summary",
		Content:        "All good",
		Confidence:     0.9,
		Sensitivity:    core.SensitivityLow,
		VisibilityMode: core.VisibilityModeExplicitGrantsOnly,
		SourceRefs:     []core.SourceReference{validRef},
	}

	if err := core.ValidateArtifactInput(valid); err != nil {
		t.Fatalf("valid artifact failed: %v", err)
	}

	t.Run("invalid type", func(t *testing.T) {
		a := valid
		a.Type = "bogus"
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("missing title", func(t *testing.T) {
		a := valid
		a.Title = "  "
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("missing content", func(t *testing.T) {
		a := valid
		a.Content = ""
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("confidence out of range", func(t *testing.T) {
		a := valid
		a.Confidence = 1.5
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid sensitivity", func(t *testing.T) {
		a := valid
		a.Sensitivity = "ultra"
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("no source refs", func(t *testing.T) {
		a := valid
		a.SourceRefs = nil
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("incomplete source ref", func(t *testing.T) {
		a := valid
		a.SourceRefs = []core.SourceReference{{SourceSystem: "github", SourceType: "pr"}} // missing SourceID
		if err := core.ValidateArtifactInput(a); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})
}

func TestValidateGrantInput(t *testing.T) {
	valid := func() (string, string, string, []core.ArtifactType, core.Sensitivity, []core.QueryPurpose) {
		return "grantee@example.com", "project", "myproject",
			[]core.ArtifactType{core.ArtifactTypeSummary},
			core.SensitivityLow,
			[]core.QueryPurpose{core.QueryPurposeStatusCheck}
	}

	email, scopeType, scopeRef, types, sens, purposes := valid()
	if err := core.ValidateGrantInput(email, scopeType, scopeRef, types, sens, purposes); err != nil {
		t.Fatalf("valid grant input failed: %v", err)
	}

	t.Run("missing email", func(t *testing.T) {
		_, s, sr, ty, se, p := valid()
		if err := core.ValidateGrantInput("", s, sr, ty, se, p); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("missing scope_type", func(t *testing.T) {
		e, _, sr, ty, se, p := valid()
		if err := core.ValidateGrantInput(e, "", sr, ty, se, p); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("missing scope_ref", func(t *testing.T) {
		e, s, _, ty, se, p := valid()
		if err := core.ValidateGrantInput(e, s, "", ty, se, p); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("no artifact types", func(t *testing.T) {
		e, s, sr, _, se, p := valid()
		if err := core.ValidateGrantInput(e, s, sr, nil, se, p); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid artifact type", func(t *testing.T) {
		e, s, sr, _, se, p := valid()
		if err := core.ValidateGrantInput(e, s, sr, []core.ArtifactType{"bogus"}, se, p); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid sensitivity", func(t *testing.T) {
		e, s, sr, ty, _, p := valid()
		if err := core.ValidateGrantInput(e, s, sr, ty, "ultra", p); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid purpose", func(t *testing.T) {
		e, s, sr, ty, se, _ := valid()
		if err := core.ValidateGrantInput(e, s, sr, ty, se, []core.QueryPurpose{"gossip"}); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})
}

func TestValidateQueryInput(t *testing.T) {
	now := time.Now()
	window := core.TimeWindow{Start: now.Add(-time.Hour), End: now}

	if err := core.ValidateQueryInput("peer@example.com", core.QueryPurposeStatusCheck,
		[]core.ArtifactType{core.ArtifactTypeSummary}, window); err != nil {
		t.Fatalf("valid query input failed: %v", err)
	}

	t.Run("missing email", func(t *testing.T) {
		if err := core.ValidateQueryInput("", core.QueryPurposeStatusCheck,
			[]core.ArtifactType{core.ArtifactTypeSummary}, window); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid purpose", func(t *testing.T) {
		if err := core.ValidateQueryInput("peer@example.com", "gossip",
			[]core.ArtifactType{core.ArtifactTypeSummary}, window); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("end before start", func(t *testing.T) {
		bad := core.TimeWindow{Start: now, End: now.Add(-time.Hour)}
		if err := core.ValidateQueryInput("peer@example.com", core.QueryPurposeStatusCheck,
			[]core.ArtifactType{core.ArtifactTypeSummary}, bad); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})
}

func TestValidateRequestResponseInput(t *testing.T) {
	if err := core.ValidateRequestResponseInput("req_123", core.RequestResponseAccept); err != nil {
		t.Fatalf("valid input failed: %v", err)
	}

	t.Run("missing request ID", func(t *testing.T) {
		if err := core.ValidateRequestResponseInput("", core.RequestResponseAccept); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid action", func(t *testing.T) {
		if err := core.ValidateRequestResponseInput("req_123", "bogus"); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})
}

func TestValidateApprovalResolutionInput(t *testing.T) {
	if err := core.ValidateApprovalResolutionInput("ap_123", core.ApprovalStateApproved); err != nil {
		t.Fatalf("valid input failed: %v", err)
	}

	t.Run("missing approval ID", func(t *testing.T) {
		if err := core.ValidateApprovalResolutionInput("", core.ApprovalStateApproved); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	t.Run("invalid decision", func(t *testing.T) {
		if err := core.ValidateApprovalResolutionInput("ap_123", "maybe"); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})

	// only approved and denied are valid decisions, not pending
	t.Run("pending not a valid decision", func(t *testing.T) {
		if err := core.ValidateApprovalResolutionInput("ap_123", core.ApprovalStatePending); !core.IsValidationError(err) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	})
}
