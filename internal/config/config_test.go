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
