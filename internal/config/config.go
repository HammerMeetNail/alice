package config

import (
	"os"
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
}

func FromEnv() Config {
	return Config{
		ListenAddr:       firstNonEmpty(os.Getenv("ALICE_LISTEN_ADDR"), ":8080"),
		ShutdownTimeout:  5 * time.Second,
		DefaultOrgName:   firstNonEmpty(os.Getenv("ALICE_DEFAULT_ORG_NAME"), "Alice Development Org"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("ALICE_DATABASE_URL")),
		AuthChallengeTTL: durationFromEnv("ALICE_AUTH_CHALLENGE_TTL", 5*time.Minute),
		AuthTokenTTL:     durationFromEnv("ALICE_AUTH_TOKEN_TTL", 15*time.Minute),
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
