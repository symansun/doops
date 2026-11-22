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
	Cluster     string
	Instance    string
	TokenID     string
	Remote      string
	Status      string
	Generation  uint64
	ConnectedAt time.Time
	LastSeen    time.Time
	UpdatedAt   time.Time
	ArchivedAt  time.Time
	ArchivedBy  string
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
	_, err := s.db.Exec(`INSERT INTO agent_status
		(cluster, instance, token_id, remote, status, generation, connected_at, last_seen, updated_at)
		VALUES (?, ?, ?, ?, 'online', ?, ?, ?, ?)
		ON CONFLICT(cluster, instance) DO UPDATE SET
			token_id = excluded.token_id,
			remote = excluded.remote,
			status = 'online',
			generation = excluded.generation,
			connected_at = excluded.connected_at,
			last_seen = excluded.last_seen,
			updated_at = excluded.updated_at,
			archived_at = '',
			archived_by = ''`,
		agent.Cluster, agent.Instance, agent.TokenID, agent.Remote, agent.Generation,
		formatTime(agent.ConnectedAt.UTC()), formatTime(agent.LastSeen.UTC()), formatTime(now))
	return err
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
	_, err := s.db.Exec(`UPDATE agent_status
		SET status = 'offline', last_seen = ?, updated_at = ?
		WHERE cluster = ? AND instance = ? AND generation = ?`,
		formatTime(lastSeen.UTC()), formatTime(now), cluster, instance, generation)
	return err
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

const agentStatusColumns = `cluster, instance, token_id, remote, status, generation, connected_at, last_seen, updated_at,
	archived_at, archived_by`

func scanAgentStatus(row interface{ Scan(dest ...any) error }) (AgentStatus, error) {
	var agent AgentStatus
	var connected, lastSeen, updated, archived string
	if err := row.Scan(&agent.Cluster, &agent.Instance, &agent.TokenID, &agent.Remote,
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
	rows, err := s.db.Query(`SELECT ` + agentStatusColumns + ` FROM agent_status ORDER BY cluster, instance`)
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
