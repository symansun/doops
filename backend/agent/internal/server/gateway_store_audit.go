package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type AuditEvent struct {
	ID               int64
	ManifestRevision uint64
	ResourceRefs     []string
	UserID           string
	TokenID          string
	Cluster          string
	Instance         string
	CredentialID     string
	VersionID        string
	GrantID          string
	Scope            *CredentialGrantScope
	ResourceVersion  string
	Digest           string
	CorrelationID    string
	Action           GatewayAction
	Session          string
	CommandSummary   string
	Status           string
	Error            string
	Tail             string
	BytesIn          int64
	BytesOut         int64
	Model            string
	InputTokens      int64
	OutputTokens     int64
	TotalTokens      int64
	StartedAt        time.Time
	EndedAt          time.Time
}

type AuditFinish struct {
	Status          string
	Error           string
	Tail            string
	CommandSummary  string
	BytesIn         int64
	BytesOut        int64
	Model           string
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	Cluster         string
	Instance        string
	VersionID       string
	GrantID         string
	Scope           *CredentialGrantScope
	ResourceVersion string
	Digest          string
	EndedAt         time.Time
}

type AuditFilter struct {
	UserID             string
	Cluster            string
	Instance           string
	Session            string
	Action             GatewayAction
	Status             string
	Limit              int
	BeforeID           int64
	CorrelationID      string
	Since              time.Time
	Until              time.Time
	HideUpgradeObserve bool
	// VisibleToUser restricts a non-admin query to that user's own events and
	// events that reference a credential they own. Empty means no extra scope.
	VisibleToUser string
}

var errAuditTerminalConflict = errors.New("audit terminal state conflict")

func (s *GatewayStore) StartAudit(event AuditEvent) (int64, error) {
	if event.StartedAt.IsZero() {
		event.StartedAt = time.Now().UTC()
	}
	scopeJSON, err := credentialAuditScopeJSON(event.Scope)
	if err != nil {
		return 0, err
	}
	startedUTC := event.StartedAt.UTC()
	res, err := s.db.Exec(`INSERT INTO audit_events
		(user_id, token_id, cluster, instance, credential_id, credential_version_id,
		 credential_grant_id, scope_json, resource_version, digest, correlation_id, action,
		 session, command_summary, status, model, error, tail, ended_at, started_at_ms, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', ?, ?)`,
		event.UserID, event.TokenID, event.Cluster, event.Instance, event.CredentialID,
		event.VersionID, event.GrantID, scopeJSON, event.ResourceVersion, event.Digest,
		event.CorrelationID, string(event.Action),
		event.Session, strings.ToValidUTF8(event.CommandSummary, "\uFFFD"), firstNonEmpty(event.Status, "running"),
		strings.TrimSpace(event.Model), startedUTC.UnixMilli(), formatTime(startedUTC))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if s.auditDocStoreEnabled() {
		event.ID = id
		event.StartedAt = startedUTC
		event.CommandSummary = strings.ToValidUTF8(event.CommandSummary, "\uFFFD")
		event.Status = firstNonEmpty(event.Status, "running")
		event.Model = strings.TrimSpace(event.Model)
		s.rememberInFlightAudit(event)
	}
	return id, nil
}

func (s *GatewayStore) peekInFlightAudit(id int64) (AuditEvent, bool) {
	if s == nil || id <= 0 {
		return AuditEvent{}, false
	}
	s.auditDocMu.Lock()
	defer s.auditDocMu.Unlock()
	ev, ok := s.auditInFlight[id]
	return ev, ok
}

func normalizeAuditFinishDefaults(finish AuditFinish) AuditFinish {
	if finish.EndedAt.IsZero() {
		finish.EndedAt = time.Now().UTC()
	}
	if finish.Status == "" {
		if finish.Error != "" {
			finish.Status = "error"
		} else {
			finish.Status = "success"
		}
	}
	return finish
}

