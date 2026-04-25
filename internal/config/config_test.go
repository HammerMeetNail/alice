package config_test

import (
	"testing"
	"time"

	"alice/internal/config"
)

func TestFromEnvDefaults(t *testing.T) {
	// Clear all relevant env vars so we get pure defaults.
	vars := []string{
		"ALICE_LISTEN_ADDR", "ALICE_DATABASE_URL", "ALICE_AUTH_CHALLENGE_TTL",
		"ALICE_AUTH_TOKEN_TTL", "ALICE_SMTP_HOST", "ALICE_SMTP_PORT",
		"ALICE_EMAIL_OTP_TTL", "ALICE_EMAIL_OTP_MAX_ATTEMPTS",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}

	cfg := config.FromEnv()

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr: got %q want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.AuthChallengeTTL != 5*time.Minute {
		t.Errorf("AuthChallengeTTL: got %v want %v", cfg.AuthChallengeTTL, 5*time.Minute)
	}
	if cfg.AuthTokenTTL != 15*time.Minute {
		t.Errorf("AuthTokenTTL: got %v want %v", cfg.AuthTokenTTL, 15*time.Minute)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL: got %q, want empty", cfg.DatabaseURL)
	}
	if cfg.SMTPHost != "" {
		t.Errorf("SMTPHost: got %q, want empty", cfg.SMTPHost)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("SMTPPort: got %d want 587", cfg.SMTPPort)
	}
	if cfg.EmailOTPMaxAttempts != 5 {
		t.Errorf("EmailOTPMaxAttempts: got %d want 5", cfg.EmailOTPMaxAttempts)
	}
}

func TestFromEnvCustomListenAddr(t *testing.T) {
	t.Setenv("ALICE_LISTEN_ADDR", ":9090")
	cfg := config.FromEnv()
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr: got %q want %q", cfg.ListenAddr, ":9090")
	}
}

func TestFromEnvDatabaseURL(t *testing.T) {
	t.Setenv("ALICE_DATABASE_URL", "postgres://alice:pw@localhost/alice")
	cfg := config.FromEnv()
	if cfg.DatabaseURL != "postgres://alice:pw@localhost/alice" {
		t.Errorf("DatabaseURL: got %q", cfg.DatabaseURL)
	}
}

func TestFromEnvSMTPConfig(t *testing.T) {
	t.Setenv("ALICE_SMTP_HOST", "smtp.example.com")
	t.Setenv("ALICE_SMTP_PORT", "465")
	t.Setenv("ALICE_SMTP_USERNAME", "user@example.com")
	t.Setenv("ALICE_SMTP_FROM", "noreply@example.com")

	cfg := config.FromEnv()

	if cfg.SMTPHost != "smtp.example.com" {
		t.Errorf("SMTPHost: got %q want smtp.example.com", cfg.SMTPHost)
	}
	if cfg.SMTPPort != 465 {
		t.Errorf("SMTPPort: got %d want 465", cfg.SMTPPort)
	}
	if cfg.SMTPUsername != "user@example.com" {
		t.Errorf("SMTPUsername: got %q", cfg.SMTPUsername)
	}
	if cfg.SMTPFrom != "noreply@example.com" {
		t.Errorf("SMTPFrom: got %q", cfg.SMTPFrom)
	}
}

func TestFromEnvInvalidDurationFallsBack(t *testing.T) {
	t.Setenv("ALICE_AUTH_TOKEN_TTL", "not-a-duration")
	cfg := config.FromEnv()
	if cfg.AuthTokenTTL != 15*time.Minute {
		t.Errorf("AuthTokenTTL should fall back to default on invalid input, got %v", cfg.AuthTokenTTL)
	}
}

// --- boolFromEnv ---

func TestBoolFromEnv_True(t *testing.T) {
	for _, v := range []string{"true", "1", "yes", "TRUE", "YES"} {
		t.Setenv("ALICE_TLS_TERMINATED", v)
		cfg := config.FromEnv()
		if !cfg.TLSTerminated {
			t.Errorf("TLSTerminated should be true for %q", v)
		}
	}
}

func TestBoolFromEnv_False(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "FALSE", "NO"} {
		t.Setenv("ALICE_SMTP_TLS", v)
		cfg := config.FromEnv()
		if cfg.SMTPTLS {
			t.Errorf("SMTPTLS should be false for %q", v)
		}
	}
}

func TestBoolFromEnv_FallbackOnUnknown(t *testing.T) {
	// ALICE_TLS_TERMINATED defaults false; unknown value should leave it false.
	t.Setenv("ALICE_TLS_TERMINATED", "maybe")
	cfg := config.FromEnv()
	if cfg.TLSTerminated {
		t.Error("TLSTerminated should fall back to false for unknown value")
	}
}

func TestBoolFromEnv_AdminUIEnabled(t *testing.T) {
	t.Setenv("ALICE_ADMIN_UI_ENABLED", "true")
	cfg := config.FromEnv()
	if !cfg.AdminUIEnabled {
		t.Error("AdminUIEnabled should be true")
	}
}

