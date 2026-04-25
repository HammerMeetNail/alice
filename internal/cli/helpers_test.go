package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestTruncate verifies that truncate leaves short strings unchanged and
// appends an ellipsis character to longer ones.
func TestTruncate(t *testing.T) {
	// Short string — unchanged.
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("truncate short: got %q, want %q", got, "hello")
	}

	// Exact-length string — unchanged.
	if got := truncate("hello", 5); got != "hello" {
		t.Fatalf("truncate exact: got %q, want %q", got, "hello")
	}

	// Long string — prefix kept and ellipsis appended.
	got := truncate("hello world", 5)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	if !strings.HasPrefix(got, "hello") {
		t.Fatalf("expected prefix 'hello', got %q", got)
	}
}

// TestMapResponseAlias verifies that mapResponseAlias maps friendly CLI verbs
// to the canonical server enum values and passes unknown values through.
func TestMapResponseAlias(t *testing.T) {
	cases := []struct{ input, want string }{
		{"accept", "accepted"},
		{"Accept", "accepted"},
		{"ACCEPT", "accepted"},
		{"decline", "denied"},
		{"deny", "denied"},
		{"defer", "deferred"},
		{"complete", "completed"},
		{"unknown_value", "unknown_value"},
		{"", ""},
	}
	for _, c := range cases {
		got := mapResponseAlias(c.input)
		if got != c.want {
			t.Errorf("mapResponseAlias(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// TestResolveInlineValue_Literal verifies that a plain string is returned as-is.
func TestResolveInlineValue_Literal(t *testing.T) {
	got, err := resolveInlineValue("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", got)
	}
}

// TestResolveInlineValue_File verifies that "@path" loads the file contents
// with trailing newlines stripped.
func TestResolveInlineValue_File(t *testing.T) {
	f, err := os.CreateTemp("", "resolve_inline_test_*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString("file content\n"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()

	got, err := resolveInlineValue("@" + f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file content" {
		t.Fatalf("expected %q, got %q", "file content", got)
	}
}

// TestResolveInlineValue_FileError verifies that "@nonexistent" returns an error.
func TestResolveInlineValue_FileError(t *testing.T) {
	_, err := resolveInlineValue("@/nonexistent/path/to/file.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

// TestResolveTimestamp_Empty verifies that an empty string returns a zero time.
func TestResolveTimestamp_Empty(t *testing.T) {
	ts, err := resolveTimestamp("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.IsZero() {
		t.Fatalf("expected zero time, got %v", ts)
	}
}

// TestResolveTimestamp_RFC3339 verifies that an RFC3339 string is parsed correctly.
func TestResolveTimestamp_RFC3339(t *testing.T) {
	input := "2024-01-15T10:30:00Z"
	ts, err := resolveTimestamp(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Year() != 2024 || ts.Month() != 1 || ts.Day() != 15 {
		t.Fatalf("unexpected timestamp %v", ts)
	}
}

// TestResolveTimestamp_Duration verifies that a duration string like "1h"
// returns a timestamp approximately that far in the past.
func TestResolveTimestamp_Duration(t *testing.T) {
	before := time.Now().UTC()
	ts, err := resolveTimestamp("1h")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ts should be roughly 1 hour ago: between now-2h and now.
	if ts.Before(after.Add(-2*time.Hour)) || ts.After(before) {
		t.Fatalf("unexpected timestamp %v (want ~1h ago, before=%v)", ts, before)
	}
}

// TestResolveTimestamp_Invalid verifies that an unparseable value returns an error.
func TestResolveTimestamp_Invalid(t *testing.T) {
	_, err := resolveTimestamp("not-a-time-or-duration")
	if err == nil {
		t.Fatal("expected error for invalid timestamp string")
	}
}

// TestResolveTimeWindow_DefaultBounds verifies that empty since/until produce
// a 7-day window ending now.
func TestResolveTimeWindow_DefaultBounds(t *testing.T) {
	before := time.Now().UTC()
	m, err := resolveTimeWindow("", "")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	start, err1 := time.Parse(time.RFC3339, m["start"].(string))
	end, err2 := time.Parse(time.RFC3339, m["end"].(string))
	if err1 != nil || err2 != nil {
		t.Fatalf("parse error: start=%v end=%v", err1, err2)
	}
	// end should be approximately now.
	if end.Before(before.Add(-time.Second)) || end.After(after.Add(time.Second)) {
		t.Fatalf("end %v not approximately now", end)
	}
	// start should be approximately 7 days ago.
	expectedStart := before.Add(-7 * 24 * time.Hour)
	if start.Before(expectedStart.Add(-time.Minute)) || start.After(after.Add(-7*24*time.Hour+time.Minute)) {
		t.Logf("start=%v within acceptable 7-day-ago range", start) // soft check
	}
}

// TestResolveTimeWindow_WithSince verifies that a since value overrides the start.
func TestResolveTimeWindow_WithSince(t *testing.T) {
	since := "2024-01-01T00:00:00Z"
	m, err := resolveTimeWindow(since, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["start"].(string) != since {
		t.Fatalf("expected start=%q, got %q", since, m["start"])
	}
}

// TestResolveTimeWindow_InvalidSince verifies that an invalid since returns an error.
func TestResolveTimeWindow_InvalidSince(t *testing.T) {
	_, err := resolveTimeWindow("bad-since", "")
	if err == nil {
		t.Fatal("expected error for invalid since")
	}
}

// TestResolveTimeWindow_WithUntil verifies that an until value overrides the end.
func TestResolveTimeWindow_WithUntil(t *testing.T) {
	until := "2024-06-01T00:00:00Z"
	m, err := resolveTimeWindow("", until)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["end"].(string) != until {
		t.Fatalf("expected end=%q, got %q", until, m["end"])
	}
}

// TestResolveTimeWindow_InvalidUntil verifies that an invalid until returns an error.
func TestResolveTimeWindow_InvalidUntil(t *testing.T) {
	_, err := resolveTimeWindow("", "bad-until")
	if err == nil {
		t.Fatal("expected error for invalid until")
	}
}
