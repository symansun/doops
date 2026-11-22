package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrAgentStatusNotFound = errors.New("instance_not_found")
	ErrAgentStatusOnline   = errors.New("instance_online")
)

type AgentStatus struct {
	Cluster       string
	Instance      string
	TokenID       string
	Remote        string
	ReleaseCommit string
	Status        string
	Generation    uint64
	ConnectedAt   time.Time
	LastSeen      time.Time
	UpdatedAt     time.Time
	ArchivedAt    time.Time
	ArchivedBy    string
}

type AgentConnectionEvent struct {
	ID            int64
	Cluster       string
	Instance      string
	Generation    uint64
	Event         string
	ReleaseCommit string
	Remote        string
	Enrolled      bool
	OccurredAtMs  int64
	OccurredAt    time.Time
}

type PlatformLoadSample struct {
	AtMs           int64 `json:"at_ms"`
	OnlineAgents   int   `json:"online_agents"`
	BusyAgents     int   `json:"busy_agents"`
	ActiveOps      int   `json:"active_ops"`
	QueuedOps      int   `json:"queued_ops"`
	ActiveSessions int   `json:"active_sessions"`
	ResourceLocks  int   `json:"resource_locks"`
}

func (s *GatewayStore) MarkAgentOnline(agent AgentStatus) error {
	if strings.TrimSpace(agent.Cluster) == "" || strings.TrimSpace(agent.Instance) == "" {
		return fmt.Errorf("cluster and instance are required")
	}
	now := time.Now().UTC()
	if agent.ConnectedAt.IsZero() {
		agent.ConnectedAt = now
	}
	if agent.LastSeen.IsZero() {
		agent.LastSeen = now
	}
	connectedUTC := agent.ConnectedAt.UTC()
	_, err := s.db.Exec(`INSERT INTO agent_status
		(cluster, instance, token_id, remote, release_commit, status, generation, connected_at, last_seen, updated_at)
		VALUES (?, ?, ?, ?, ?, 'online', ?, ?, ?, ?)
		ON CONFLICT(cluster, instance) DO UPDATE SET
			token_id = excluded.token_id,
			remote = excluded.remote,
			release_commit = CASE WHEN excluded.release_commit = '' THEN agent_status.release_commit ELSE excluded.release_commit END,
			status = 'online',
			generation = excluded.generation,
			connected_at = excluded.connected_at,
			last_seen = excluded.last_seen,
			updated_at = excluded.updated_at,
			archived_at = '',
			archived_by = ''`,
		agent.Cluster, agent.Instance, agent.TokenID, agent.Remote, agent.ReleaseCommit, agent.Generation,
		formatTime(connectedUTC), formatTime(agent.LastSeen.UTC()), formatTime(now))
	if err != nil {
		return err
	}
	enrolledInt := 0
	if agent.TokenID != "" {
		enrolledInt = 1
	}
	_, _ = s.db.Exec(`INSERT INTO agent_connection_events
		(cluster, instance, generation, event, release_commit, remote, enrolled, occurred_at_ms, occurred_at)
		VALUES (?, ?, ?, 'online', ?, ?, ?, ?, ?)`,
		agent.Cluster, agent.Instance, agent.Generation, agent.ReleaseCommit, agent.Remote,
		enrolledInt, connectedUTC.UnixMilli(), formatTime(connectedUTC))
	return nil
}

func (s *GatewayStore) MarkAgentOffline(
	cluster,
	instance string,
	generation uint64,
	lastSeen time.Time,
) error {
	now := time.Now().UTC()
	if lastSeen.IsZero() {
		lastSeen = now
	}
	seenUTC := lastSeen.UTC()
	res, err := s.db.Exec(`UPDATE agent_status
		SET status = 'offline', last_seen = ?, updated_at = ?
		WHERE cluster = ? AND instance = ? AND generation = ?`,
		formatTime(seenUTC), formatTime(now), cluster, instance, generation)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		var remote, releaseCommit, tokenID string
		_ = s.db.QueryRow(`SELECT remote, release_commit, token_id FROM agent_status WHERE cluster = ? AND instance = ?`,
			cluster, instance).Scan(&remote, &releaseCommit, &tokenID)
		enrolledInt := 0
		if tokenID != "" {
			enrolledInt = 1
		}
		_, _ = s.db.Exec(`INSERT INTO agent_connection_events
			(cluster, instance, generation, event, release_commit, remote, enrolled, occurred_at_ms, occurred_at)
			VALUES (?, ?, ?, 'offline', ?, ?, ?, ?, ?)`,
			cluster, instance, generation, releaseCommit, remote,
			enrolledInt, seenUTC.UnixMilli(), formatTime(seenUTC))
	}
	return nil
}

func (s *GatewayStore) MarkAllAgentsOffline() error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE agent_status
		SET status = 'offline', updated_at = ?
		WHERE status = 'online'`,
		formatTime(now))
	return err
}

func (s *GatewayStore) TouchAgentContext(
	ctx context.Context,
	cluster,
	instance string,
	generation uint64,
	lastSeen time.Time,
) error {
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE agent_status
		SET last_seen = ?, updated_at = ?
		WHERE cluster = ? AND instance = ? AND generation = ? AND status = 'online'`,
		formatTime(lastSeen.UTC()), formatTime(time.Now().UTC()), cluster, instance, generation)
	return err
}

type AgentHeartbeatUpdate struct {
	Cluster    string
	Instance   string
	Generation uint64
	LastSeen   time.Time
}

