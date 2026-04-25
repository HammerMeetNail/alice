# Google Calendar connector

The Google Calendar connector polls or receives push notifications for calendar events across one or more calendars and publishes derived `status_delta` artifacts (meeting load, focus blocks, availability windows) to the coordination server.

---

## Prerequisites

- A running edge agent configured for your org (see the root README for edge agent setup)
- A Google account with access to the calendars you want to track

---

## Authentication options

### Option A: OAuth access token (simple, short-lived)

Suitable for development or for automation accounts with a long-lived token (e.g., a service account delegated access).

1. Obtain a Google OAuth access token for the following scopes:
   - `https://www.googleapis.com/auth/calendar.readonly`
2. Export it:

```sh
export ALICE_GCAL_TOKEN="ya29.your-access-token"
```

3. Reference it in your config:

```json
{
  "connectors": {
    "gcal": {
      "enabled": true,
      "token_env_var": "ALICE_GCAL_TOKEN",
      "calendars": [
        {
          "id": "primary",
          "project_refs": ["my-project"],
          "category": "focus"
        }
      ]
    }
  }
}
```

See `examples/edge-agent-gcal-live-config.json` for a complete working config.

Note: Google OAuth access tokens expire after one hour. For unattended, long-running use, configure the OAuth PKCE flow (Option B) instead — it obtains and auto-refreshes tokens.

### Option B: OAuth app with PKCE loopback (recommended for production)

1. Go to **https://console.cloud.google.com/** and create or select a project.
2. Enable the **Google Calendar API** under **APIs & Services → Library**.
3. Under **APIs & Services → Credentials**, click **Create Credentials → OAuth client ID**.
   - Application type: **Web application**
   - Authorized redirect URI: `http://127.0.0.1:8787/oauth/gcal/callback` (adjust the port if needed)
4. Copy the **Client ID** and **Client secret**.
5. Under **APIs & Services → OAuth consent screen**, add the scope:
   - `https://www.googleapis.com/auth/calendar.readonly`
6. Export the client secret:

```sh
export ALICE_GCAL_CLIENT_SECRET="your-client-secret"
export ALICE_EDGE_CREDENTIAL_KEY="$(openssl rand -base64 32)"  # encrypt stored tokens
```

7. Configure the connector with OAuth enabled:

```json
{
  "connectors": {
    "gcal": {
      "enabled": true,
      "calendars": [
        {
          "id": "primary",
          "project_refs": ["my-project"],
          "category": "focus"
        }
      ],
      "oauth": {
        "enabled": true,
        "client_id": "your-google-oauth-client-id",
        "client_secret_env_var": "ALICE_GCAL_CLIENT_SECRET",
        "callback_url": "http://127.0.0.1:8787/oauth/gcal/callback"
      }
    }
  },
  "runtime": {
    "credentials_file": "~/.alice/edge-gcal-credentials.json",
    "credentials_key_env_var": "ALICE_EDGE_CREDENTIAL_KEY"
  }
}
```

8. Run the OAuth bootstrap once:

```sh
edge-agent -config edge-config.json -bootstrap-connector gcal
```

A browser window opens, you authorize the app, and the refresh token is encrypted and written to `credentials_file`. Subsequent runs refresh automatically.

See `examples/edge-agent-gcal-oauth-config.json` for a complete config.

---

## Calendar categories

The `category` field on each calendar entry controls how the edge agent classifies events when deriving availability artifacts. Supported values:

| Value | Meaning |
|---|---|
| `focus` | Deep-work blocks (no-meeting time, focus time events) |
| `meeting` | Scheduled meetings and calls |
| `personal` | Personal time (marked busy but content not shared) |
| `pto` | Out-of-office, holiday, or PTO events |

You can assign different categories to different calendar IDs. For example, your primary calendar might contain meetings while a secondary "Focus Time" calendar contains focus blocks:

```json
"calendars": [
  { "id": "primary",          "project_refs": ["project-a"], "category": "meeting" },
  { "id": "focus@group.calendar.google.com", "project_refs": ["project-a"], "category": "focus" }
]
```

---

## Webhook (push notification) setup

Polling is the default. Google Calendar push notifications deliver near-real-time event changes without consuming the polling quota.

Google Calendar push notifications are implemented via **watch channels** — a timed subscription (up to 7 days) that the edge agent renews automatically within a 15-minute window before expiry.

### 1. Expose a public HTTPS endpoint

