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

	// Observability.
	//
	// MetricsAddr is the address for the Prometheus /metrics listener.
	// Empty string disables the metrics endpoint (default).
	MetricsAddr string

	// Security hardening.
	//
	// TLSTerminated controls whether HSTS is added to responses.
	// Set true when the server sits behind a TLS-terminating reverse proxy
	// and the X-Forwarded-Proto: https header is present.
	TLSTerminated bool
	// TrustedProxies is a comma-separated list of CIDR ranges whose
	// X-Forwarded-For header is trusted for client-IP extraction. When a
	// request arrives from an IP in this list the rightmost untrusted hop in
	// X-Forwarded-For is used as the client IP; otherwise RemoteAddr is used.
	TrustedProxies []string
	// RateLimitAgentPerMin is the per-agent token-bucket rate on the heavy
	// authenticated endpoints (POST /v1/queries, /v1/requests, /v1/artifacts).
	// 0 disables per-agent limiting (default: 60).
	RateLimitAgentPerMin float64
	// RateLimitAdminSignInPerMin is the per-IP rate limit on the admin UI
	// sign-in flow. 0 uses the default of 10.
	RateLimitAdminSignInPerMin float64

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

	// Gatekeeper auto-answer tuning.
	//
	// GatekeeperConfidenceThreshold is the minimum aggregate artifact
	// confidence required before the gatekeeper answers a request on the
	// recipient's behalf. Values outside [0, 1] fall back to the compile-time
	// default. Zero leaves the default.
	GatekeeperConfidenceThreshold float64
	// GatekeeperLookbackWindow is how far back the gatekeeper looks for
	// artifacts when synthesising a query. Zero leaves the default.
	GatekeeperLookbackWindow time.Duration

	// Admin UI feature flag and hardening.
	//
	// AdminUIEnabled gates the /admin/* browser surface. Off by default so
	// existing deployments don't suddenly expose a new attack surface.
	AdminUIEnabled bool
	// AdminUIAllowedOrigins is the explicit CORS allow-list for the admin
	// UI. Empty disables CORS entirely (same-origin only).
	AdminUIAllowedOrigins []string
	// AdminUIDevMode disables the HTTPS-only guard and the Secure cookie
	// attribute. Only safe for local development; never set in production.
	AdminUIDevMode bool
	// AdminUISessionTTL is how long an admin browser session lives after
	// sign-in. Zero leaves the 24 h default.
	AdminUISessionTTL time.Duration
	// AdminUISignInTTL is how long an email-OTP sign-in attempt remains
	// valid after the code is sent. Zero leaves the 10 m default.
	AdminUISignInTTL time.Duration
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

		MetricsAddr:                strings.TrimSpace(os.Getenv("ALICE_METRICS_ADDR")),
		TLSTerminated:              boolFromEnv("ALICE_TLS_TERMINATED", false),
		TrustedProxies:             splitCSVFromEnv("ALICE_TRUSTED_PROXIES"),
		RateLimitAgentPerMin:       floatFromEnvUnbounded("ALICE_RATE_LIMIT_AGENT_PER_MIN", 60),
		RateLimitAdminSignInPerMin: floatFromEnvUnbounded("ALICE_RATE_LIMIT_ADMIN_SIGNIN_PER_MIN", 10),

		SMTPHost:     smtpHost,
		SMTPPort:     smtpPort,
		SMTPUsername: strings.TrimSpace(os.Getenv("ALICE_SMTP_USERNAME")),
		SMTPPassword: os.Getenv("ALICE_SMTP_PASSWORD"),
		SMTPFrom:     strings.TrimSpace(os.Getenv("ALICE_SMTP_FROM")),
		SMTPTLS:      smtpTLS,

		EmailOTPTTL:         durationFromEnv("ALICE_EMAIL_OTP_TTL", 10*time.Minute),
		EmailOTPMaxAttempts: intFromEnv("ALICE_EMAIL_OTP_MAX_ATTEMPTS", 5),

		AuditLogFile: strings.TrimSpace(os.Getenv("ALICE_AUDIT_LOG_FILE")),

		GatekeeperConfidenceThreshold: floatFromEnv("ALICE_GATEKEEPER_CONFIDENCE_THRESHOLD", 0),
		GatekeeperLookbackWindow:      durationFromEnv("ALICE_GATEKEEPER_LOOKBACK_WINDOW", 0),

		AdminUIEnabled:        boolFromEnv("ALICE_ADMIN_UI_ENABLED", false),
		AdminUIAllowedOrigins: splitCSVFromEnv("ALICE_ADMIN_UI_ALLOWED_ORIGINS"),
		AdminUIDevMode:        boolFromEnv("ALICE_ADMIN_UI_DEV_MODE", false),
		AdminUISessionTTL:     durationFromEnv("ALICE_ADMIN_UI_SESSION_TTL", 24*time.Hour),
		AdminUISignInTTL:      durationFromEnv("ALICE_ADMIN_UI_SIGNIN_TTL", 10*time.Minute),
	}
}

func splitCSVFromEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func floatFromEnv(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 1 {
		return fallback
	}
	return value
}

// floatFromEnvUnbounded is like floatFromEnv but accepts any non-negative value.
func floatFromEnvUnbounded(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
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
