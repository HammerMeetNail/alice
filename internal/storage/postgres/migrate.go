package postgres

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return fmt.Errorf("parse migration version from %q: %w", name, err)
		}

		tx, err := s.rawDB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction for migration %s: %w", name, err)
		}

		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = $1`, version).Scan(&count); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			_ = tx.Rollback()
			continue
		}

		path := filepath.Join("migrations", name)
		stmt, err := migrationFiles.ReadFile(path)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(stmt)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	return nil
}

// migrationVersion extracts the leading integer version from a migration filename
// such as "001_initial.sql" → 1.
func migrationVersion(filename string) (int, error) {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	parts := strings.SplitN(base, "_", 2)
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("migration filename must start with an integer version: %w", err)
	}
	return version, nil
}
