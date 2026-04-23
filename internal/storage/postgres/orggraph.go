package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SaveTeam(ctx context.Context, team core.Team) (core.Team, error) {
	var parent sql.NullString
	if team.ParentTeamID != "" {
		parent = sql.NullString{String: team.ParentTeamID, Valid: true}
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO teams (team_id, org_id, name, parent_team_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (team_id) DO UPDATE
		SET name = EXCLUDED.name, parent_team_id = EXCLUDED.parent_team_id`,
		team.TeamID, team.OrgID, team.Name, parent, team.CreatedAt,
	)
	if err != nil {
		return core.Team{}, fmt.Errorf("save team: %w", err)
	}
	return team, nil
}

func (s *Store) FindTeamByID(ctx context.Context, teamID string) (core.Team, bool, error) {
	team, err := scanTeam(s.db.QueryRowContext(
		ctx,
		`SELECT team_id, org_id, name, parent_team_id, created_at FROM teams WHERE team_id = $1`,
		teamID,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Team{}, false, nil
		}
		return core.Team{}, false, fmt.Errorf("find team: %w", err)
	}
	return team, true, nil
}

func (s *Store) ListTeamsForOrg(ctx context.Context, orgID string, limit, offset int) ([]core.Team, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT team_id, org_id, name, parent_team_id, created_at
		FROM teams WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	teams := make([]core.Team, 0)
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
}

func (s *Store) DeleteTeam(ctx context.Context, teamID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE team_id = $1`, teamID)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrTeamNotFound
	}
	return nil
}

func (s *Store) SaveTeamMember(ctx context.Context, m core.TeamMember) error {
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO team_members (team_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		m.TeamID, m.UserID, string(m.Role), m.JoinedAt,
	); err != nil {
		return fmt.Errorf("save team member: %w", err)
	}
	return nil
}

func (s *Store) DeleteTeamMember(ctx context.Context, teamID, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID)
	if err != nil {
		return fmt.Errorf("delete team member: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrTeamMemberNotFound
	}
	return nil
}

func (s *Store) ListTeamMembers(ctx context.Context, teamID string, limit, offset int) ([]core.TeamMember, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT team_id, user_id, role, joined_at FROM team_members
		WHERE team_id = $1 ORDER BY joined_at, user_id LIMIT $2 OFFSET $3`,
		teamID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	defer rows.Close()

	members := make([]core.TeamMember, 0)
	for rows.Next() {
		var (
			m    core.TeamMember
			role string
		)
		if err := rows.Scan(&m.TeamID, &m.UserID, &role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan team member: %w", err)
		}
		m.Role = core.TeamMemberRole(role)
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *Store) ListTeamsForUser(ctx context.Context, userID string) ([]core.Team, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT t.team_id, t.org_id, t.name, t.parent_team_id, t.created_at
		FROM teams t JOIN team_members tm ON tm.team_id = t.team_id
		WHERE tm.user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list teams for user: %w", err)
	}
	defer rows.Close()

	teams := make([]core.Team, 0)
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
}

func (s *Store) UsersShareTeam(ctx context.Context, userAID, userBID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1 FROM team_members a
			JOIN team_members b ON a.team_id = b.team_id
			WHERE a.user_id = $1 AND b.user_id = $2
		)`,
		userAID, userBID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check shared team: %w", err)
	}
	return exists, nil
}

// SaveManagerEdge revokes any prior active edge for the user and inserts
// the new one atomically. A unique partial index on (user_id) WHERE
// revoked_at IS NULL enforces the invariant at the DB level; the revoke
// step exists so we never fail the INSERT with a constraint violation on
// healthy data.
func (s *Store) SaveManagerEdge(ctx context.Context, edge core.ManagerEdge) (core.ManagerEdge, error) {
	tx, err := s.rawDB.BeginTx(ctx, nil)
	if err != nil {
		return core.ManagerEdge{}, fmt.Errorf("begin save-manager-edge tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE manager_edges SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`,
		edge.UserID, edge.EffectiveAt,
	); err != nil {
		return core.ManagerEdge{}, fmt.Errorf("revoke prior manager edge: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO manager_edges (edge_id, user_id, manager_user_id, effective_at, revoked_at)
		VALUES ($1, $2, $3, $4, NULL)`,
		edge.EdgeID, edge.UserID, edge.ManagerUserID, edge.EffectiveAt,
	); err != nil {
		return core.ManagerEdge{}, fmt.Errorf("insert manager edge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return core.ManagerEdge{}, fmt.Errorf("commit save-manager-edge tx: %w", err)
	}
	return edge, nil
}

func (s *Store) RevokeCurrentManagerEdge(ctx context.Context, userID string, revokedAt time.Time) error {
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE manager_edges SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID, revokedAt,
	); err != nil {
		return fmt.Errorf("revoke manager edge: %w", err)
	}
	return nil
}

func (s *Store) FindCurrentManagerEdge(ctx context.Context, userID string) (core.ManagerEdge, bool, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT edge_id, user_id, manager_user_id, effective_at, revoked_at
		FROM manager_edges WHERE user_id = $1 AND revoked_at IS NULL`,
		userID,
	)
	edge, err := scanManagerEdge(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.ManagerEdge{}, false, nil
		}
		return core.ManagerEdge{}, false, fmt.Errorf("find current manager edge: %w", err)
	}
	return edge, true, nil
}

func (s *Store) WalkManagerChain(ctx context.Context, userID string, maxDepth int) ([]core.ManagerEdge, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}
	chain := make([]core.ManagerEdge, 0, maxDepth)
	seen := make(map[string]struct{}, maxDepth+1)
	seen[userID] = struct{}{}
	current := userID
	for i := 0; i < maxDepth; i++ {
		edge, ok, err := s.FindCurrentManagerEdge(ctx, current)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if _, loop := seen[edge.ManagerUserID]; loop {
			break
		}
		chain = append(chain, edge)
		seen[edge.ManagerUserID] = struct{}{}
		current = edge.ManagerUserID
	}
	return chain, nil
}

func scanTeam(row rowScanner) (core.Team, error) {
	var (
		team   core.Team
		parent sql.NullString
	)
	if err := row.Scan(&team.TeamID, &team.OrgID, &team.Name, &parent, &team.CreatedAt); err != nil {
		return core.Team{}, err
	}
	if parent.Valid {
		team.ParentTeamID = parent.String
	}
	return team, nil
}

func scanManagerEdge(row rowScanner) (core.ManagerEdge, error) {
	var (
		edge    core.ManagerEdge
		revoked sql.NullTime
	)
	if err := row.Scan(&edge.EdgeID, &edge.UserID, &edge.ManagerUserID, &edge.EffectiveAt, &revoked); err != nil {
		return core.ManagerEdge{}, err
	}
	if revoked.Valid {
		t := revoked.Time
		edge.RevokedAt = &t
	}
	return edge, nil
}
