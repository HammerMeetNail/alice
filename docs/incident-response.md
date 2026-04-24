# Incident response

Short runbooks for common production incidents. Each runbook lists symptoms,
immediate containment steps, root-cause investigation, and recovery.

---

## 1. Compromised bearer token or private key

### Symptoms
- Unexpected API calls in the audit log attributed to a known agent
- Agent reports not having made requests it is being attributed for
- Unfamiliar `actor_agent_id` in audit events

### Containment
1. **Revoke all tokens for the affected agent immediately.** There is no
   single-agent logout CLI command; use the database directly or reject the
   agent via the admin approval queue (which also revokes tokens):
   ```bash
   # As an org admin — rejects the agent and revokes all its bearer tokens:
   alice deny <agent-id>
   ```
   If the agent needs to remain registered but all existing tokens must be
   invalidated, purge its rows in `agent_tokens` directly:
   ```sql
   UPDATE agent_tokens SET revoked_at = NOW() WHERE agent_id = '<agent-id>' AND revoked_at IS NULL;
   ```
2. If the Ed25519 **private key** is suspected compromised, de-register the agent:
   delete its row from `agents` in the database and re-register with a new keypair.
3. Block the agent's source IP at the load balancer / network policy if the attack
   is ongoing.

### Investigation
```bash
# Pull all recent audit events and filter to the compromised agent
# (no --agent flag; pipe through jq or grep on actor_agent_id)
alice audit --since 24h --json | jq '.items[] | select(.actor_agent_id=="<agent-id>")'

# Identify what actions were taken (grants issued, artifacts written, requests sent)
alice audit --since 24h --json | jq -r '.items[] | select(.actor_agent_id=="<agent-id>") | .kind'
```

Review the `kind` field: `artifact.published`, `policy.grant.created`, `request.created` are the
high-impact event types.

### Recovery
1. Audit and revoke any grants issued during the compromise window.
2. Delete or supersede any artifacts published during the window.
3. Notify affected peers (grantees who received grants from the compromised agent).
4. Re-register the agent with a fresh keypair after root cause is understood.

---

## 2. Compromised admin invite token

### Symptoms
- Unknown agents have registered in the org
- Audit log shows unexpected `agent.registered` events
- `alice peers` lists agents that no one in your team recognizes

### Containment
1. **Rotate the invite token immediately:**
   ```bash
   # Via HTTP (no dedicated CLI subcommand — use curl or the MCP tool):
   curl -X POST https://<server>/v1/orgs/rotate-invite-token \
     -H "Authorization: Bearer <admin-token>"
   # Or via the MCP rotate_invite_token tool if using claude/opencode.
   # Distribute the new token out-of-band to legitimate registrants only.
   ```
2. Reject any `pending_admin_approval` agents you do not recognize:
   ```bash
   # List agents awaiting approval (no dedicated CLI subcommand — use curl or the MCP tool):
   curl -s https://<server>/v1/orgs/pending-agents \
     -H "Authorization: Bearer <admin-token>"

   # Reject each unknown agent by its ID:
   curl -X POST https://<server>/v1/orgs/agents/<agent-id>/review \
     -H "Authorization: Bearer <admin-token>" \
     -H "Content-Type: application/json" \
     -d '{"decision":"rejected"}'
   # Or via the MCP tools: list_pending_agents then review_agent.
   ```
3. If unauthorized agents are already `active`, revoke their tokens and delete their
   agent rows from the database.

### Investigation
```bash
# List all agents in the org and review registration timestamps
alice peers

# Check audit log for agent.registered events in the compromise window
alice audit --event-kind agent.registered --since 48h
```

### Recovery
1. Confirm all legitimate agents are still active after token rotation.
2. Review whether the compromised token led to data exfiltration (policy.grant.created,
   artifact.published, request.created events from unknown agents).
3. If the server is publicly reachable, consider switching to `admin_approval` mode
   so all future registrations require explicit review:
   ```http
   POST /v1/orgs/verification-mode
   {"verification_mode": "admin_approval"}
   ```

---

## 3. Database corruption or catastrophic failure

### Symptoms
- alice-server crashes with `ERROR pq: ...` or exits with non-zero status
- `/readyz` returns 503 continuously
- PostgreSQL logs show checksum failures or `FATAL: could not open file`

