package memory

import (
	"context"
	"sort"
	"time"

	"alice/internal/core"
	"alice/internal/storage"
)

func (s *Store) SaveTeam(_ context.Context, team core.Team) (core.Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.teams[team.TeamID]; !ok {
		s.teamsByOrg[team.OrgID] = append(s.teamsByOrg[team.OrgID], team.TeamID)
	}
	s.teams[team.TeamID] = team
	if _, ok := s.teamMembers[team.TeamID]; !ok {
		s.teamMembers[team.TeamID] = make(map[string]core.TeamMember)
	}
	return team, nil
}

func (s *Store) FindTeamByID(_ context.Context, teamID string) (core.Team, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	team, ok := s.teams[teamID]
	return team, ok, nil
}

func (s *Store) ListTeamsForOrg(_ context.Context, orgID string, limit, offset int) ([]core.Team, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	ids := s.teamsByOrg[orgID]
	teams := make([]core.Team, 0, len(ids))
	for _, tid := range ids {
		if team, ok := s.teams[tid]; ok {
			teams = append(teams, team)
		}
	}
	sort.Slice(teams, func(i, j int) bool {
		return teams[i].CreatedAt.After(teams[j].CreatedAt)
	})
	if offset >= len(teams) {
		return []core.Team{}, nil
	}
	end := offset + limit
	if end > len(teams) {
		end = len(teams)
	}
	return teams[offset:end], nil
}

func (s *Store) DeleteTeam(_ context.Context, teamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	team, ok := s.teams[teamID]
	if !ok {
		return storage.ErrTeamNotFound
	}
	delete(s.teams, teamID)
	delete(s.teamMembers, teamID)
	ids := s.teamsByOrg[team.OrgID]
	filtered := ids[:0]
	for _, id := range ids {
		if id != teamID {
			filtered = append(filtered, id)
		}
	}
	s.teamsByOrg[team.OrgID] = filtered
	return nil
}

func (s *Store) SaveTeamMember(_ context.Context, member core.TeamMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.teams[member.TeamID]; !ok {
		return storage.ErrTeamNotFound
	}
	if s.teamMembers[member.TeamID] == nil {
		s.teamMembers[member.TeamID] = make(map[string]core.TeamMember)
	}
	s.teamMembers[member.TeamID][member.UserID] = member
	return nil
}

func (s *Store) DeleteTeamMember(_ context.Context, teamID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	members, ok := s.teamMembers[teamID]
	if !ok {
		return storage.ErrTeamNotFound
	}
	if _, ok := members[userID]; !ok {
		return storage.ErrTeamMemberNotFound
	}
	delete(members, userID)
	return nil
}

func (s *Store) ListTeamMembers(_ context.Context, teamID string, limit, offset int) ([]core.TeamMember, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	members, ok := s.teamMembers[teamID]
	if !ok {
		return nil, storage.ErrTeamNotFound
	}
	out := make([]core.TeamMember, 0, len(members))
	for _, m := range members {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].JoinedAt.Equal(out[j].JoinedAt) {
			return out[i].UserID < out[j].UserID
		}
		return out[i].JoinedAt.Before(out[j].JoinedAt)
	})
	if offset >= len(out) {
		return []core.TeamMember{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (s *Store) ListTeamsForUser(_ context.Context, userID string) ([]core.Team, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]core.Team, 0)
	for teamID, members := range s.teamMembers {
		if _, ok := members[userID]; !ok {
			continue
		}
		if team, ok := s.teams[teamID]; ok {
			out = append(out, team)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TeamID < out[j].TeamID })
	return out, nil
}

func (s *Store) UsersShareTeam(_ context.Context, userAID, userBID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, members := range s.teamMembers {
		if _, okA := members[userAID]; !okA {
			continue
		}
		if _, okB := members[userBID]; okB {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) SaveManagerEdge(_ context.Context, edge core.ManagerEdge) (core.ManagerEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if priorID, ok := s.managerByUser[edge.UserID]; ok && priorID != "" {
		prior := s.managerEdges[priorID]
		if prior.RevokedAt == nil {
			t := edge.EffectiveAt
			prior.RevokedAt = &t
			s.managerEdges[priorID] = prior
		}
	}
	s.managerEdges[edge.EdgeID] = edge
	s.managerByUser[edge.UserID] = edge.EdgeID
	return edge, nil
}

func (s *Store) RevokeCurrentManagerEdge(_ context.Context, userID string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	edgeID, ok := s.managerByUser[userID]
	if !ok || edgeID == "" {
		return nil
	}
	edge := s.managerEdges[edgeID]
	if edge.RevokedAt != nil {
		return nil
	}
	t := revokedAt
	edge.RevokedAt = &t
	s.managerEdges[edgeID] = edge
	s.managerByUser[userID] = ""
	return nil
}

func (s *Store) FindCurrentManagerEdge(_ context.Context, userID string) (core.ManagerEdge, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	edgeID, ok := s.managerByUser[userID]
	if !ok || edgeID == "" {
		return core.ManagerEdge{}, false, nil
	}
	edge, ok := s.managerEdges[edgeID]
	if !ok || edge.RevokedAt != nil {
		return core.ManagerEdge{}, false, nil
	}
	return edge, true, nil
}

func (s *Store) WalkManagerChain(_ context.Context, userID string, maxDepth int) ([]core.ManagerEdge, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	chain := make([]core.ManagerEdge, 0, maxDepth)
	seen := make(map[string]struct{}, maxDepth+1)
	seen[userID] = struct{}{}
	current := userID
	for i := 0; i < maxDepth; i++ {
		edgeID, ok := s.managerByUser[current]
		if !ok || edgeID == "" {
			break
		}
		edge, ok := s.managerEdges[edgeID]
		if !ok || edge.RevokedAt != nil {
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
