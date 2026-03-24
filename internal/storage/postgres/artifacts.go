package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"alice/internal/core"
)

func (s *Store) SaveArtifact(ctx context.Context, artifact core.Artifact) (core.Artifact, error) {
	structuredPayload, err := marshalJSONObject(artifact.StructuredPayload)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("marshal structured payload: %w", err)
	}
	sourceRefs, err := marshalSourceRefs(artifact.SourceRefs)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("marshal source refs: %w", err)
	}

	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO artifacts (
			artifact_id, org_id, owner_agent_id, owner_user_id, type, title, content, structured_payload,
			source_refs, visibility_mode, sensitivity, confidence, approval_state, created_at, expires_at, supersedes_artifact_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8::jsonb,
			$9::jsonb, $10, $11, $12, $13, $14, $15, $16
		)`,
		artifact.ArtifactID,
		artifact.OrgID,
		artifact.OwnerAgentID,
		artifact.OwnerUserID,
		artifact.Type,
		artifact.Title,
		artifact.Content,
		structuredPayload,
		sourceRefs,
		artifact.VisibilityMode,
		artifact.Sensitivity,
		artifact.Confidence,
		artifact.ApprovalState,
		artifact.CreatedAt,
		artifact.ExpiresAt,
		nullStringPtr(artifact.SupersedesArtifactID),
	)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("insert artifact: %w", err)
	}
	return artifact, nil
}

func (s *Store) FindArtifactByID(ctx context.Context, artifactID string) (core.Artifact, bool, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT artifact_id, org_id, owner_agent_id, owner_user_id, type, title, content, structured_payload,
		        source_refs, visibility_mode, sensitivity, confidence, approval_state, created_at, expires_at, supersedes_artifact_id
		FROM artifacts
		WHERE artifact_id = $1`,
		artifactID,
	)
	artifact, err := scanArtifact(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Artifact{}, false, nil
		}
		return core.Artifact{}, false, fmt.Errorf("find artifact by id: %w", err)
	}
	return artifact, true, nil
}

func (s *Store) ListArtifactsByOwner(ctx context.Context, userID string) ([]core.Artifact, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT artifact_id, org_id, owner_agent_id, owner_user_id, type, title, content, structured_payload,
		        source_refs, visibility_mode, sensitivity, confidence, approval_state, created_at, expires_at, supersedes_artifact_id
		FROM artifacts
		WHERE owner_user_id = $1
		ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query artifacts by owner: %w", err)
	}
	defer rows.Close()

	artifacts := make([]core.Artifact, 0)
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts by owner: %w", err)
	}

	return artifacts, nil
}

func scanArtifact(scanner interface{ Scan(dest ...any) error }) (core.Artifact, error) {
	var (
		artifact      core.Artifact
		payload       []byte
		sourceRefs    []byte
		expiresAt     sql.NullTime
		supersedesRef sql.NullString
	)

	if err := scanner.Scan(
		&artifact.ArtifactID,
		&artifact.OrgID,
		&artifact.OwnerAgentID,
		&artifact.OwnerUserID,
		&artifact.Type,
		&artifact.Title,
		&artifact.Content,
		&payload,
		&sourceRefs,
		&artifact.VisibilityMode,
		&artifact.Sensitivity,
		&artifact.Confidence,
		&artifact.ApprovalState,
		&artifact.CreatedAt,
		&expiresAt,
		&supersedesRef,
	); err != nil {
		return core.Artifact{}, fmt.Errorf("scan artifact: %w", err)
	}

	if err := unmarshalJSON(payload, &artifact.StructuredPayload); err != nil {
		return core.Artifact{}, fmt.Errorf("decode artifact structured payload: %w", err)
	}
	if artifact.StructuredPayload == nil {
		artifact.StructuredPayload = map[string]any{}
	}
	if err := unmarshalJSON(sourceRefs, &artifact.SourceRefs); err != nil {
		return core.Artifact{}, fmt.Errorf("decode artifact source refs: %w", err)
	}
	if expiresAt.Valid {
		artifact.ExpiresAt = &expiresAt.Time
	}
	if supersedesRef.Valid {
		artifact.SupersedesArtifactID = &supersedesRef.String
	}
	return artifact, nil
}

func nullStringPtr(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return nullString(*value)
}