func TestBoolFromEnv_AdminUIDevMode(t *testing.T) {
	t.Setenv("ALICE_ADMIN_UI_DEV_MODE", "1")
	cfg := config.FromEnv()
	if !cfg.AdminUIDevMode {
		t.Error("AdminUIDevMode should be true for '1'")
	}
}

// --- floatFromEnv (bounded [0,1]) ---

func TestFloatFromEnv_ValidInRange(t *testing.T) {
	t.Setenv("ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD", "0.75")
	cfg := config.FromEnv()
	if cfg.GatekeeperConfidenceThreshold != 0.75 {
		t.Errorf("GatekeeperConfidenceThreshold: got %v want 0.75", cfg.GatekeeperConfidenceThreshold)
	}
}

func TestFloatFromEnv_AboveOne_FallsBack(t *testing.T) {
	t.Setenv("ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD", "1.5")
	cfg := config.FromEnv()
	if cfg.GatekeeperConfidenceThreshold != 0 {
		t.Errorf("GatekeeperConfidenceThreshold: out-of-range should fall back to 0, got %v", cfg.GatekeeperConfidenceThreshold)
	}
}

func TestFloatFromEnv_Negative_FallsBack(t *testing.T) {
	t.Setenv("ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD", "-0.1")
	cfg := config.FromEnv()
	if cfg.GatekeeperConfidenceThreshold != 0 {
		t.Errorf("GatekeeperConfidenceThreshold: negative should fall back to 0, got %v", cfg.GatekeeperConfidenceThreshold)
	}
}

func TestFloatFromEnv_ParseError_FallsBack(t *testing.T) {
	t.Setenv("ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD", "not-a-float")
	cfg := config.FromEnv()
	if cfg.GatekeeperConfidenceThreshold != 0 {
		t.Errorf("GatekeeperConfidenceThreshold: parse error should fall back to 0, got %v", cfg.GatekeeperConfidenceThreshold)
	}
}

// --- floatFromEnvUnbounded ---

func TestFloatFromEnvUnbounded_Valid(t *testing.T) {
	t.Setenv("ALICE_RATE_LIMIT_AGENT_PER_MIN", "120")
	cfg := config.FromEnv()
	if cfg.RateLimitAgentPerMin != 120 {
		t.Errorf("RateLimitAgentPerMin: got %v want 120", cfg.RateLimitAgentPerMin)
	}
}

func TestFloatFromEnvUnbounded_Zero_IsValid(t *testing.T) {
	t.Setenv("ALICE_RATE_LIMIT_AGENT_PER_MIN", "0")
	cfg := config.FromEnv()
	if cfg.RateLimitAgentPerMin != 0 {
		t.Errorf("RateLimitAgentPerMin: zero should be valid, got %v", cfg.RateLimitAgentPerMin)
	}
}

func TestFloatFromEnvUnbounded_Negative_FallsBack(t *testing.T) {
	t.Setenv("ALICE_RATE_LIMIT_AGENT_PER_MIN", "-5")
	cfg := config.FromEnv()
	if cfg.RateLimitAgentPerMin != 60 {
		t.Errorf("RateLimitAgentPerMin: negative should fall back to 60, got %v", cfg.RateLimitAgentPerMin)
	}
}

func TestFloatFromEnvUnbounded_ParseError_FallsBack(t *testing.T) {
	t.Setenv("ALICE_RATE_LIMIT_ADMIN_SIGNIN_PER_MIN", "bad")
	cfg := config.FromEnv()
	if cfg.RateLimitAdminSignInPerMin != 10 {
		t.Errorf("RateLimitAdminSignInPerMin: parse error should fall back to 10, got %v", cfg.RateLimitAdminSignInPerMin)
	}
}

// --- intFromEnv ---

func TestIntFromEnv_Valid(t *testing.T) {
	t.Setenv("ALICE_EMAIL_OTP_MAX_ATTEMPTS", "3")
	cfg := config.FromEnv()
	if cfg.EmailOTPMaxAttempts != 3 {
		t.Errorf("EmailOTPMaxAttempts: got %d want 3", cfg.EmailOTPMaxAttempts)
	}
}

func TestIntFromEnv_Zero_FallsBack(t *testing.T) {
	t.Setenv("ALICE_EMAIL_OTP_MAX_ATTEMPTS", "0")
	cfg := config.FromEnv()
	if cfg.EmailOTPMaxAttempts != 5 {
		t.Errorf("EmailOTPMaxAttempts: zero should fall back to 5, got %d", cfg.EmailOTPMaxAttempts)
	}
}

func TestIntFromEnv_Negative_FallsBack(t *testing.T) {
	t.Setenv("ALICE_EMAIL_OTP_MAX_ATTEMPTS", "-3")
	cfg := config.FromEnv()
	if cfg.EmailOTPMaxAttempts != 5 {
		t.Errorf("EmailOTPMaxAttempts: negative should fall back to 5, got %d", cfg.EmailOTPMaxAttempts)
	}
}