// TouchAgentsContext persists heartbeats for all supplied live agent sessions
// in a single SQLite write transaction so N online agents produce one write
// transaction per flush interval instead of N separate writes.
func (s *GatewayStore) TouchAgentsContext(ctx context.Context, updates []AgentHeartbeatUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE agent_status
		SET last_seen = ?, updated_at = ?
		WHERE cluster = ? AND instance = ? AND generation = ? AND status = 'online'`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := formatTime(time.Now().UTC())
	for _, u := range updates {
		lastSeen := u.LastSeen
		if lastSeen.IsZero() {
			lastSeen = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx, formatTime(lastSeen.UTC()), now, u.Cluster, u.Instance, u.Generation); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const agentStatusColumns = `cluster, instance, token_id, remote, release_commit, status, generation, connected_at, last_seen, updated_at,
	archived_at, archived_by`

func scanAgentStatus(row interface{ Scan(dest ...any) error }) (AgentStatus, error) {
	var agent AgentStatus
	var connected, lastSeen, updated, archived string
	if err := row.Scan(&agent.Cluster, &agent.Instance, &agent.TokenID, &agent.Remote, &agent.ReleaseCommit,
		&agent.Status, &agent.Generation, &connected, &lastSeen, &updated, &archived, &agent.ArchivedBy); err != nil {
		return AgentStatus{}, err
	}
	if connected != "" {
		agent.ConnectedAt, _ = parseTime(connected)
	}
	if lastSeen != "" {
		agent.LastSeen, _ = parseTime(lastSeen)
	}
	if updated != "" {
		agent.UpdatedAt, _ = parseTime(updated)
	}
	if archived != "" {
		agent.ArchivedAt, _ = parseTime(archived)
	}
	return agent, nil
}

// ListAgentStatus returns every instance record, archived ones included; the
// registry needs all generations and callers filter what they display.
func (s *GatewayStore) ListAgentStatus() ([]AgentStatus, error) {
	rows, err := s.readDB().Query(`SELECT ` + agentStatusColumns + ` FROM agent_status ORDER BY cluster, instance`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []AgentStatus
	for rows.Next() {
		agent, err := scanAgentStatus(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *GatewayStore) RecordPlatformLoadSample(sample PlatformLoadSample) error {
	if s == nil || s.db == nil {
		return nil
	}
	if sample.AtMs <= 0 {
		sample.AtMs = time.Now().UTC().UnixMilli()
	}
	_, err := s.db.Exec(`INSERT INTO platform_load_samples
		(at_ms, online_agents, busy_agents, active_ops, queued_ops, active_sessions, resource_locks)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(at_ms) DO UPDATE SET
			online_agents = MAX(platform_load_samples.online_agents, excluded.online_agents),
			busy_agents = MAX(platform_load_samples.busy_agents, excluded.busy_agents),
			active_ops = MAX(platform_load_samples.active_ops, excluded.active_ops),
			queued_ops = MAX(platform_load_samples.queued_ops, excluded.queued_ops),
			active_sessions = MAX(platform_load_samples.active_sessions, excluded.active_sessions),
			resource_locks = MAX(platform_load_samples.resource_locks, excluded.resource_locks)`,
		sample.AtMs, sample.OnlineAgents, sample.BusyAgents, sample.ActiveOps,
		sample.QueuedOps, sample.ActiveSessions, sample.ResourceLocks)
	return err
}

func (s *GatewayStore) agentStatus(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, cluster, instance string) (AgentStatus, error) {
	agent, err := scanAgentStatus(q.QueryRow(`SELECT `+agentStatusColumns+`
		FROM agent_status WHERE cluster = ? AND instance = ?`, cluster, instance))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentStatus{}, ErrAgentStatusNotFound
	}
	return agent, err
}

// ArchiveAgentStatus hides an offline instance record from the default
// instance list. It only marks the record: grants, credential bindings,
// tokens and audit stay as they are, and MarkAgentOnline clears the mark.
// Archiving an archived record keeps its first archive time and actor.
func (s *GatewayStore) ArchiveAgentStatus(cluster, instance, actorID string, at time.Time) (AgentStatus, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AgentStatus{}, err
	}
	defer tx.Rollback()
	agent, err := s.agentStatus(tx, cluster, instance)
	if err != nil {
		return AgentStatus{}, err
	}
	if agent.Status == "online" {
		return AgentStatus{}, ErrAgentStatusOnline
	}
	if agent.ArchivedAt.IsZero() {
		agent.ArchivedAt, agent.ArchivedBy = at.UTC(), actorID
		if _, err := tx.Exec(`UPDATE agent_status SET archived_at = ?, archived_by = ?
			WHERE cluster = ? AND instance = ?`,
			formatTime(agent.ArchivedAt), agent.ArchivedBy, cluster, instance); err != nil {
			return AgentStatus{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentStatus{}, err
	}
	return agent, nil
}

// UnarchiveAgentStatus clears the archive mark; a record that is not archived
// is returned unchanged.
func (s *GatewayStore) UnarchiveAgentStatus(cluster, instance string) (AgentStatus, error) {
	res, err := s.db.Exec(`UPDATE agent_status SET archived_at = '', archived_by = ''
		WHERE cluster = ? AND instance = ?`, cluster, instance)
	if err != nil {
		return AgentStatus{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return AgentStatus{}, ErrAgentStatusNotFound
	}
	return s.agentStatus(s.db, cluster, instance)
}
