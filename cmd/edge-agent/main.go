package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"alice/internal/edge"
)

func main() {
	configPath := flag.String("config", "", "path to edge agent JSON config")
	bootstrapConnector := flag.String("bootstrap-connector", "", "connector to bootstrap via local oauth callback (github, jira, gcal)")
	bootstrapTimeout := flag.Duration("bootstrap-timeout", 5*time.Minute, "how long to wait for the local oauth callback")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("edge agent requires -config")
	}

	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load edge config: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	runtime := edge.NewRuntime(cfg)
	if *bootstrapConnector != "" {
		ctx, cancel := context.WithTimeout(context.Background(), *bootstrapTimeout)
		defer cancel()

		result, err := runtime.BootstrapConnector(ctx, *bootstrapConnector, func(prompt edge.ConnectorBootstrapPrompt) error {
			log.Printf("Open this URL to authorize %s: %s", prompt.ConnectorType, prompt.AuthorizationURL)
			log.Printf("Waiting for callback at %s", prompt.CallbackURL)
			return nil
		})
		if err != nil {
			var keyErr *edge.CredentialStoreKeyRequiredError
			if errors.As(err, &keyErr) {
				log.Printf("edge connector bootstrap failed: %v", err)
				log.Fatalf("Set %s or runtime.credentials_key_file before retrying.", cfg.CredentialsKeyEnvVar())
			}
			var decryptErr *edge.CredentialStoreDecryptError
			if errors.As(err, &decryptErr) {
				log.Printf("edge connector bootstrap failed: %v", err)
				log.Fatalf("Check %s or runtime.credentials_key_file and retry.", cfg.CredentialsKeyEnvVar())
			}
			log.Fatalf("edge connector bootstrap failed: %v", err)
		}
		if err := encoder.Encode(result); err != nil {
			log.Fatalf("encode bootstrap report: %v", err)
		}
		return
	}

	report, err := runtime.RunOnce(context.Background())
	if err != nil {
		var reauthErr *edge.ConnectorReauthRequiredError
		if errors.As(err, &reauthErr) {
			log.Printf("edge runtime failed: %v", err)
			log.Fatalf("Re-authorize with: go run ./cmd/edge-agent -config %s -bootstrap-connector %s", *configPath, reauthErr.ConnectorType)
		}

		var keyErr *edge.CredentialStoreKeyRequiredError
		if errors.As(err, &keyErr) {
			log.Printf("edge runtime failed: %v", err)
			log.Fatalf("Set %s or runtime.credentials_key_file before retrying.", cfg.CredentialsKeyEnvVar())
		}
		var decryptErr *edge.CredentialStoreDecryptError
		if errors.As(err, &decryptErr) {
			log.Printf("edge runtime failed: %v", err)
			log.Fatalf("Check %s or runtime.credentials_key_file and retry.", cfg.CredentialsKeyEnvVar())
		}

		log.Fatalf("edge runtime failed: %v", err)
	}
	if err := encoder.Encode(report); err != nil {
		log.Fatalf("encode runtime report: %v", err)
	}
}
