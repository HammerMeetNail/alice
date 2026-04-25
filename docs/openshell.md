# OpenShell sandbox for the Alice edge agent

Running the Alice edge agent inside an [NVIDIA OpenShell](https://github.com/NVIDIA/OpenShell) sandbox adds a second containment layer on top of Docker: declarative egress policies that the sandbox enforces at the network proxy, Landlock-backed filesystem isolation, and a process identity constraint. A compromised or misbehaving edge agent cannot reach arbitrary internet hosts or read credentials outside its working directory.

---

## Prerequisites

- Docker or Podman running locally
- `openshell` CLI installed (`uv tool install -U openshell` or via the install script)
- The repository checked out with `make` available

---

## 1. Build the edge agent container image

The single `Dockerfile` at the repository root selects which binary to build via `CMD_PATH`:

```shell
docker build \
  --build-arg CMD_PATH=cmd/edge-agent \
  --tag alice-edge-agent:latest \
  .
```

This produces a minimal Alpine image with the edge agent binary at `/app/alice-bin`, running as the non-root `alice` user (uid 10001).

---

## 2. Prepare the config file and data volume

Create a local directory that will be bind-mounted into the sandbox as `/data`.  Put your edge agent config there (copy and edit one of the examples):

```shell
mkdir -p ~/.alice/sandbox
cp examples/edge-agent-config.json ~/.alice/sandbox/config.json
# edit ~/.alice/sandbox/config.json — set server.base_url, agent fields, etc.
```

The state file path in your config should point inside `/data` (the writable mount):

```json
{
  "runtime": {
    "state_file": "/data/state.json"
  }
}
```

You can also generate a policy skeleton directly from that config:

```shell
go run ./cmd/edge-agent \
  -config ~/.alice/sandbox/config.json \
  -generate-openshell-policy \
  > /tmp/edge-agent-openshell.yaml
```

The generated YAML includes only the coordination server and connector endpoints implied by the current config, plus a warning comment when `server.base_url` points at loopback.  It is valid input for `openshell policy set`.

---

## 3. Create an OpenShell provider for credentials

OpenShell injects credentials as environment variables at sandbox creation time.  Create a provider for the edge agent's secret material:

```shell
openshell provider create \
  --name alice-edge \
  --set ALICE_EDGE_CREDENTIAL_KEY="$(openssl rand -hex 32)" \
  --set ALICE_SERVER_URL="https://your-alice-server.example.com"
```

Add any connector-specific credentials to the same provider (or a separate one):

```shell
# GitHub connector
openshell provider create --name alice-github --set GITHUB_TOKEN="ghp_..."

# Google Calendar connector
openshell provider create --name alice-gcal \
  --set GCAL_CLIENT_ID="..." \
  --set GCAL_CLIENT_SECRET="..."
```

Credentials are never written to the sandbox filesystem; OpenShell injects them as environment variables at runtime.

---

## 4. Create the sandbox

```shell
openshell sandbox create \
  --from alice-edge-agent:latest \
  --name edge-agent \
  --provider alice-edge \
  --provider alice-github \
  --volume ~/.alice/sandbox:/data \
  -- /app/alice-bin -config /data/config.json
```

OpenShell bootstraps a gateway on first use, then starts the sandbox container.  The edge agent launches inside the sandbox and connects to the coordination server.

---

## 5. Apply the network policy

The default sandbox policy allows no outbound egress.  Apply either the checked-in baseline policy or the config-derived policy generated above:

```shell
# First edit the policy to replace placeholder hostnames.
# At minimum, update alice_server.endpoints[0].host to your coordination
# server hostname.

openshell policy set edge-agent \
  --policy deploy/openshell/policies/edge-agent.yaml \
  --wait
```

The `--wait` flag blocks until the proxy confirms the policy is active.

If you generated a policy from your config, substitute that file path instead:

```shell
openshell policy set edge-agent \
  --policy /tmp/edge-agent-openshell.yaml \
  --wait
```

---

## 6. Verify the sandbox is running

```shell
openshell sandbox list
openshell logs edge-agent --tail
```

Look for the edge agent startup logs.  If the agent cannot reach the coordination server, the proxy decision log will show a `policy_denied` line.

---

## Troubleshooting with audit mode

If you are adding a new connector or are unsure of the egress footprint, start with the dev (audit-mode) policy and tail the logs to observe every outbound attempt:

```shell
openshell policy set edge-agent \
  --policy deploy/openshell/policies/dev.yaml \
  --wait

openshell logs edge-agent --tail | grep policy_decision
```

In audit mode, OpenShell logs each decision but never blocks traffic.  Once you have confirmed the required hosts, update `edge-agent.yaml` to add them and switch to enforce mode.

---

## Policy files

| File | Mode | When to use |
|------|------|-------------|
| `deploy/openshell/policies/edge-agent.yaml` | enforce | Production — blocks all unlisted egress |
| `deploy/openshell/policies/dev.yaml` | audit | Development — logs all egress but never blocks |

Both files contain inline comments explaining each entry and how to adapt it for self-hosted Jira or non-standard coordination server URLs.

---

## What OpenShell enforces

| Layer | What it protects |
|-------|-----------------|
| Filesystem | Edge agent reads/writes only `/data`, `/tmp`, and system dirs; `/app` binary dir is read-only |
| Network | Only listed API hosts are reachable; all other outbound connections are blocked (enforce) or logged (audit) |
| Process | Agent runs as the non-root `alice` user; privilege escalation is blocked |

OpenShell does **not** replace Alice's own auth, permission grants, or audit logging — it is an additional containment layer that shrinks the blast radius if the agent process is compromised.

---

## Further reading

- [OpenShell documentation](https://docs.nvidia.com/openshell/latest/)
- [OpenShell policy schema reference](https://docs.nvidia.com/openshell/latest/reference/policy-schema.html)
- [Bring Your Own Container example](https://github.com/NVIDIA/OpenShell/tree/main/examples/bring-your-own-container)
- Alice threat model: `docs/threat-model.md` § 17 (OpenShell / sandbox strategy)
- Alice technical spec: `docs/technical-spec.md` § 20 (OpenShell integration strategy)
