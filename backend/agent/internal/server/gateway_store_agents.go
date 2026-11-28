package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

var (
	ErrAgentStatusNotFound = errors.New("instance_not_found")
	ErrAgentStatusOnline   = errors.New("instance_online")
)

type AgentStatus struct {
	Cluster             string
	Instance            string
	TokenID             string
	Remote              string
	ReleaseCommit       string
	Status              string
	Generation          uint64
	Mode                string
	KubernetesAvailable *bool
	resourceSync        bool // capability advertised on the connection being registered
	ConnectedAt         time.Time
	LastSeen            time.Time
	UpdatedAt           time.Time
	ArchivedAt          time.Time
	ArchivedBy          string
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
	var kubernetesAvailable any
	if agent.KubernetesAvailable != nil {
		kubernetesAvailable = *agent.KubernetesAvailable
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO agent_status\n\t\t(cluster, instance, token_id, remote, release_commit, status, generation, mode, kubernetes_available, connected_at, last_seen, updated_at)\n\t\tVALUES (?, ?, ?, ?, ?, 'online', ?, ?, ?, ?, ?, ?)", target: []string{"cluster", "instance"}, set: []string{"token_id = excluded.token_id", "remote = excluded.remote", "release_commit = CASE WHEN excluded.release_commit = '' THEN agent_status.release_commit ELSE excluded.release_commit END", "status = 'online'", "generation = excluded.generation", "mode = excluded.mode", "kubernetes_available = excluded.kubernetes_available", "connected_at = excluded.connected_at", "last_seen = excluded.last_seen", "updated_at = excluded.updated_at", "archived_at = ''", "archived_by = ''"}}),
		agent.Cluster, agent.Instance, agent.TokenID, agent.Remote, agent.ReleaseCommit, agent.Generation,
		agent.Mode, kubernetesAvailable, formatTime(connectedUTC), formatTime(agent.LastSeen.UTC()), formatTime(now))
	if err != nil {
		return err
	}
	// A replacement connection has its own protocol support, freshness and
	// acknowledgement. Keep the desired revision and observation history.
	_, err = tx.Exec(gatewayUpsertSQL(gatewayUpsert{
		insert: `INSERT INTO agent_resource_state(cluster,instance,supported,connection_at) VALUES(?,?,?,?)`,
		target: []string{"cluster", "instance"},
		set:    []string{"supported = excluded.supported", "connection_at = excluded.connection_at", "last_observed_at = ''", "applied_revision = 0"},
	}), agent.Cluster, agent.Instance, agent.resourceSync, formatTime(connectedUTC))
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
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
	s.enqueueAgentConnectionEvent(agent.Cluster, agent.Instance, agent.Generation, "connected", agent.ReleaseCommit, agent.Mode, enrolledInt == 1, connectedUTC)
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
		var remote, releaseCommit, tokenID, mode string
		_ = s.db.QueryRow(`SELECT remote, release_commit, token_id, mode FROM agent_status WHERE cluster = ? AND instance = ?`,
			cluster, instance).Scan(&remote, &releaseCommit, &tokenID, &mode)
		enrolledInt := 0
		if tokenID != "" {
			enrolledInt = 1
		}
		_, _ = s.db.Exec(`INSERT INTO agent_connection_events
			(cluster, instance, generation, event, release_commit, remote, enrolled, occurred_at_ms, occurred_at)
			VALUES (?, ?, ?, 'offline', ?, ?, ?, ?, ?)`,
			cluster, instance, generation, releaseCommit, remote,
			enrolledInt, seenUTC.UnixMilli(), formatTime(seenUTC))
		s.enqueueAgentConnectionEvent(cluster, instance, generation, "disconnected", releaseCommit, mode, enrolledInt == 1, seenUTC)
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

type AgentHeartbeatUpdate struct {
	Cluster    string
	Instance   string
	Generation uint64
	LastSeen   time.Time
}

// TouchAgentsContext persists heartbeats for all supplied live agent sessions.
// Updates are sorted deterministically by (cluster, instance) to prevent
// cross-transaction InnoDB row-lock deadlocks. On MySQL, updates are sent in
// single-statement batches (up to 100 agents per UPDATE) so N online agents
// consume 1 WAN round-trip instead of N+4.
func (s *GatewayStore) TouchAgentsContext(ctx context.Context, updates []AgentHeartbeatUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	ordered := append([]AgentHeartbeatUpdate(nil), updates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Cluster != ordered[j].Cluster {
			return ordered[i].Cluster < ordered[j].Cluster
		}
		if ordered[i].Instance != ordered[j].Instance {
			return ordered[i].Instance < ordered[j].Instance
		}
		return ordered[i].Generation < ordered[j].Generation
	})
	now := formatTime(time.Now().UTC())
	if s.dialect == gatewayDialectMySQL {
		return s.touchAgentsMySQL(ctx, ordered, now)
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
	for _, u := range ordered {
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

const touchAgentsMySQLBatchSize = 100

func touchAgentsMySQLBatchSQL(count int) string {
	var b strings.Builder
	b.Grow(160 + count*72)
	b.WriteString("UPDATE agent_status SET last_seen = CASE")
	for i := 0; i < count; i++ {
		b.WriteString(" WHEN cluster = ? AND instance = ? AND generation = ? THEN ?")
	}
	b.WriteString(" ELSE last_seen END, updated_at = ? WHERE status = 'online' AND (cluster, instance, generation) IN (")
	for i := 0; i < count; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(?,?,?)")
	}
	b.WriteByte(')')
	return b.String()
}

func (s *GatewayStore) touchAgentsMySQL(ctx context.Context, ordered []AgentHeartbeatUpdate, now string) error {
	for start := 0; start < len(ordered); start += touchAgentsMySQLBatchSize {
		end := start + touchAgentsMySQLBatchSize
		if end > len(ordered) {
			end = len(ordered)
		}
		chunk := ordered[start:end]
		args := make([]any, 0, len(chunk)*7+1)
		for _, u := range chunk {
			lastSeen := u.LastSeen
			if lastSeen.IsZero() {
				lastSeen = time.Now().UTC()
			}
			args = append(args, u.Cluster, u.Instance, u.Generation, formatTime(lastSeen.UTC()))
		}
		args = append(args, now)
		for _, u := range chunk {
			args = append(args, u.Cluster, u.Instance, u.Generation)
		}
		if _, err := s.db.ExecContext(ctx, touchAgentsMySQLBatchSQL(len(chunk)), args...); err != nil {
			return err
		}
	}
	return nil
}

const agentStatusColumns = `cluster, instance, token_id, remote, release_commit, status, generation, mode, kubernetes_available, connected_at, last_seen, updated_at,
	archived_at, archived_by`

func scanAgentStatus(row interface{ Scan(dest ...any) error }) (AgentStatus, error) {
	var agent AgentStatus
	var connected, lastSeen, updated, archived, mode string
	var kubernetesAvailable sql.NullBool
	if err := row.Scan(&agent.Cluster, &agent.Instance, &agent.TokenID, &agent.Remote, &agent.ReleaseCommit,
		&agent.Status, &agent.Generation, &mode, &kubernetesAvailable, &connected, &lastSeen, &updated, &archived, &agent.ArchivedBy); err != nil {
		return AgentStatus{}, err
	}
	agent.Mode = mode
	if kubernetesAvailable.Valid {
		value := kubernetesAvailable.Bool
		agent.KubernetesAvailable = &value
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
	_, err := s.db.Exec(gatewayUpsertSQL(gatewayUpsert{insert: "INSERT INTO platform_load_samples\n\t\t(at_ms, online_agents, busy_agents, active_ops, queued_ops, active_sessions, resource_locks)\n\t\tVALUES (?, ?, ?, ?, ?, ?, ?)", target: []string{"at_ms"}, set: []string{"online_agents = MAX(platform_load_samples.online_agents, excluded.online_agents)", "busy_agents = MAX(platform_load_samples.busy_agents, excluded.busy_agents)", "active_ops = MAX(platform_load_samples.active_ops, excluded.active_ops)", "queued_ops = MAX(platform_load_samples.queued_ops, excluded.queued_ops)", "active_sessions = MAX(platform_load_samples.active_sessions, excluded.active_sessions)", "resource_locks = MAX(platform_load_samples.resource_locks, excluded.resource_locks)"}}),
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
//
// MySQL counts changed rows, not matched rows, for UPDATE: repeating this call
// on a record whose archived_at is already empty matches the row but changes
// nothing, so affected == 0 alone cannot mean "absent" (INT-61). A zero count is
// re-checked with an existence SELECT in the same transaction; a record that is
// still there takes the normal success path, and only a missing one returns
// ErrAgentStatusNotFound.
func (s *GatewayStore) UnarchiveAgentStatus(cluster, instance string) (AgentStatus, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AgentStatus{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE agent_status SET archived_at = '', archived_by = ''
		WHERE cluster = ? AND instance = ?`, cluster, instance)
	if err != nil {
		return AgentStatus{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return AgentStatus{}, err
	}
	if affected == 0 {
		var present int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM agent_status WHERE cluster = ? AND instance = ?`,
			cluster, instance).Scan(&present); err != nil {
			return AgentStatus{}, err
		}
		if present == 0 {
			return AgentStatus{}, ErrAgentStatusNotFound
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentStatus{}, err
	}
	return s.agentStatus(s.db, cluster, instance)
}

type AgentRemainingReferences struct {
	ActiveCredentialGrants   int `json:"active_credential_grants"`
	RevokingCredentialGrants int `json:"revoking_credential_grants"`
	ProxyRoutes              int `json:"proxy_routes"`
	UserGrants               int `json:"user_grants"`
}

type AgentDecommissionResult struct {
	Cluster   string                   `json:"cluster"`
	Instance  string                   `json:"instance"`
	Deleted   bool                     `json:"deleted"`
	Remaining AgentRemainingReferences `json:"remaining"`
}

// DeleteOfflineAgentStatus explicitly deletes an offline instance record from
// agent_status without removing authority records (user grants, credential
// bindings, proxy routes, tokens, or audit history). It returns counts of
// remaining references pointing at the deleted target so operators can clean up
// orphaned bindings intentionally.
func (s *GatewayStore) DeleteOfflineAgentStatus(cluster, instance string) (AgentDecommissionResult, error) {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if cluster == "" || instance == "" {
		return AgentDecommissionResult{}, fmt.Errorf("cluster and instance are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AgentDecommissionResult{}, err
	}
	defer tx.Rollback()
	agent, err := s.agentStatus(tx, cluster, instance)
	if err != nil {
		return AgentDecommissionResult{}, err
	}
	if agent.Status == "online" {
		return AgentDecommissionResult{}, ErrAgentStatusOnline
	}
	res, err := tx.Exec(`DELETE FROM agent_status WHERE cluster = ? AND instance = ? AND status = 'offline'`,
		cluster, instance)
	if err != nil {
		return AgentDecommissionResult{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return AgentDecommissionResult{}, err
	}
	if affected == 0 {
		current, err := s.agentStatus(tx, cluster, instance)
		if err != nil {
			return AgentDecommissionResult{}, err
		}
		if current.Status == "online" {
			return AgentDecommissionResult{}, ErrAgentStatusOnline
		}
		return AgentDecommissionResult{}, ErrAgentStatusNotFound
	}
	remaining, err := countAgentRemainingReferences(tx, cluster, instance)
	if err != nil {
		return AgentDecommissionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentDecommissionResult{}, err
	}
	return AgentDecommissionResult{
		Cluster:   cluster,
		Instance:  instance,
		Deleted:   true,
		Remaining: remaining,
	}, nil
}

func countAgentRemainingReferences(tx *sql.Tx, cluster, instance string) (AgentRemainingReferences, error) {
	var remaining AgentRemainingReferences
	rows, err := tx.Query(`SELECT state, scope_json FROM credential_grants WHERE state IN (?, ?)`,
		CredentialStateActive, CredentialStateRevoking)
	if err != nil {
		return AgentRemainingReferences{}, err
	}
	for rows.Next() {
		var state CredentialState
		var rawScope string
		if err := rows.Scan(&state, &rawScope); err != nil {
			_ = rows.Close()
			return AgentRemainingReferences{}, err
		}
		var scope struct {
			Cluster  string `json:"cluster"`
			Instance string `json:"instance"`
		}
		if err := json.Unmarshal([]byte(rawScope), &scope); err != nil {
			continue
		}
		if strings.TrimSpace(scope.Cluster) != cluster || strings.TrimSpace(scope.Instance) != instance {
			continue
		}
		if state == CredentialStateActive {
			remaining.ActiveCredentialGrants++
		} else if state == CredentialStateRevoking {
			remaining.RevokingCredentialGrants++
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return AgentRemainingReferences{}, err
	}
	if err := rows.Close(); err != nil {
		return AgentRemainingReferences{}, err
	}
	if err := tx.QueryRow(`SELECT COUNT(1) FROM proxy_routes WHERE cluster = ? AND instance = ?`,
		cluster, instance).Scan(&remaining.ProxyRoutes); err != nil {
		return AgentRemainingReferences{}, err
	}
	if err := tx.QueryRow(`SELECT COUNT(1) FROM grants WHERE cluster = ? AND instance = ?`,
		cluster, instance).Scan(&remaining.UserGrants); err != nil {
		return AgentRemainingReferences{}, err
	}
	return remaining, nil
}

// enqueueAgentConnectionEvent publishes a metadata-only agent lifecycle event.
// The row transition is already committed; a webhook enqueue failure must not
// make the Agent connection itself fail, so this is best-effort and logged.
func (s *GatewayStore) enqueueAgentConnectionEvent(cluster, instance string, generation uint64, state, releaseCommit, mode string, enrolled bool, at time.Time) {
	eventType := "agent.disconnected"
	if state == "connected" {
		eventType = "agent.connected"
	}
	ev, err := newPlatformEvent(eventType, at, "", cluster, instance, "", map[string]any{
		"cluster":        cluster,
		"instance":       instance,
		"generation":     generation,
		"release_commit": releaseCommit,
		"mode":           mode,
		"enrolled":       enrolled,
	}, fmt.Sprintf("agent:%s:%s:%d:%s", cluster, instance, generation, state))
	if err != nil {
		log.Printf("[gateway] webhook event %s for %s/%s could not be built: %v", eventType, cluster, instance, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := s.EnqueuePlatformEvent(ctx, ev); err != nil {
		log.Printf("[gateway] webhook event %s for %s/%s could not be enqueued: %v", eventType, cluster, instance, err)
	}
}
