package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"alice/internal/edge"
)

func main() {
	configPath := flag.String("config", "", "path to edge agent JSON config")
	bootstrapConnector := flag.String("bootstrap-connector", "", "connector to bootstrap via local oauth callback (github, jira, gcal)")
	registerWatches := flag.String("register-watches", "", "register provider-side push watches for a connector (gcal)")
	serveWebhooks := flag.Bool("serve-webhooks", false, "serve configured connector webhook endpoints")
	bootstrapTimeout := flag.Duration("bootstrap-timeout", 5*time.Minute, "how long to wait for the local oauth callback")
	flag.Parse()

	if *configPath == "" {
		slog.Error("edge agent requires -config")
		os.Exit(1)
	}

	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load edge config", "err", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	runtime := edge.NewRuntime(cfg)
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *bootstrapConnector != "" {
		ctx, cancel := context.WithTimeout(rootCtx, *bootstrapTimeout)
		defer cancel()

		result, err := runtime.BootstrapConnector(ctx, *bootstrapConnector, func(prompt edge.ConnectorBootstrapPrompt) error {
			slog.Info("open this URL to authorize connector", "connector", prompt.ConnectorType, "url", prompt.AuthorizationURL)
			slog.Info("waiting for callback", "url", prompt.CallbackURL)
			return nil
		})
		if err != nil {
			var keyErr *edge.CredentialStoreKeyRequiredError
			if errors.As(err, &keyErr) {
				slog.Error("edge connector bootstrap failed", "err", err, "hint", "Set "+cfg.CredentialsKeyEnvVar()+" or runtime.credentials_key_file before retrying.")
				os.Exit(1)
			}
			var decryptErr *edge.CredentialStoreDecryptError
			if errors.As(err, &decryptErr) {
				slog.Error("edge connector bootstrap failed", "err", err, "hint", "Check "+cfg.CredentialsKeyEnvVar()+" or runtime.credentials_key_file and retry.")
				os.Exit(1)
			}
			slog.Error("edge connector bootstrap failed", "err", err)
			os.Exit(1)
		}
		if err := encoder.Encode(result); err != nil {
			slog.Error("encode bootstrap report", "err", err)
			os.Exit(1)
		}
		return
	}

	if *registerWatches != "" {
		report, err := runtime.RegisterConnectorWatch(rootCtx, *registerWatches)
		if err != nil {
			var keyErr *edge.CredentialStoreKeyRequiredError
			if errors.As(err, &keyErr) {
				slog.Error("register watches failed", "err", err, "hint", "Set "+cfg.CredentialsKeyEnvVar()+" or runtime.credentials_key_file before retrying.")
				os.Exit(1)
			}
			var decryptErr *edge.CredentialStoreDecryptError
			if errors.As(err, &decryptErr) {
				slog.Error("register watches failed", "err", err, "hint", "Check "+cfg.CredentialsKeyEnvVar()+" or runtime.credentials_key_file and retry.")
				os.Exit(1)
			}
			slog.Error("register watches failed", "err", err)
			os.Exit(1)
		}
		if err := encoder.Encode(report); err != nil {
			slog.Error("encode watch report", "err", err)
			os.Exit(1)
		}
		return
	}

	if *serveWebhooks {
		if cfg.GitHubWebhookEnabled() {
			slog.Info("serving GitHub webhooks", "addr", "http://"+cfg.GitHubWebhookListenAddr()+edge.GitHubWebhookPath)
		}
		if cfg.JiraWebhookEnabled() {
			slog.Info("serving Jira webhooks", "addr", "http://"+cfg.JiraWebhookListenAddr()+edge.JiraWebhookPath)
		}
		if err := runtime.ServeWebhooks(rootCtx); err != nil {
			slog.Error("edge webhook server failed", "err", err)
			os.Exit(1)
		}
		return
	}

	report, err := runtime.RunOnce(rootCtx)
	if err != nil {
		var reauthErr *edge.ConnectorReauthRequiredError
		if errors.As(err, &reauthErr) {
			slog.Error("edge runtime failed", "err", err, "hint", "Re-authorize with: go run ./cmd/edge-agent -config "+*configPath+" -bootstrap-connector "+reauthErr.ConnectorType)
			os.Exit(1)
		}

		var keyErr *edge.CredentialStoreKeyRequiredError
		if errors.As(err, &keyErr) {
			slog.Error("edge runtime failed", "err", err, "hint", "Set "+cfg.CredentialsKeyEnvVar()+" or runtime.credentials_key_file before retrying.")
			os.Exit(1)
		}
		var decryptErr *edge.CredentialStoreDecryptError
		if errors.As(err, &decryptErr) {
			slog.Error("edge runtime failed", "err", err, "hint", "Check "+cfg.CredentialsKeyEnvVar()+" or runtime.credentials_key_file and retry.")
			os.Exit(1)
		}

		slog.Error("edge runtime failed", "err", err)
		os.Exit(1)
	}
	if err := encoder.Encode(report); err != nil {
		slog.Error("encode runtime report", "err", err)
		os.Exit(1)
	}
}