func (s *GatewayStore) prepareAuditDocFinish(id int64, finish AuditFinish) (AuditEvent, AuditFinish, string, error) {
	finish = normalizeAuditFinishDefaults(finish)
	ev, found := s.peekInFlightAudit(id)
	if !found {
		if loaded, err := s.loadAuditEventByID(id); err == nil {
			ev = loaded
		} else {
			ev = AuditEvent{ID: id, StartedAt: time.Now().UTC()}
		}
	}
	cleanError := strings.ToValidUTF8(utf8Tail(redactSecretText(finish.Error), 1200), "\uFFFD")
	cleanTail := strings.ToValidUTF8(trimTail(finish.Tail, 8192), "\uFFFD")

	dbFinish := finish
	var tailRelPath string
	if cleanTail != "" && auditActionExternalizesTail(ev.Action) {
		ptrJSON, relPath, err := s.writeAuditTailDoc(id, ev.StartedAt, cleanTail)
		if err != nil {
			return AuditEvent{}, AuditFinish{}, "", err
		}
		dbFinish.Tail = ptrJSON
		tailRelPath = relPath
	}

	ev.ID = id
	ev.Status = finish.Status
	ev.Error = cleanError
	ev.Tail = cleanTail
	ev.BytesIn = finish.BytesIn
	ev.BytesOut = finish.BytesOut
	if model := strings.TrimSpace(finish.Model); model != "" {
		ev.Model = model
	}
	if finish.InputTokens > 0 {
		ev.InputTokens = finish.InputTokens
	}
	if finish.OutputTokens > 0 {
		ev.OutputTokens = finish.OutputTokens
	}
	totalTokens := finish.TotalTokens
	if totalTokens == 0 && (finish.InputTokens > 0 || finish.OutputTokens > 0) {
		totalTokens = finish.InputTokens + finish.OutputTokens
	}
	if totalTokens > 0 {
		ev.TotalTokens = totalTokens
	}
	if finish.CommandSummary != "" {
		ev.CommandSummary = strings.ToValidUTF8(finish.CommandSummary, "\uFFFD")
	}
	if finish.Cluster != "" {
		ev.Cluster = finish.Cluster
	}
	if finish.Instance != "" {
		ev.Instance = finish.Instance
	}
	if finish.VersionID != "" {
		ev.VersionID = finish.VersionID
	}
	if finish.GrantID != "" {
		ev.GrantID = finish.GrantID
	}
	if finish.Scope != nil {
		ev.Scope = finish.Scope
	}
	if finish.ResourceVersion != "" {
		ev.ResourceVersion = finish.ResourceVersion
	}
	if finish.Digest != "" {
		ev.Digest = finish.Digest
	}
	ev.EndedAt = finish.EndedAt.UTC()
	return ev, dbFinish, tailRelPath, nil
}

func (s *GatewayStore) FinishAudit(id int64, finish AuditFinish) error {
	if id == 0 {
		return nil
	}
	if !s.auditDocStoreEnabled() {
		return finishAuditWith(gatewayRetryExecer{s.db}, id, finish)
	}
	ev, dbFinish, tailRelPath, err := s.prepareAuditDocFinish(id, finish)
	if err != nil {
		return err
	}
	transitioned, err := finishAuditRow(gatewayRetryExecer{s.db}, id, dbFinish)
	if err != nil {
		if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
		return err
	}
	if !transitioned {
		if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
		s.takeInFlightAudit(id)
		return nil
	}
	s.takeInFlightAudit(id)
	return s.appendAuditDocRecord(ev)
}

type auditExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func finishAuditWith(execer auditExecer, id int64, finish AuditFinish) error {
	_, err := finishAuditRow(execer, id, finish)
	return err
}

