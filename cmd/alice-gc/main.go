// Command alice-gc removes stale rows that accumulate in the alice database over time.
//
// Run on a nightly cron or Kubernetes CronJob:
//
//	ALICE_DATABASE_URL=postgres://... alice-gc
//
// Environment variables:
//
//	ALICE_DATABASE_URL   (required) PostgreSQL DSN
//	ALICE_AUDIT_RETENTION  Optional duration (e.g. "90d", "365d"). When set,
//	                       audit events older than this value are deleted.
//	                       Unset (default) keeps audit events forever.
//
// Exit codes: 0 = success, 1 = fatal error (connection failure or unexpected DB error).
// Individual delete errors are logged but do not abort the remaining steps.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	dbURL := strings.TrimSpace(os.Getenv("ALICE_DATABASE_URL"))
	if dbURL == "" {
		slog.Error("ALICE_DATABASE_URL is required")
		os.Exit(1)
	}

	auditRetention := parseDuration(strings.TrimSpace(os.Getenv("ALICE_AUDIT_RETENTION")))

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer db.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		slog.Error("ping database", "err", err)
		os.Exit(1)
	}

	now := time.Now().UTC()
	slog.Info("alice-gc starting", "cutoff", now.Format(time.RFC3339))

	var hasErr bool

	// 1. Expired registration challenges.
	n, err := execDelete(ctx, db,
		`DELETE FROM agent_registration_challenges WHERE expires_at < $1`, now)
	if err != nil {
		slog.Error("delete expired challenges", "err", err)
		hasErr = true
	} else {
		slog.Info("deleted expired registration challenges", "count", n)
	}

	// 2. Expired and revoked bearer tokens.
	// Tokens are safe to delete when:
	//   - their natural expiry has passed, OR
	//   - they have been explicitly revoked (revoked_at IS NOT NULL).
	// Revoked tokens can never be used again, so there is no value in
	// keeping them beyond revocation time.
	n, err = execDelete(ctx, db,
		`DELETE FROM agent_tokens WHERE expires_at < $1 OR revoked_at IS NOT NULL`, now)
	if err != nil {
		slog.Error("delete expired/revoked tokens", "err", err)
		hasErr = true
	} else {
		slog.Info("deleted expired/revoked bearer tokens", "count", n)
	}

	// 3. Expired email verification records.
	n, err = execDelete(ctx, db,
		`DELETE FROM email_verifications WHERE expires_at < $1`, now)
	if err != nil {
		slog.Error("delete expired email verifications", "err", err)
		hasErr = true
	} else {
		slog.Info("deleted expired email verifications", "count", n)
	}

	// 4. Old audit events (opt-in via ALICE_AUDIT_RETENTION).
	if auditRetention > 0 {
		cutoff := now.Add(-auditRetention)
		n, err = execDelete(ctx, db,
			`DELETE FROM audit_events WHERE created_at < $1`, cutoff)
		if err != nil {
			slog.Error("delete old audit events", "err", err)
			hasErr = true
		} else {
			slog.Info("deleted old audit events", "count", n, "cutoff", cutoff.Format(time.RFC3339))
		}
	}

	if hasErr {
		slog.Error("alice-gc completed with errors")
		os.Exit(1)
	}
	slog.Info("alice-gc completed successfully")
}

// execDelete runs a DELETE statement with one $1 parameter and returns the
// number of affected rows.
func execDelete(ctx context.Context, db *sql.DB, query string, arg any) (int64, error) {
	res, err := db.ExecContext(ctx, query, arg)
	if err != nil {
		return 0, fmt.Errorf("exec: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// parseDuration parses a retention string like "90d", "365d", or a Go duration
// string like "2160h". Returns 0 on empty input or parse failure (caller treats 0
// as "disabled").
func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	// Support "Nd" shorthand (N days).
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || days <= 0 {
			slog.Warn("invalid ALICE_AUDIT_RETENTION value; audit retention disabled", "value", s)
			return 0
		}
		return time.Duration(days) * 24 * time.Hour
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		slog.Warn("invalid ALICE_AUDIT_RETENTION value; audit retention disabled", "value", s)
		return 0
	}
	return d
}
