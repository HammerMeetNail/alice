package config

import (
	"os"
	"time"
)

type Config struct {
	ListenAddr      string
	ShutdownTimeout time.Duration
	DefaultOrgName  string
}

func FromEnv() Config {
	return Config{
		ListenAddr:      firstNonEmpty(os.Getenv("ALICE_LISTEN_ADDR"), ":8080"),
		ShutdownTimeout: 5 * time.Second,
		DefaultOrgName:  firstNonEmpty(os.Getenv("ALICE_DEFAULT_ORG_NAME"), "Alice Development Org"),
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