Google Calendar requires a verified HTTPS endpoint. For production, use a domain with a valid TLS certificate. For local development, use ngrok or localtunnel:

```sh
ngrok http 8790
# or
lt --port 8790 --subdomain your-subdomain
```

### 2. Configure the connector for webhook mode

```json
{
  "connectors": {
    "gcal": {
      "token_env_var": "ALICE_GCAL_TOKEN",
      "webhook": {
        "enabled": true,
        "listen_addr": "0.0.0.0:8790",
        "secret_env_var": "ALICE_GCAL_WEBHOOK_SECRET"
      },
      "calendars": [
        {
          "id": "primary",
          "project_refs": ["my-project"],
          "category": "focus"
        }
      ]
    }
  }
}
```

See `examples/edge-agent-gcal-webhook-config.json` for a complete config.

### 3. Register watches

Run the edge agent in `-register-watches` mode to subscribe your calendars to push notifications:

```sh
ALICE_GCAL_WEBHOOK_SECRET="$(openssl rand -hex 32)" \
  edge-agent -config edge-config.json -register-watches
```

The agent calls the Google Calendar `watch` API, registers a push channel pointing at your public HTTPS endpoint, and persists the channel state. The registration is valid for up to 7 days; the agent auto-renews.

### 4. Start the webhook server

```sh
edge-agent -config edge-config.json -serve-webhooks
```

The agent listens on `listen_addr`. When Google sends a push notification, the agent fetches the updated event list and publishes a new artifact.

---

## Config reference

| Field | Type | Required | Description |
|---|---|---|---|
| `gcal.enabled` | bool | yes (live poll) | Enable the connector |
| `gcal.token_env_var` | string | yes (live poll) | Env var holding the OAuth access token |
| `gcal.calendars[].id` | string | yes | Calendar ID (`primary` or full calendar email) |
| `gcal.calendars[].project_refs` | []string | yes | Logical project tags applied to derived artifacts |
| `gcal.calendars[].category` | string | yes | Event category: `focus`, `meeting`, `personal`, or `pto` |
| `gcal.oauth.enabled` | bool | no | Use OAuth PKCE instead of a static token |
| `gcal.oauth.client_id` | string | yes (if oauth) | OAuth app client ID |
| `gcal.oauth.client_secret_env_var` | string | yes (if oauth) | Env var holding the client secret |
| `gcal.oauth.callback_url` | string | yes (if oauth) | Loopback callback URL (must match OAuth app setting) |
| `gcal.webhook.enabled` | bool | no | Enable webhook (push notification) intake |
| `gcal.webhook.listen_addr` | string | yes (if webhook) | `host:port` for the webhook listener |
| `gcal.webhook.secret_env_var` | string | yes (if webhook) | Env var holding the channel state secret |

---

## Troubleshooting

**`401 Unauthorized` on Calendar API calls**
Your access token has expired (Google tokens expire after one hour). Switch to the OAuth PKCE flow (Option B) to get automatic token refresh, or regenerate the token.

**Push notifications not arriving**
Google Calendar push channels require a publicly reachable HTTPS URL. Verify:
1. Your ngrok/localtunnel URL is active
2. The URL you registered matches the `listen_addr` port (not `localhost` — Google cannot reach it)
3. Re-run `-register-watches` after updating the tunnel URL; old channels must be stopped and new ones created

**`403 Forbidden` on watch registration**
The OAuth consent screen must include `https://www.googleapis.com/auth/calendar.readonly`. If you added the scope after bootstrapping OAuth, re-run:
```sh
edge-agent -config edge-config.json -bootstrap-connector gcal
```
to re-authorize with the new scope.

**Watch channel expires and push stops**
Watch channels have a maximum lifetime of 7 days. The edge agent renews channels automatically in the 15-minute window before expiry, but only while it is running. If the agent is stopped before renewal, restart it — it will re-register channels on startup.

**Duplicate event artifacts**
The edge agent deduplicates calendar push notifications using channel state persistence. Duplicate events are suppressed. If you see duplicate artifacts, check that `runtime.state_file` is persisted between restarts (i.e., not a tmpfs or container scratch layer).

**Calendar ID not found**
Use the full calendar email address for non-primary calendars (e.g., `focus@group.calendar.google.com`), not a display name. Retrieve your calendar IDs from the Calendar API:

```sh
curl -H "Authorization: Bearer $ALICE_GCAL_TOKEN" \
  "https://www.googleapis.com/calendar/v3/users/me/calendarList" \
  | jq '.items[] | {id, summary}'
```
