package id_test

import (
	"strings"
	"testing"

	"alice/internal/id"
)

func TestNew_NonEmpty(t *testing.T) {
	got := id.New("agent")
	if got == "" {
		t.Fatal("expected non-empty ID")
	}
}

func TestNew_Prefix(t *testing.T) {
	got := id.New("artifact")
	if !strings.HasPrefix(got, "artifact_") {
		t.Fatalf("expected prefix %q, got %q", "artifact_", got)
	}
}

func TestNew_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		v := id.New("x")
		if seen[v] {
			t.Fatalf("duplicate ID generated: %s", v)
		}
		seen[v] = true
	}
}
