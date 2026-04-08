package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr       string
	ShutdownTimeout  time.Duration
	DefaultOrgName   string
	DatabaseURL      string
	AuthChallengeTTL time.Duration
	AuthTokenTTL     time.Duration

	// SMTP configuration for email OTP verification.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLS      bool

	// Email OTP settings.
	EmailOTPTTL         time.Duration
	EmailOTPMaxAttempts int

	// AuditLogFile is the path to an NDJSON audit log file. When set, all audit
	// events are written to this file in addition to the database.
	AuditLogFile string
}

func FromEnv() Config {
	smtpHost := strings.TrimSpace(os.Getenv("ALICE_SMTP_HOST"))
	smtpPort := intFromEnv("ALICE_SMTP_PORT", 587)
	smtpTLS := boolFromEnv("ALICE_SMTP_TLS", true)

	return Config{
		ListenAddr:       firstNonEmpty(os.Getenv("ALICE_LISTEN_ADDR"), ":8080"),
		ShutdownTimeout:  5 * time.Second,
		DefaultOrgName:   firstNonEmpty(os.Getenv("ALICE_DEFAULT_ORG_NAME"), "Alice Development Org"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("ALICE_DATABASE_URL")),
		AuthChallengeTTL: durationFromEnv("ALICE_AUTH_CHALLENGE_TTL", 5*time.Minute),
		AuthTokenTTL:     durationFromEnv("ALICE_AUTH_TOKEN_TTL", 15*time.Minute),

		SMTPHost:     smtpHost,
		SMTPPort:     smtpPort,
		SMTPUsername: strings.TrimSpace(os.Getenv("ALICE_SMTP_USERNAME")),
		SMTPPassword: os.Getenv("ALICE_SMTP_PASSWORD"),
		SMTPFrom:     strings.TrimSpace(os.Getenv("ALICE_SMTP_FROM")),
		SMTPTLS:      smtpTLS,

		EmailOTPTTL:         durationFromEnv("ALICE_EMAIL_OTP_TTL", 10*time.Minute),
		EmailOTPMaxAttempts: intFromEnv("ALICE_EMAIL_OTP_MAX_ATTEMPTS", 5),

		AuditLogFile: strings.TrimSpace(os.Getenv("ALICE_AUDIT_LOG_FILE")),
	}
}

func intFromEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func boolFromEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}

	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}

	return value
}