func finishAuditRow(execer auditExecer, id int64, finish AuditFinish) (bool, error) {
	finish = normalizeAuditFinishDefaults(finish)
	scopeJSON, err := credentialAuditScopeJSON(finish.Scope)
	if err != nil {
		return false, err
	}
	model := strings.TrimSpace(finish.Model)
	totalTokens := finish.TotalTokens
	if totalTokens == 0 && (finish.InputTokens > 0 || finish.OutputTokens > 0) {
		totalTokens = finish.InputTokens + finish.OutputTokens
	}
	result, err := execer.Exec(`UPDATE audit_events
		SET status = ?, error = ?, tail = ?, bytes_in = ?, bytes_out = ?,
			model = CASE WHEN ? = '' THEN model ELSE ? END,
			input_tokens = CASE WHEN ? > 0 THEN ? ELSE input_tokens END,
			output_tokens = CASE WHEN ? > 0 THEN ? ELSE output_tokens END,
			total_tokens = CASE WHEN ? > 0 THEN ? ELSE total_tokens END,
			command_summary = CASE WHEN ? = '' THEN command_summary ELSE ? END,
			cluster = CASE WHEN ? = '' THEN cluster ELSE ? END,
			instance = CASE WHEN ? = '' THEN instance ELSE ? END,
			credential_version_id = CASE WHEN ? = '' THEN credential_version_id ELSE ? END,
			credential_grant_id = CASE WHEN ? = '' THEN credential_grant_id ELSE ? END,
			scope_json = CASE WHEN ? = '' THEN scope_json ELSE ? END,
			resource_version = CASE WHEN ? = '' THEN resource_version ELSE ? END,
			digest = CASE WHEN ? = '' THEN digest ELSE ? END,
			ended_at = ?
		WHERE id = ? AND status IN ('running', 'executing')`,
		finish.Status, strings.ToValidUTF8(utf8Tail(redactSecretText(finish.Error), 1200), "\uFFFD"), strings.ToValidUTF8(trimTail(finish.Tail, 8192), "\uFFFD"), finish.BytesIn, finish.BytesOut,
		model, model,
		finish.InputTokens, finish.InputTokens,
		finish.OutputTokens, finish.OutputTokens,
		totalTokens, totalTokens,
		strings.ToValidUTF8(finish.CommandSummary, "\uFFFD"), strings.ToValidUTF8(finish.CommandSummary, "\uFFFD"),
		finish.Cluster, finish.Cluster, finish.Instance, finish.Instance,
		finish.VersionID, finish.VersionID, finish.GrantID, finish.GrantID,
		scopeJSON, scopeJSON,
		finish.ResourceVersion, finish.ResourceVersion, finish.Digest, finish.Digest,
		formatTime(finish.EndedAt.UTC()), id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		// Terminal audit state is immutable. A late completion may race with
		// recovery or an uncertainty transition, but must not overwrite it.
		querier, ok := execer.(interface {
			QueryRow(string, ...any) *sql.Row
		})
		if !ok {
			return false, sql.ErrNoRows
		}
		var status string
		if err := querier.QueryRow("SELECT status FROM audit_events WHERE id = ?", id).Scan(&status); err != nil {
			return false, err
		}
		if status == "running" || status == "executing" {
			return false, sql.ErrNoRows
		}
		if status != finish.Status {
			return false, fmt.Errorf("%w: audit-%d is %s, requested %s", errAuditTerminalConflict, id, status, finish.Status)
		}
		return false, nil
	}
	return true, nil
}

func (s *GatewayStore) ListAudit(limit int) ([]AuditEvent, error) {
	return s.ListAuditFiltered(AuditFilter{Limit: limit})
}

