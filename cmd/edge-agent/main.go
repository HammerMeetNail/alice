package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"alice/internal/edge"
)

func main() {
	configPath := flag.String("config", "", "path to edge agent JSON config")
	bootstrapConnector := flag.String("bootstrap-connector", "", "connector to bootstrap via local oauth callback (github, jira, gcal)")
	registerWatches := flag.String("register-watches", "", "register provider-side push watches for a connector (gcal)")
	serveWebhooks := flag.Bool("serve-webhooks", false, "serve configured connector webhook endpoints")
	dryRun := flag.Bool("dry-run", false, "preview what would be published without contacting the coordination server")
	validateConfig := flag.Bool("validate-config", false, "validate the config file, print normalized values, and exit")
	generateConfig := flag.Bool("generate-config", false, "print a starter config template to stdout and exit (no -config required)")
	generateOpenShellPolicy := flag.Bool("generate-openshell-policy", false, "print an OpenShell policy derived from the config to stdout and exit")
	bootstrapTimeout := flag.Duration("bootstrap-timeout", 5*time.Minute, "how long to wait for the local oauth callback")
	flag.Parse()

	// -generate-config does not require -config; handle it first.
	if *generateConfig {
		printStarterConfig()
		return
	}

	if *configPath == "" {
		slog.Error("edge agent requires -config")
		os.Exit(1)
	}

	cfg, err := edge.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load edge config", "err", err)
		os.Exit(1)
	}

	// -validate-config: print the normalised config and exit.
	if *validateConfig {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cfg); err != nil {
			slog.Error("encode config", "err", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "config OK: %s\n", *configPath)
		return
	}

	if *generateOpenShellPolicy {
		fmt.Print(edge.GenerateOpenShellPolicy(cfg))
		fmt.Fprintf(os.Stderr, "OpenShell policy OK: %s\n", *configPath)
		return
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if sandboxID := strings.TrimSpace(os.Getenv("OPENSHELL_SANDBOX_ID")); sandboxID != "" {
		slog.Info("detected OpenShell sandbox", "sandbox_id", sandboxID)
	}

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

	if *dryRun {
		artifacts, err := runtime.PreviewArtifacts(rootCtx)
		if err != nil {
			slog.Error("dry-run preview failed", "err", err)
			os.Exit(1)
		}
		preview := map[string]any{
			"dry_run":        true,
			"artifact_count": len(artifacts),
			"artifacts":      artifacts,
		}
		if err := encoder.Encode(preview); err != nil {
			slog.Error("encode dry-run report", "err", err)
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

// printStarterConfig writes a starter config template to stdout. The template
// is valid JSON that edge.LoadConfig accepts directly after filling in real
// values; it is also valid input for -validate-config. A field-by-field guide
// is printed to stderr so the two streams can be separated (stdout to a file,
// stderr to the terminal).
func printStarterConfig() {
	fmt.Fprint(os.Stderr, `# edge agent starter config
# Fill in the fields below, then run:
#   edge-agent -config my-config.json -validate-config
#
# agent.*          — required. Identifies the agent on the coordination server.
# server.base_url  — required. HTTPS URL of the coordination server.
# runtime.state_file — required. Persists keypair, bearer token, and cursors.
# runtime.credentials_key_env_var — env var holding the AES-256-GCM key for
#   the encrypted credentials store (set ALICE_EDGE_CREDENTIAL_KEY or change
#   this field). Set runtime.allow_plaintext_state:true to skip encryption
#   (local dev only).
#
# connectors.*     — all disabled by default. Set enabled:true and supply a
#   token to activate live polling. For GitHub/Jira, a plain API token in
#   token_env_var is enough to start. Google Calendar requires OAuth; run:
#     edge-agent -config my-config.json -bootstrap-connector gcal
#
# See docs/connectors/ for per-connector setup guides.
`)
	fmt.Print(`{
  "agent": {
    "org_slug": "your-org",
    "owner_email": "you@example.com",
    "agent_name": "my-edge-agent",
    "client_type": "edge_agent",
    "invite_token": ""
  },
  "server": {
    "base_url": "https://your-alice-server.example.com"
  },
  "runtime": {
    "state_file": "~/.alice/edge-agent-state.json",
    "credentials_key_env_var": "ALICE_EDGE_CREDENTIAL_KEY",
    "poll_incoming_requests": false,
    "allow_plaintext_state": false
  },
  "connectors": {
    "github": {
      "enabled": false,
      "token_env_var": "ALICE_GITHUB_TOKEN",
      "actor_login": "your-github-username",
      "repositories": [
        {
          "name": "your-org/your-repo",
          "project_refs": ["your-project"]
        }
      ],
      "webhook": {
        "enabled": false,
        "listen_addr": "127.0.0.1:8788",
        "secret_env_var": "ALICE_GITHUB_WEBHOOK_SECRET"
      }
    },
    "jira": {
      "enabled": false,
      "api_base_url": "https://your-org.atlassian.net",
      "token_env_var": "ALICE_JIRA_TOKEN",
      "actor_email": "you@example.com",
      "projects": [
        {
          "key": "PROJ",
          "project_refs": ["your-project"]
        }
      ],
      "webhook": {
        "enabled": false,
        "listen_addr": "127.0.0.1:8789",
        "secret_env_var": "ALICE_JIRA_WEBHOOK_SECRET"
      }
    },
    "gcal": {
      "enabled": false,
      "token_env_var": "ALICE_GCAL_TOKEN",
      "calendars": [
        {
          "id": "primary",
          "project_refs": [],
          "category": "meetings"
        }
      ],
      "oauth": {
        "enabled": true,
        "client_id": "YOUR_GOOGLE_CLIENT_ID"
      },
      "webhook": {
        "enabled": false,
        "listen_addr": "127.0.0.1:8790",
        "secret_env_var": "ALICE_GCAL_WEBHOOK_SECRET",
        "callback_url": "https://your-edge-agent.example.com/webhooks/gcal"
      }
    }
  }
}
`)
}
