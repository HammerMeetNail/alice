package policy_test

import (
	"context"
	"testing"

	"alice/internal/core"
	"alice/internal/id"
	"alice/internal/policy"
	"alice/internal/storage/memory"
)

func TestGrant_ValidInput(t *testing.T) {
	store := memory.New()
	svc := policy.NewService(store)
	ctx := context.Background()

	orgID := id.New("org")
	grantor := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantor@example.com"}
	grantee := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantee@example.com"}

	grant, err := svc.Grant(ctx, orgID, grantor, grantee, "project", "myproject",
		[]core.ArtifactType{core.ArtifactTypeSummary},
		core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck},
	)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if grant.PolicyGrantID == "" {
		t.Fatal("expected non-empty grant ID")
	}
	if grant.GrantorUserID != grantor.UserID {
		t.Fatalf("grantor mismatch: got %s want %s", grant.GrantorUserID, grantor.UserID)
	}
}

func TestGrant_InvalidInput(t *testing.T) {
	store := memory.New()
	svc := policy.NewService(store)
	ctx := context.Background()

	orgID := id.New("org")
	grantor := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantor@example.com"}
	grantee := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantee@example.com"}

	cases := []struct {
		name          string
		granteeEmail  string
		scopeType     string
		scopeRef      string
		artifactTypes []core.ArtifactType
		maxSens       core.Sensitivity
		purposes      []core.QueryPurpose
	}{
		{
			name: "missing scope_ref",
			granteeEmail:  grantee.Email,
			scopeType:     "project",
			scopeRef:      "",
			artifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
			maxSens:       core.SensitivityLow,
			purposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		},
		{
			name:          "missing artifact types",
			granteeEmail:  grantee.Email,
			scopeType:     "project",
			scopeRef:      "proj",
			artifactTypes: nil,
			maxSens:       core.SensitivityLow,
			purposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		},
		{
			name:          "missing purposes",
			granteeEmail:  grantee.Email,
			scopeType:     "project",
			scopeRef:      "proj",
			artifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
			maxSens:       core.SensitivityLow,
			purposes:      nil,
		},
		{
			name:          "invalid sensitivity",
			granteeEmail:  grantee.Email,
			scopeType:     "project",
			scopeRef:      "proj",
			artifactTypes: []core.ArtifactType{core.ArtifactTypeSummary},
			maxSens:       core.Sensitivity("ultra"),
			purposes:      []core.QueryPurpose{core.QueryPurposeStatusCheck},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Grant(ctx, orgID, grantor, grantee, tc.scopeType, tc.scopeRef,
				tc.artifactTypes, tc.maxSens, tc.purposes)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRevokeGrant_ByGrantor(t *testing.T) {
	store := memory.New()
	svc := policy.NewService(store)
	ctx := context.Background()

	orgID := id.New("org")
	grantor := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantor@example.com"}
	grantee := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantee@example.com"}

	grant, err := svc.Grant(ctx, orgID, grantor, grantee, "project", "proj",
		[]core.ArtifactType{core.ArtifactTypeSummary},
		core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck},
	)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}

	revoked, err := svc.RevokeGrant(ctx, grant.PolicyGrantID, grantor.UserID)
	if err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("RevokedAt should be set")
	}
}

func TestRevokeGrant_ByNonGrantor(t *testing.T) {
	store := memory.New()
	svc := policy.NewService(store)
	ctx := context.Background()

	orgID := id.New("org")
	grantor := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantor@example.com"}
	grantee := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantee@example.com"}

	grant, err := svc.Grant(ctx, orgID, grantor, grantee, "project", "proj",
		[]core.ArtifactType{core.ArtifactTypeSummary},
		core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck},
	)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// Grantee should not be able to revoke the grant
	_, err = svc.RevokeGrant(ctx, grant.PolicyGrantID, grantee.UserID)
	if err == nil {
		t.Fatal("expected error when non-grantor tries to revoke")
	}
}

func TestListAllowedPeers(t *testing.T) {
	store := memory.New()
	svc := policy.NewService(store)
	ctx := context.Background()

	orgID := id.New("org")
	grantor := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantor@example.com"}
	grantee := core.User{UserID: id.New("user"), OrgID: orgID, Email: "grantee@example.com"}

	// Initially no peers
	peers, err := svc.ListAllowedPeers(ctx, grantee.UserID)
	if err != nil || len(peers) != 0 {
		t.Fatalf("expected 0 initial peers, got %d err=%v", len(peers), err)
	}

	// After grant, grantee sees grantor as a peer
	if _, err = svc.Grant(ctx, orgID, grantor, grantee, "project", "proj",
		[]core.ArtifactType{core.ArtifactTypeSummary},
		core.SensitivityLow,
		[]core.QueryPurpose{core.QueryPurposeStatusCheck},
	); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	peers, err = svc.ListAllowedPeers(ctx, grantee.UserID)
	if err != nil || len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d err=%v", len(peers), err)
	}
}
