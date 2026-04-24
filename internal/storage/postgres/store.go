package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"alice/internal/core"
	"alice/internal/storage"

	_ "github.com/lib/pq"
)

// dbExecutor is satisfied by both *sql.DB and *sql.Tx, allowing all store
// methods to execute against either a plain connection or an open transaction.
type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db    dbExecutor
	rawDB *sql.DB // retained for BeginTx, Close, and Ping
}

var (
	_ storage.OrganizationRepository               = (*Store)(nil)
	_ storage.UserRepository                       = (*Store)(nil)
	_ storage.AgentRepository                      = (*Store)(nil)
	_ storage.AgentRegistrationChallengeRepository = (*Store)(nil)
	_ storage.AgentTokenRepository                 = (*Store)(nil)
	_ storage.ArtifactRepository                   = (*Store)(nil)
	_ storage.PolicyGrantRepository                = (*Store)(nil)
	_ storage.QueryRepository                      = (*Store)(nil)
	_ storage.RequestRepository                    = (*Store)(nil)
	_ storage.ApprovalRepository                   = (*Store)(nil)
	_ storage.AuditRepository                      = (*Store)(nil)
	_ storage.EmailVerificationRepository          = (*Store)(nil)
	_ storage.OrgGraphRepository                   = (*Store)(nil)
	_ storage.Transactor                           = (*Store)(nil)
)

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetMaxIdleConns(4)
	db.SetMaxOpenConns(10)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Store{db: db, rawDB: db}, nil
}

func (s *Store) Close() error {
	return s.rawDB.Close()
}

// Ping verifies the database connection is alive. Used by the /readyz handler.
func (s *Store) Ping(ctx context.Context) error {
	return s.rawDB.PingContext(ctx)
}

// DBStats returns connection-pool statistics. Used by the Prometheus collector.
func (s *Store) DBStats() sql.DBStats {
	return s.rawDB.Stats()
}

// Stats implements metrics.DBStatsGetter so *Store can be passed directly
// to metrics.Register.
func (s *Store) Stats() sql.DBStats {
	return s.rawDB.Stats()
}

// WithTx runs fn inside a single database transaction. If fn returns an error
// the transaction is rolled back; otherwise it is committed.
func (s *Store) WithTx(ctx context.Context, fn func(tx storage.StoreTx) error) error {
	tx, err := s.rawDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	txStore := &Store{db: tx, rawDB: s.rawDB}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func marshalJSONObject(value map[string]any) ([]byte, error) {
	if value == nil {
		value = map[string]any{}
	}
	return marshalJSON(value)
}

func marshalStringSlice(value []string) ([]byte, error) {
	if value == nil {
		value = []string{}
	}
	return marshalJSON(value)
}

func marshalArtifactTypes(value []core.ArtifactType) ([]byte, error) {
	if value == nil {
		value = []core.ArtifactType{}
	}
	return marshalJSON(value)
}

func marshalQueryPurposes(value []core.QueryPurpose) ([]byte, error) {
	if value == nil {
		value = []core.QueryPurpose{}
	}
	return marshalJSON(value)
}

func marshalSourceRefs(value []core.SourceReference) ([]byte, error) {
	if value == nil {
		value = []core.SourceReference{}
	}
	return marshalJSON(value)
}

func marshalQueryArtifacts(value []core.QueryArtifact) ([]byte, error) {
	if value == nil {
		value = []core.QueryArtifact{}
	}
	return marshalJSON(value)
}

func unmarshalJSON(data []byte, target any) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, target)
}