func (s *GatewayStore) ListAuditFiltered(filter AuditFilter) ([]AuditEvent, error) {
	query, args := auditFilterQuery(filter)
	rows, err := s.readDB().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		var action, started, ended, scopeJSON string
		if err := rows.Scan(&ev.ID, &ev.UserID, &ev.TokenID, &ev.Cluster, &ev.Instance,
			&ev.CredentialID, &ev.VersionID, &ev.GrantID, &scopeJSON,
			&ev.ResourceVersion, &ev.Digest, &ev.CorrelationID,
			&action, &ev.Session, &ev.CommandSummary, &ev.Status, &ev.Error, &ev.Tail,
			&ev.BytesIn, &ev.BytesOut, &ev.Model, &ev.InputTokens, &ev.OutputTokens, &ev.TotalTokens,
			&started, &ended); err != nil {
			return nil, err
		}
		if scopeJSON != "" {
			var scope CredentialGrantScope
			if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
				return nil, err
			}
			ev.Scope = &scope
		}
		ev.Action = GatewayAction(action)
		ev.Tail = s.hydrateAuditTail(ev.Tail)
		ev.StartedAt, _ = parseTime(started)
		if ended != "" {
			ev.EndedAt, _ = parseTime(ended)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	effectiveLimit := filter.Limit
	if effectiveLimit <= 0 || effectiveLimit > 500 {
		effectiveLimit = 100
	}
	if s.auditDocStoreEnabled() && len(events) < effectiveLimit {
		seen := make(map[int64]bool, len(events))
		for _, ev := range events {
			seen[ev.ID] = true
		}
		var ownedCreds map[string]bool
		if v := strings.TrimSpace(filter.VisibleToUser); v != "" {
			ownedCreds = s.loadOwnedCredentialIDs(v)
		}
		docEvents, err := s.queryAuditDocRecords(filter, effectiveLimit-len(events), seen, ownedCreds)
		if err != nil {
			return nil, err
		}
		if len(docEvents) > 0 {
			events = append(events, docEvents...)
			sort.Slice(events, func(i, j int) bool { return events[i].ID > events[j].ID })
			if len(events) > effectiveLimit {
				events = events[:effectiveLimit]
			}
		}
	}
	return events, nil
}

// auditFilterQuery builds the ListAuditFiltered statement. audit_indexes.sql
// keeps an index for each filter combination the console sends.
func auditFilterQuery(filter AuditFilter) (string, []any) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	conditions := make([]string, 0, 6)
	args := make([]any, 0, 7)
	if filter.BeforeID > 0 {
		conditions = append(conditions, "id < ?")
		args = append(args, filter.BeforeID)
	}
	if v := strings.TrimSpace(filter.CorrelationID); v != "" {
		conditions = append(conditions, "correlation_id = ?")
		args = append(args, v)
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "started_at_ms >= ?")
		args = append(args, filter.Since.UTC().UnixMilli())
	}
	if !filter.Until.IsZero() {
		conditions = append(conditions, "started_at_ms < ?")
		args = append(args, filter.Until.UTC().UnixMilli())
	}
	if filter.HideUpgradeObserve {
		conditions = append(conditions, "NOT (action = 'agent:upgrade' AND status = 'observing')")
	}
	if v := strings.TrimSpace(filter.UserID); v != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Cluster); v != "" {
		conditions = append(conditions, "cluster = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Instance); v != "" {
		conditions = append(conditions, "instance = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Session); v != "" {
		conditions = append(conditions, "session = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(string(filter.Action)); v != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.VisibleToUser); v != "" {
		conditions = append(conditions, `(user_id = ? OR credential_id IN (SELECT id FROM credentials WHERE owner_id = ?))`)
		args = append(args, v, v)
	}
	query := `SELECT id, user_id, token_id, cluster, instance, credential_id,
			credential_version_id, credential_grant_id, scope_json,
			resource_version, digest, correlation_id, action, session,
			command_summary, status, error, tail, bytes_in, bytes_out,
			model, input_tokens, output_tokens, total_tokens, started_at, ended_at
		FROM audit_events`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, filter.Limit)
	return query, args
}

func credentialAuditScopeJSON(scope *CredentialGrantScope) (string, error) {
	if scope == nil {
		return "", nil
	}
	normalized := normalizeCredentialGrantScope(*scope)
	if err := validateCredentialGrantScope(normalized); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	return string(encoded), err
}

// DeleteAuditBefore removes completed audit rows older than before. It is the
// store-level entry point the maintenance paths and tests use; the HTTP route
// (/v1/audit, DELETE) goes through PurgeAuditBefore, which shares the same
// dialect rendering. The statement has SQLite-only clauses (AS MATERIALIZED,
// INDEXED BY), so the dialect decides which text runs; MySQL gets the
// multi-table DELETE over a materialized derived table. Keeping this on
// deleteCompletedAuditSQLFor instead of a second selector is what stops this
// path from drifting back to the SQLite text.
func (s *GatewayStore) DeleteAuditBefore(before time.Time) (int64, error) {
	return execRowsAffected(s.db, deleteCompletedAuditSQLFor(s.auditPurgeDialect()), before.UTC().UnixMilli())
}
