package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
	DefaultOrgName  string
	DatabaseURL     string
}

func FromEnv() Config {
	return Config{
		ListenAddr:      firstNonEmpty(os.Getenv("ALICE_LISTEN_ADDR"), ":8080"),
		ShutdownTimeout: 5 * time.Second,
		DefaultOrgName:  firstNonEmpty(os.Getenv("ALICE_DEFAULT_ORG_NAME"), "Alice Development Org"),
		DatabaseURL:     strings.TrimSpace(os.Getenv("ALICE_DATABASE_URL")),
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