### Containment
1. Scale alice-server to 0 replicas to stop further writes.
2. Snapshot the current database volume before any recovery attempt.

### Recovery from backup
```bash
# 1. Identify the most recent clean backup
ls -lt /backups/alice-*.dump

# 2. Drop and recreate the database
psql -c "DROP DATABASE IF EXISTS alice;" $PG_DSN
psql -c "CREATE DATABASE alice OWNER alice;" $PG_DSN

# 3. Restore
pg_restore --dbname=alice --no-owner --no-privileges /backups/alice-YYYYMMDD.dump

# 4. Confirm migration state
psql -c "SELECT version FROM schema_migrations ORDER BY version;" $ALICE_DATABASE_URL

# 5. Start alice-server; it will run any missing migrations automatically
```

### Point-in-time recovery (if WAL archiving is enabled)
```bash
# Stop PostgreSQL, restore base backup, configure recovery.conf / recovery target
# See: https://www.postgresql.org/docs/current/continuous-archiving.html
```

### Post-recovery audit
- Check how many minutes/hours of data were lost based on the backup timestamp.
- Notify affected users of the data-loss window.
- Review whether any in-progress requests or approvals need manual resolution.

---

## 4. Registration spam / abuse spike

### Symptoms
- Sudden spike in `alice_rate_limit_rejections_total{limiter="ip"}` in Prometheus
- High traffic volume to `POST /v1/agents/register/challenge` in the access log
- Database growth from stale challenge rows

### Containment
1. **Tighten registration rate limit** at the load balancer (e.g. nginx `limit_req`
   or Caddy rate-limit plugin) before the request reaches alice-server.
2. **Block offending CIDRs** at the firewall or cloud security group.
3.    Optionally switch to `invite_token` verification mode to require a pre-shared
   token for all new registrations:
   ```bash
   # Rotate the token via HTTP (no dedicated CLI subcommand):
   curl -X POST https://<server>/v1/orgs/rotate-invite-token \
     -H "Authorization: Bearer <admin-token>"
   # Then tighten the mode:
   # POST /v1/orgs/verification-mode {"verification_mode":"invite_token"}
   ```

### Investigation
```bash
# Find the top source IPs from access logs (nginx / Caddy / ALB)
awk '{print $1}' /var/log/nginx/access.log | sort | uniq -c | sort -rn | head -20

# Check alice audit for completed (successful) registrations in the window
alice audit --event-kind agent.registered --since 1h --json | jq -r '.items[].actor_agent_id' | sort | uniq -c
```

### Recovery
1. Run `cmd/alice-gc` to clean up the stale challenge rows.
2. Review whether any spammy registrations completed before the block; reject pending
   agents via the admin approval queue.
3. Document the offending ASN/CIDR and add a permanent block if it recurs.

---

## 5. Audit-log sink failure

### Symptoms
- `ALICE_AUDIT_LOG_FILE` path exists but the file size is not growing
- Disk-full error in alice-server logs: `write audit log: no space left on device`
- Alert fires on `alice_audit_events_total` not increasing over a period

### Containment
Audit events are written to the database **first**; the NDJSON file is a secondary
sink. A file-sink failure does not prevent the server from operating, but it creates a
gap in the log file.

1. If disk is full: clear space (rotate old log files, increase volume size).
2. Restart alice-server; it will reopen the audit log file and resume writing.

### Recovering the gap
The gap can be recovered by re-exporting audit events from the database:

```bash
# Assuming you know the gap started at $GAP_START (RFC3339 timestamp)
alice audit --since "$GAP_START" --json | jq -c '.items[]' >> /var/log/alice/audit.log
```

Or with raw SQL:
```sql
SELECT row_to_json(a)
FROM audit_events a
WHERE a.created_at >= '$GAP_START'
ORDER BY a.created_at;
```

### Prevention
- Mount the audit log on a dedicated volume separate from the OS disk.
- Set a Prometheus alert on `predict_linear(node_filesystem_free_bytes[1h], 4*3600) < 0`
  for the volume hosting the audit log.
- Consider shipping audit events to an external SIEM (Elastic, Splunk, Loki) via
  Filebeat or Vector as a parallel sink.