func TestIntFromEnv_ParseError_FallsBack(t *testing.T) {
	t.Setenv("ALICE_EMAIL_OTP_MAX_ATTEMPTS", "five")
	cfg := config.FromEnv()
	if cfg.EmailOTPMaxAttempts != 5 {
		t.Errorf("EmailOTPMaxAttempts: parse error should fall back to 5, got %d", cfg.EmailOTPMaxAttempts)
	}
}

func TestIntFromEnv_SMTPPort(t *testing.T) {
	t.Setenv("ALICE_SMTP_PORT", "465")
	cfg := config.FromEnv()
	if cfg.SMTPPort != 465 {
		t.Errorf("SMTPPort: got %d want 465", cfg.SMTPPort)
	}
}

// --- splitCSVFromEnv ---

func TestSplitCSVFromEnv_MultipleEntries(t *testing.T) {
	t.Setenv("ALICE_TRUSTED_PROXIES", "10.0.0.0/8,192.168.0.0/16")
	cfg := config.FromEnv()
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies: got %v want 2 entries", cfg.TrustedProxies)
	}
	if cfg.TrustedProxies[0] != "10.0.0.0/8" || cfg.TrustedProxies[1] != "192.168.0.0/16" {
		t.Errorf("TrustedProxies: unexpected values %v", cfg.TrustedProxies)
	}
}

func TestSplitCSVFromEnv_WhitespacePadded(t *testing.T) {
	t.Setenv("ALICE_TRUSTED_PROXIES", " 10.0.0.0/8 , 192.168.0.0/16 ")
	cfg := config.FromEnv()
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies: expected 2 trimmed entries, got %v", cfg.TrustedProxies)
	}
}

func TestSplitCSVFromEnv_SingleEntry(t *testing.T) {
	t.Setenv("ALICE_ADMIN_UI_ALLOWED_ORIGINS", "https://admin.example.com")
	cfg := config.FromEnv()
	if len(cfg.AdminUIAllowedOrigins) != 1 || cfg.AdminUIAllowedOrigins[0] != "https://admin.example.com" {
		t.Errorf("AdminUIAllowedOrigins: got %v", cfg.AdminUIAllowedOrigins)
	}
}

func TestSplitCSVFromEnv_Empty_ReturnsNil(t *testing.T) {
	t.Setenv("ALICE_TRUSTED_PROXIES", "")
	cfg := config.FromEnv()
	if cfg.TrustedProxies != nil {
		t.Errorf("TrustedProxies: empty string should give nil, got %v", cfg.TrustedProxies)
	}
}

// --- durationFromEnv ---

func TestDurationFromEnv_Zero_FallsBack(t *testing.T) {
	t.Setenv("ALICE_ADMIN_UI_SESSION_TTL", "0s")
	cfg := config.FromEnv()
	// zero duration falls back to the 24h default
	if cfg.AdminUISessionTTL != 24*time.Hour {
		t.Errorf("AdminUISessionTTL: zero should fall back to 24h, got %v", cfg.AdminUISessionTTL)
	}
}

func TestDurationFromEnv_Negative_FallsBack(t *testing.T) {
	t.Setenv("ALICE_ADMIN_UI_SIGNIN_TTL", "-5m")
	cfg := config.FromEnv()
	if cfg.AdminUISignInTTL != 10*time.Minute {
		t.Errorf("AdminUISignInTTL: negative should fall back to 10m, got %v", cfg.AdminUISignInTTL)
	}
}

func TestDurationFromEnv_Valid(t *testing.T) {
	t.Setenv("ALICE_GATEKEEPER_LOOKBACK_WINDOW", "48h")
	cfg := config.FromEnv()
	if cfg.GatekeeperLookbackWindow != 48*time.Hour {
		t.Errorf("GatekeeperLookbackWindow: got %v want 48h", cfg.GatekeeperLookbackWindow)
	}
}

// --- misc new fields ---

func TestFromEnvMetricsAddr(t *testing.T) {
	t.Setenv("ALICE_METRICS_ADDR", ":9090")
	cfg := config.FromEnv()
	if cfg.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr: got %q want :9090", cfg.MetricsAddr)
	}
}

func TestFromEnvAuditLogFile(t *testing.T) {
	t.Setenv("ALICE_AUDIT_LOG_FILE", "/var/log/alice-audit.ndjson")
	cfg := config.FromEnv()
	if cfg.AuditLogFile != "/var/log/alice-audit.ndjson" {
		t.Errorf("AuditLogFile: got %q", cfg.AuditLogFile)
	}
}

func TestFromEnvDefaultOrgName(t *testing.T) {
	t.Setenv("ALICE_DEFAULT_ORG_NAME", "My Org")
	cfg := config.FromEnv()
	if cfg.DefaultOrgName != "My Org" {
		t.Errorf("DefaultOrgName: got %q want 'My Org'", cfg.DefaultOrgName)
	}
}
