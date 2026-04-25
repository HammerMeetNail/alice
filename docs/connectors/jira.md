# Jira connector

The Jira connector polls or receives push events for issues, transitions, and comments across one or more Jira projects and publishes derived `status_delta` artifacts to the coordination server.

Supported deployment targets: **Jira Cloud** (`*.atlassian.net`) and **Jira Data Center** (self-hosted, same REST API surface).

---

## Prerequisites

- A running edge agent configured for your org (see the root README for edge agent setup)
- A Jira account with read access to the projects you want to track

---

## Authentication options

### Option A: API token (recommended for getting started)

Jira Cloud requires an API token; username + password authentication has been deprecated.

1. Go to **https://id.atlassian.com/manage-profile/security/api-tokens** and click **Create API token**.
2. Give it a descriptive name (e.g., `alice-edge-agent`) and copy the generated token.
3. Set the environment variable the edge agent will read. The token is used as the password in HTTP Basic auth with your Atlassian email address as the username:

```sh
# Format: "email@example.com:API_TOKEN" base64-encoded, or just the raw token.
# The edge agent sends it as: Authorization: Basic base64(actor_email:token)
export ALICE_JIRA_TOKEN="your-api-token"
```

4. Reference it in your config:

```json
{
  "connectors": {
    "jira": {
      "enabled": true,
      "api_base_url": "https://your-domain.atlassian.net",
      "token_env_var": "ALICE_JIRA_TOKEN",
      "projects": [
        {
          "key": "PAY",
          "project_refs": ["payments-api"]
        }
      ]
    }
  }
}
```

See `examples/edge-agent-jira-live-config.json` for a complete working config.

For **Jira Data Center**, set `api_base_url` to your self-hosted base URL (e.g., `https://jira.internal.example.com`).

### Option B: OAuth app (PKCE loopback)

Use this when you want to avoid static credentials or need token rotation.

1. Go to **https://developer.atlassian.com/console/myapps/** and create a new OAuth 2.0 (3LO) app.
2. Add the following scopes under **Permissions → Jira API**:
   - `read:jira-work` (read issues, transitions, comments)
   - `read:jira-user` (resolve assignees and reporters)
   - `offline_access` (refresh token, required for unattended operation)
3. Set the **Callback URL** to `http://127.0.0.1:8787/oauth/jira/callback` (adjust if needed).
4. Copy the **Client ID** and **Client secret**.
5. Export the secret:

```sh
export ALICE_JIRA_CLIENT_SECRET="your-client-secret"
export ALICE_EDGE_CREDENTIAL_KEY="$(openssl rand -base64 32)"  # encrypt stored tokens
```

6. Configure the connector with OAuth enabled:

```json
{
  "connectors": {
    "jira": {
      "enabled": true,
      "api_base_url": "https://your-domain.atlassian.net",
      "projects": [
        { "key": "PAY", "project_refs": ["payments-api"] }
      ],
      "oauth": {
        "enabled": true,
        "client_id": "your-atlassian-oauth-client-id",
        "client_secret_env_var": "ALICE_JIRA_CLIENT_SECRET",
        "callback_url": "http://127.0.0.1:8787/oauth/jira/callback"
      }
    }
  },
  "runtime": {
    "credentials_file": "~/.alice/edge-jira-credentials.json",
    "credentials_key_env_var": "ALICE_EDGE_CREDENTIAL_KEY"
  }
}
```

7. Run the OAuth bootstrap once:

```sh
edge-agent -config edge-config.json -bootstrap-connector jira
```

See `examples/edge-agent-jira-oauth-config.json` for a complete config.

---

## Webhook setup (push mode)

Polling is the default. Jira webhooks deliver near-real-time issue and transition events without consuming the polling rate limit.

### 1. Generate a webhook secret

```sh
export ALICE_JIRA_WEBHOOK_SECRET="$(openssl rand -hex 32)"
```

### 2. Configure the connector for webhook mode

```json
{
  "connectors": {
    "jira": {
      "actor_email": "jane@example.com",
      "webhook": {
        "enabled": true,
        "listen_addr": "0.0.0.0:8789",
        "secret_env_var": "ALICE_JIRA_WEBHOOK_SECRET",
        "projects": [
          { "key": "PAY", "project_refs": ["payments-api"] }
        ]
      }
    }
  }
}
```

