package server

import (
	"fmt"
	"time"
)

// These windows are the instance-list contract. The console mirrors the active
// window in lib/admin-views.ts; DOOPS_GATEWAY_TUNNEL.md is the authoritative
// description.
const (
	// ActiveInstanceWindow keeps an offline instance in the default list.
	// Exactly this age is still active; anything older is history.
	ActiveInstanceWindow = 7 * 24 * time.Hour
	// OfflineInstanceRetention is how long an offline instance record stays
	// in agent_status before automatic deletion. Exactly this age is kept.
	OfflineInstanceRetention = 90 * 24 * time.Hour
)

func meaningfulInstanceTime(t time.Time) bool {
	return !t.IsZero() && t.Year() > 1
}

// agentInstanceActive reports whether an instance belongs in the default list.
// A live connection is active even when its heartbeat timestamp is missing.
// An offline record with no heartbeat is history, not active.
func agentInstanceActive(status string, lastSeen time.Time, now time.Time) bool {
	if status == "online" {
		return true
	}
	if !meaningfulInstanceTime(lastSeen) {
		return false
	}
	cutoff := now.UTC().Add(-ActiveInstanceWindow)
	return !lastSeen.UTC().Before(cutoff)
}

// offlineInstanceExpired reports whether an offline agent_status row is past
// the retention window. Online rows are never expired. A missing last_seen
// falls back to updated_at; if both are missing the row is kept.
func offlineInstanceExpired(agent AgentStatus, now time.Time) bool {
	if agent.Status == "online" {
		return false
	}
	seen := agent.LastSeen
	if !meaningfulInstanceTime(seen) {
		seen = agent.UpdatedAt
	}
	if !meaningfulInstanceTime(seen) {
		return false
	}
	cutoff := now.UTC().Add(-OfflineInstanceRetention)
	return seen.UTC().Before(cutoff)
}

func parseInstanceInclude(raw string) (bool, error) {
	switch raw {
	case "":
		return false, nil
	case "history":
		return true, nil
	default:
		return false, fmt.Errorf("invalid include")
	}
}

// PruneOfflineAgentStatus deletes offline instance records older than the
// retention window. It does not touch grants, tokens, credential bindings,
// audit events, proxy routes, or edge routes. A row that is online at delete
// time is left in place, including one that reconnected after it was listed.
func (s *GatewayStore) PruneOfflineAgentStatus(now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("gateway store is required")
	}
	agents, err := s.ListAgentStatus()
	if err != nil {
		return 0, err
	}
	var deleted int64
	for _, agent := range agents {
		if !offlineInstanceExpired(agent, now) {
			continue
		}
		res, err := s.db.Exec(`DELETE FROM agent_status
			WHERE cluster = ? AND instance = ? AND status = 'offline'`,
			agent.Cluster, agent.Instance)
		if err != nil {
			return deleted, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	return deleted, nil
}
