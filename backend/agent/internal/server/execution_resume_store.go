package server

// Persistence for addressable executions (INT-82a).
//
// INT-72 kept the Gateway's execution records in memory, so a Gateway restart
// turned every unfinished-looking execution into "not found". This file stores
// the same record in the gateway database (SQLite and MySQL share one upsert
// through gatewayUpsertSQL) and reloads it when the hub starts.
//
// What is stored: identity, target, tool name, owner, status, the bounded error
// category and the terminal JSON-RPC envelope. Command arguments are never
// stored; the terminal payload is scrubbed with sanitizeSecretText before it is
// written, so a result that echoed a credential cannot persist it.
//
// What is not stored: a live "running" record survives the restart only as a
// definite failure. INT-82b is the task that may distinguish "the Agent crashed"
// from "the Agent has not reconnected yet"; this change must not pretend it can.

import (
	"database/sql"
	"encoding/json"
	"time"
)

// executionRecordTerminalLimit bounds one stored terminal payload. A tool reply
// is already size-limited by the message queue; this is the second guard so a
// single row cannot grow without bound.
const executionRecordTerminalLimit = 256 * 1024

type executionRecordRow struct {
	id       string
	tool     string
	cluster  string
	instance string
	ownerID  string
	// agentBootID is the Agent process that owned the execution when it was
	// accepted, or agentBootUnknown when that Agent reported none. A restart of
	// that process is what makes a redelivery unsafe (INT-82b).
	agentBootID string
	status      string
	errText     string
	terminal    string
	// terminalTruncated records that terminal is a prefix of a larger payload,
	// and terminalBytes is the original payload size. Both are persisted so a
	// restarted Gateway can still tell "this is the whole result" from "this is
	// 256 KiB of it" instead of handing the caller half a JSON document.
	terminalTruncated bool
	terminalBytes     int64
	createdAt         time.Time
	updatedAt         time.Time
	finishedAt        time.Time
	expiresAt         time.Time
}

// saveExecutionRecord upserts one record. expires_at_ms is the moment the row
// may be deleted: twice the TTL after the terminal state, so GET can answer
// "expired" for one full TTL before the row disappears.
func (s *GatewayStore) saveExecutionRecord(record *executionRecord, ttl time.Duration) error {
	row := executionRecordFromMemory(record, ttl)
	_, err := s.db.Exec(gatewayUpsertSQL(gatewayUpsert{
		insert: `INSERT INTO execution_records
		(execution_id, tool, cluster, instance, owner_id, agent_boot_id, status, error_text, terminal,
		 terminal_truncated, terminal_bytes,
		 created_at_ms, updated_at_ms, finished_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		target: []string{"execution_id"},
		set: []string{
			"tool = excluded.tool",
			"cluster = excluded.cluster",
			"instance = excluded.instance",
			"owner_id = excluded.owner_id",
			"agent_boot_id = excluded.agent_boot_id",
			"status = excluded.status",
			"error_text = excluded.error_text",
			"terminal = excluded.terminal",
			"terminal_truncated = excluded.terminal_truncated",
			"terminal_bytes = excluded.terminal_bytes",
			"updated_at_ms = excluded.updated_at_ms",
			"finished_at_ms = excluded.finished_at_ms",
			"expires_at_ms = excluded.expires_at_ms",
		},
	}), row.values()...)
	return err
}

func executionRecordFromMemory(record *executionRecord, ttl time.Duration) executionRecordRow {
	terminal := string(record.terminal)
	originalBytes := int64(len(record.terminal))
	truncated := false
	if len(terminal) > executionRecordTerminalLimit {
		terminal = truncateUTF8Bytes(terminal, executionRecordTerminalLimit)
		truncated = true
	}
	updated := record.updatedAt
	if updated.IsZero() {
		updated = time.Now()
	}
	finished := record.finishedAt
	expires := updated.Add(2 * ttl)
	if !finished.IsZero() {
		expires = finished.Add(2 * ttl)
	}
	return executionRecordRow{
		id: record.id, tool: record.tool, cluster: record.cluster, instance: record.instance,
		ownerID: record.ownerID, agentBootID: record.agentBootID, status: record.status,
		errText:           normalizeUTF8ForStore(sanitizeSecretText(record.errText)),
		terminal:          normalizeUTF8ForStore(redactSecretText(terminal)),
		terminalTruncated: truncated, terminalBytes: originalBytes,
		createdAt: record.createdAt, updatedAt: updated, finishedAt: finished, expiresAt: expires,
	}
}

func (row executionRecordRow) values() []interface{} {
	return []interface{}{
		row.id, row.tool, row.cluster, row.instance, row.ownerID, row.agentBootID, row.status,
		row.errText, row.terminal,
		boolToInt(row.terminalTruncated), row.terminalBytes,
		row.createdAt.UnixMilli(), row.updatedAt.UnixMilli(),
		row.finishedAt.UnixMilli(), row.expiresAt.UnixMilli(),
	}
}

// loadExecutionRecords returns every record that has not reached its expiry, so
// a restarted hub can answer by execution ID again.
func (s *GatewayStore) loadExecutionRecords(now time.Time) ([]executionRecordRow, error) {
	rows, err := s.db.Query(`SELECT execution_id, tool, cluster, instance, owner_id, agent_boot_id, status, error_text,
		terminal, terminal_truncated, terminal_bytes,
		created_at_ms, updated_at_ms, finished_at_ms, expires_at_ms
		FROM execution_records WHERE expires_at_ms > ? ORDER BY updated_at_ms DESC`, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []executionRecordRow
	for rows.Next() {
		var row executionRecordRow
		var truncated int
		var createdAt, updatedAt, finishedAt, expiresAt int64
		if err := rows.Scan(&row.id, &row.tool, &row.cluster, &row.instance, &row.ownerID, &row.agentBootID,
			&row.status, &row.errText, &row.terminal, &truncated, &row.terminalBytes,
			&createdAt, &updatedAt, &finishedAt, &expiresAt); err != nil {
			return nil, err
		}
		row.terminalTruncated = truncated != 0
		row.createdAt = time.UnixMilli(createdAt)
		row.updatedAt = time.UnixMilli(updatedAt)
		if finishedAt > 0 {
			row.finishedAt = time.UnixMilli(finishedAt)
		}
		row.expiresAt = time.UnixMilli(expiresAt)
		out = append(out, row)
	}
	return out, rows.Err()
}

// purgeExecutionRecords deletes rows past their expiry. It is called from the
// registry's lazy sweep, which already runs on every addressable access: the
// repository has no second Gateway scheduler, and lazy eviction is the pattern
// detached_tasks already uses.
func (s *GatewayStore) purgeExecutionRecords(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM execution_records WHERE expires_at_ms > 0 AND expires_at_ms < ?`, now.UnixMilli())
	return err
}

// boolToInt keeps the SQLite/MySQL contract to plain 0/1 integers: MySQL has no
// boolean type and the two dialects must store exactly the same value.
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func executionRowTerminal(row executionRecordRow) json.RawMessage {
	if row.terminal == "" {
		return nil
	}
	return json.RawMessage(row.terminal)
}

var _ = sql.ErrNoRows