See `examples/edge-agent-jira-webhook-config.json` for a complete config.

### 3. Register the webhook in Jira

In Jira, go to **Settings → System → WebHooks** (admin required) and click **Create a WebHook**:

| Field | Value |
|---|---|
| Name | `alice-edge-agent` |
| URL | `https://your-public-host:8789/webhooks/jira` |
| Secret | The value of `ALICE_JIRA_WEBHOOK_SECRET` |
| Issue events | *Issue Created*, *Issue Updated*, *Issue Deleted* |
| Comment events | *Comment Created*, *Comment Updated* |

For Jira Cloud, you can also register webhooks via the REST API:

```sh
curl -u "you@example.com:API_TOKEN" \
  -H "Content-Type: application/json" \
  -X POST "https://your-domain.atlassian.net/rest/api/3/webhook" \
  -d '{
    "url": "https://your-public-host:8789/webhooks/jira",
    "webhooks": [{
      "events": ["jira:issue_created","jira:issue_updated"],
      "jqlFilter": "project = PAY"
    }]
  }'
```

### 4. Start the webhook server

```sh
edge-agent -config edge-config.json -serve-webhooks
```

### Local development with ngrok or localtunnel

Expose your local port for Jira Cloud to reach:

```sh
ngrok http 8789
# or
lt --port 8789 --subdomain your-subdomain
```

Use the forwarding URL as the Jira webhook URL. Jira Cloud requires HTTPS — both ngrok and localtunnel provide that automatically.

---

## Config reference

| Field | Type | Required | Description |
|---|---|---|---|
| `jira.enabled` | bool | yes (live poll) | Enable the connector |
| `jira.api_base_url` | string | yes | Jira instance base URL (e.g., `https://your-domain.atlassian.net`) |
| `jira.token_env_var` | string | yes (live poll) | Env var holding the API token |
| `jira.projects[].key` | string | yes | Jira project key (e.g., `PAY`) |
| `jira.projects[].project_refs` | []string | yes | Logical project tags applied to derived artifacts |
| `jira.actor_email` | string | yes (webhook) | Your Atlassian email; used to correlate events to you |
| `jira.oauth.enabled` | bool | no | Use OAuth 3LO instead of a static token |
| `jira.oauth.client_id` | string | yes (if oauth) | OAuth app client ID |
| `jira.oauth.client_secret_env_var` | string | yes (if oauth) | Env var holding the client secret |
| `jira.oauth.callback_url` | string | yes (if oauth) | Loopback callback URL (must match app setting) |
| `jira.webhook.enabled` | bool | no | Enable webhook intake |
| `jira.webhook.listen_addr` | string | yes (if webhook) | `host:port` for the webhook listener |
| `jira.webhook.secret_env_var` | string | yes (if webhook) | Env var holding the shared webhook secret |
| `jira.webhook.projects` | []object | yes (if webhook) | Same shape as live-poll `projects` |

---

## Troubleshooting

**`401 Unauthorized` on API requests**
Confirm that `ALICE_JIRA_TOKEN` is set to an API token (not your Atlassian password). API tokens are created at `id.atlassian.com`, not in Jira itself. The token is used as the HTTP Basic auth password with your email address as the username.

**Signature validation failed (webhook returns 400)**
Jira Cloud sends a `X-Hub-Signature` (HMAC-SHA256) header. Verify that `ALICE_JIRA_WEBHOOK_SECRET` matches the secret you entered when registering the webhook. A mismatch on any byte will cause the validation to fail.

**No events arriving for a project**
Check that the JQL filter on the registered webhook includes your project key. For Data Center, also verify that the webhook URL is reachable from the Jira server — firewalls between the Jira server and the internet are a common source of silence.

**`413 Request Entity Too Large`**
The edge agent body limit is 1 MiB. Jira webhooks that include full issue bodies for very large tickets can exceed this. If you see 413 responses in your Jira webhook delivery log, reduce the payload by filtering down to smaller project scopes or contact your Jira admin to enable payload compression.

**Wrong project key**
Jira project keys are case-sensitive (`PAY` ≠ `pay`). Use the exact key shown in your Jira project settings.

**OAuth refresh fails after token expiry**
Atlassian refresh tokens expire after 90 days of inactivity. If the edge agent is stopped for longer than that, re-run the bootstrap:
```sh
edge-agent -config edge-config.json -bootstrap-connector jira
```
