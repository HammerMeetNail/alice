# GitHub connector

The GitHub connector polls or receives push events for pull requests, reviews, issues, and commits across one or more repositories and publishes derived `status_delta` artifacts to the coordination server.

---

## Prerequisites

- A running edge agent configured for your org (see the root README for edge agent setup)
- A GitHub account with access to the repositories you want to track

---

## Authentication options

There are two ways to authenticate the GitHub connector: a static personal-access token (simplest) or an OAuth app with loopback PKCE (longer-lived, more secure).

### Option A: Personal access token (recommended for getting started)

1. Go to **GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)** and click **Generate new token (classic)**.
2. Select the following scopes:
   - `repo` (full repository access, including private repos and PR/issue events)
   - `read:user` (needed to resolve your `actor_login`)
   - `read:org` (needed if the repos are in an org with SSO)
3. Copy the generated token.
4. Set the environment variable the edge agent will read:

```sh
export ALICE_GITHUB_TOKEN="ghp_your_token_here"
```

5. Reference it in your config:

```json
{
  "connectors": {
    "github": {
      "enabled": true,
      "token_env_var": "ALICE_GITHUB_TOKEN",
      "actor_login": "your-github-username",
      "repositories": [
        {
          "name": "your-org/your-repo",
          "project_refs": ["your-project"]
        }
      ]
    }
  }
}
```

See `examples/edge-agent-github-live-config.json` for a complete working config.

### Option B: OAuth app (PKCE loopback)

Use this when you want to avoid long-lived static tokens or need to rotate credentials without redeploying.

1. Go to **GitHub → Settings → Developer settings → OAuth Apps** and click **New OAuth App**.
2. Set **Authorization callback URL** to `http://127.0.0.1:8787/oauth/github/callback` (adjust the port if needed).
3. Copy the **Client ID** and generate a **Client secret**.
4. Export the secret:

```sh
export ALICE_GITHUB_CLIENT_SECRET="your-client-secret"
export ALICE_EDGE_CREDENTIAL_KEY="$(openssl rand -base64 32)"  # encrypt stored tokens
```

5. Configure the connector with OAuth enabled:

```json
{
  "connectors": {
    "github": {
      "enabled": true,
      "actor_login": "your-github-username",
      "repositories": [
        { "name": "your-org/your-repo", "project_refs": ["your-project"] }
      ],
      "oauth": {
        "enabled": true,
        "client_id": "your-github-oauth-client-id",
        "client_secret_env_var": "ALICE_GITHUB_CLIENT_SECRET",
        "callback_url": "http://127.0.0.1:8787/oauth/github/callback"
      }
    }
  },
  "runtime": {
    "credentials_file": "~/.alice/edge-github-credentials.json",
    "credentials_key_env_var": "ALICE_EDGE_CREDENTIAL_KEY"
  }
}
```

6. Run the OAuth bootstrap once to authorize and store the token:

```sh
edge-agent -config edge-config.json -bootstrap-connector github
```

A browser window opens, you authorize the app, and the token is encrypted and written to `credentials_file`.

See `examples/edge-agent-github-oauth-config.json` for a complete config.

---

## Webhook setup (push mode)

Polling is the default. Webhooks reduce latency to near-real-time and eliminate polling API rate usage, but require a publicly reachable endpoint.

### 1. Generate a webhook secret

```sh
export ALICE_GITHUB_WEBHOOK_SECRET="$(openssl rand -hex 32)"
```

### 2. Configure the connector for webhook mode

```json
{
  "connectors": {
    "github": {
      "actor_login": "your-github-username",
      "webhook": {
        "enabled": true,
        "listen_addr": "0.0.0.0:8788",
        "secret_env_var": "ALICE_GITHUB_WEBHOOK_SECRET",
        "repositories": [
          { "name": "your-org/your-repo", "project_refs": ["your-project"] }
        ]
      }
    }
  }
}
```

See `examples/edge-agent-github-webhook-config.json` for a complete config.

### 3. Register the webhook on GitHub

In the repository (or org) settings, go to **Webhooks → Add webhook** and fill in:

| Field | Value |
|---|---|
| Payload URL | `https://your-public-host:8788/webhooks/github` |
| Content type | `application/json` |
| Secret | The value of `ALICE_GITHUB_WEBHOOK_SECRET` |
| Events | Select: *Pull request*, *Pull request review*, *Issues*, *Issue comment*, *Push* |

### 4. Start the webhook server

```sh
edge-agent -config edge-config.json -serve-webhooks
```

The agent listens on `listen_addr` and verifies incoming requests with HMAC-SHA256 before processing them.

### Local development with ngrok or localtunnel

If you are developing locally and need to receive webhooks from GitHub, expose the local port with:

```sh
ngrok http 8788
# or
lt --port 8788 --subdomain your-subdomain
```

Use the forwarding URL (`https://xxxx.ngrok.io`) as the GitHub webhook payload URL. Remember to update the webhook URL in GitHub when your tunnel URL changes.

---

## Config reference

| Field | Type | Required | Description |
|---|---|---|---|
| `github.enabled` | bool | yes (live poll) | Enable the connector |
| `github.token_env_var` | string | yes (live poll) | Env var holding the PAT |
| `github.actor_login` | string | yes | Your GitHub username; events for this user are prioritised |
| `github.repositories[].name` | string | yes | Full repo name (`owner/repo`) |
| `github.repositories[].project_refs` | []string | yes | Logical project tags applied to derived artifacts |
| `github.oauth.enabled` | bool | no | Use OAuth PKCE instead of a static token |
| `github.oauth.client_id` | string | yes (if oauth) | OAuth app client ID |
| `github.oauth.client_secret_env_var` | string | yes (if oauth) | Env var holding the client secret |
| `github.oauth.callback_url` | string | yes (if oauth) | Loopback callback URL (must match OAuth app setting) |
| `github.webhook.enabled` | bool | no | Enable webhook intake |
| `github.webhook.listen_addr` | string | yes (if webhook) | `host:port` for the webhook listener |
| `github.webhook.secret_env_var` | string | yes (if webhook) | Env var holding the HMAC webhook secret |
| `github.webhook.repositories` | []object | yes (if webhook) | Same shape as live-poll `repositories` |

---

## Troubleshooting

**HMAC signature mismatch (webhook returns 400)**
Verify that `ALICE_GITHUB_WEBHOOK_SECRET` matches the secret stored in the GitHub webhook settings exactly. Even a trailing newline will cause the comparison to fail.

**Replay rejected (webhook returns 409)**
The agent deduplicates events using the `X-GitHub-Delivery` header. If you replay the same delivery from the GitHub webhook settings, it will be rejected. This is expected behaviour — redeliveries are for debugging only.

**`403 Forbidden` on private repos**
Ensure the PAT includes the `repo` scope. If your org enforces SSO, the token must also be authorized for SSO in the GitHub UI.

**Rate limit errors (`403 rate limit exceeded`)**
Reduce `ALICE_TRACK_INTERVAL` or switch to webhook mode to avoid polling rate limits. The default polling interval is 5 minutes, which is within the 5000 req/hr limit for most users with a few repos.

**`actor_login` required**
The `actor_login` field is required for the live connector. Set it to your GitHub username (lowercase, as it appears in your profile URL).
