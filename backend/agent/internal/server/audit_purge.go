package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// Audit retention and expired-token cleanup limits. Zero retention keeps every
// audit row: the gateway never purges audit history unless the operator opts in.
const (
	// AuditPurgeBatchSize bounds the ordinary rows removed by one statement so
	// the single SQLite connection is released between batches. It is a row
	// count, not a duration, so it stays out of the timeout table: changing it
	// needs a code change (RUNTIME_LIMITS.md, section 1).
	AuditPurgeBatchSize = 5000
	// ExpiredTokenGrace keeps an expired token row after its expiry so operators
	// can still explain recent authentication failures from GET /v1/admin/tokens.
	ExpiredTokenGrace = 7 * 24 * time.Hour
	// AuditRetentionActor is recorded as the purger of scheduled purges. It is
	// deliberately not a user id and not local:uid, so a receipt can never be
	// mistaken for a Gateway user or an interactive local operator.
	AuditRetentionActor = "system:audit-retention"
)

// activeAuditStatusSQL lists execution and controller states that still
// describe live or unresolved work. They are never eligible for retention.
// accepted/observing controller rows are protected by the upgrade rules below.
const activeAuditStatusSQL = `'running', 'executing', 'outcome_unknown'`

// auditIndexHint pins the started_at index for every retention scan. A delivered
// database can pick a different plan, and a full table scan on the single
// gateway connection would stall every execution for the whole purge.
const auditIndexHint = ` INDEXED BY idx_audit_started_ms `

// deleteCompletedAuditSQL removes upgrade evidence that a later terminal
// observation has replaced. Controller admission and polls stay until their
// exact Job has a later success/error row. The CTE is materialized and the
// predicate and the deletion run in one statement, so an admission row is never
// deleted without the terminal row being evaluated in the same snapshot. The
// cutoff is a plain range on started_at_ms so idx_audit_started_ms serves it.
// The comparison must stay on the integer column: RFC3339Nano text drops the
// fractional part at whole seconds, so ".5Z" sorts before "Z" and a text
// started_at < cutoff would wrongly include rows up to one second after it.
const deleteCompletedAuditSQL = `WITH terminal AS MATERIALIZED (
	SELECT cluster, instance, correlation_id,
		json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job') AS job,
		MAX(id) AS id
	FROM audit_events
	WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
		AND json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome') IN ('succeeded', 'failed')
	GROUP BY cluster, instance, correlation_id, job
)
DELETE FROM audit_events WHERE id IN (
	SELECT audit_events.id FROM audit_events` + auditIndexHint + `
	WHERE started_at_ms < ?
		AND status NOT IN (` + activeAuditStatusSQL + `) AND action != 'audit:purge'
		AND (status NOT IN ('accepted', 'observing') OR EXISTS (
			SELECT 1 FROM terminal WHERE terminal.id > audit_events.id
				AND terminal.cluster = audit_events.cluster AND terminal.instance = audit_events.instance
				AND terminal.correlation_id = audit_events.correlation_id AND terminal.correlation_id != ''
				AND terminal.job = json_extract(CASE WHEN json_valid(audit_events.tail) THEN audit_events.tail ELSE '{}' END, '$.result.job')
		))
)`

// deleteAuditBatchSQL removes one bounded batch of ordinary completed rows.
// agent:upgrade rows are never touched here: their evidence rules need every row
// of one correlation to be visible, so they stay with deleteCompletedAuditSQL.
const deleteAuditBatchSQL = `DELETE FROM audit_events WHERE id IN (
	SELECT id FROM audit_events INDEXED BY idx_audit_started_ms
	WHERE started_at_ms < ?
		AND action NOT IN ('agent:upgrade', 'audit:purge')
		AND status NOT IN (` + activeAuditStatusSQL + `)
	ORDER BY started_at_ms, id
	LIMIT ?
)`

// deleteCompletedAuditSQLMySQL is the MySQL rendering of
// deleteCompletedAuditSQL. MySQL 8.4 rejects AS MATERIALIZED and INDEXED BY
// (Error 1064) and rejects a DELETE whose subquery reads the table being
// deleted (Error 1093), so the two-step-on-paper form becomes one multi-table
// DELETE whose id list comes from a derived table. The derived table is forced
// to materialize (ORDER BY ... LIMIT <max>) so the optimizer cannot merge the
// self-read back into the DELETE and resurrect 1093; the LIMIT itself excludes
// no row. The terminal CTE carries GROUP BY, so MySQL materializes it once and
// the admission row is still judged against a fixed snapshot in the same
// statement - a separate SELECT-then-DELETE would lose that promise because a
// terminal row could be purged in between.
const deleteCompletedAuditSQLMySQL = `DELETE target FROM audit_events AS target JOIN (
	SELECT victim.id AS id FROM audit_events AS victim
	LEFT JOIN (
		SELECT cluster, instance, correlation_id,
			JSON_UNQUOTE(json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job')) AS job,
			MAX(id) AS id
		FROM audit_events
		WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
			AND JSON_UNQUOTE(json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome')) IN ('succeeded', 'failed')
		GROUP BY cluster, instance, correlation_id, job
	) AS terminal
		ON terminal.id > victim.id
		AND terminal.cluster = victim.cluster AND terminal.instance = victim.instance
		AND terminal.correlation_id = victim.correlation_id AND terminal.correlation_id != ''
		AND terminal.job = JSON_UNQUOTE(json_extract(CASE WHEN json_valid(victim.tail) THEN victim.tail ELSE '{}' END, '$.result.job'))
	WHERE victim.started_at_ms < ?
		AND victim.status NOT IN (` + activeAuditStatusSQL + `) AND victim.action != 'audit:purge'
		AND (victim.status NOT IN ('accepted', 'observing') OR terminal.id IS NOT NULL)
	ORDER BY victim.started_at_ms, victim.id
	LIMIT 18446744073709551615
) AS batch ON batch.id = target.id`

// deleteAuditBatchSQLMySQL is the MySQL rendering of deleteAuditBatchSQL. The
// derived table keeps the batch cap (AuditPurgeBatchSize) and the
// oldest-first ORDER BY, and its ORDER BY ... LIMIT forces materialization so
// the multi-table DELETE never trips Error 1093. Both dialects order by
// (started_at_ms, id), so a batch removes the same rows on either backend.
const deleteAuditBatchSQLMySQL = `DELETE target FROM audit_events AS target JOIN (
	SELECT candidate.id AS id FROM audit_events AS candidate
	WHERE candidate.started_at_ms < ?
		AND candidate.action NOT IN ('agent:upgrade', 'audit:purge')
		AND candidate.status NOT IN (` + activeAuditStatusSQL + `)
	ORDER BY candidate.started_at_ms, candidate.id
	LIMIT ?
) AS batch ON batch.id = target.id`

// deleteUpgradeAuditSQL removes only completed agent:upgrade evidence rows that
// have a later terminal observation. Used by drainAuditTx once the bounded
// ordinary-row batches are drained so multi-million-row tables never execute an
// unbounded single-transaction DELETE.
const deleteUpgradeAuditSQL = `WITH terminal AS (
	SELECT cluster, instance, correlation_id,
		json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job') AS job,
		MAX(id) AS id
	FROM audit_events
	WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
		AND json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome') IN ('succeeded', 'failed')
	GROUP BY cluster, instance, correlation_id, job
)
DELETE FROM audit_events WHERE id IN (
	SELECT audit_events.id FROM audit_events` + auditIndexHint + `
	WHERE started_at_ms < ?
		AND action = 'agent:upgrade'
		AND status NOT IN (` + activeAuditStatusSQL + `)
		AND (status NOT IN ('accepted', 'observing') OR EXISTS (
			SELECT 1 FROM terminal WHERE terminal.id > audit_events.id
				AND terminal.cluster = audit_events.cluster AND terminal.instance = audit_events.instance
				AND terminal.correlation_id = audit_events.correlation_id AND terminal.correlation_id != ''
				AND terminal.job = json_extract(CASE WHEN json_valid(audit_events.tail) THEN audit_events.tail ELSE '{}' END, '$.result.job')
		))
)`

// deleteUpgradeAuditSQLMySQL is the MySQL rendering of deleteUpgradeAuditSQL.
const deleteUpgradeAuditSQLMySQL = `DELETE target FROM audit_events AS target JOIN (
	SELECT victim.id AS id FROM audit_events AS victim
	LEFT JOIN (
		SELECT cluster, instance, correlation_id,
			JSON_UNQUOTE(json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job')) AS job,
			MAX(id) AS id
		FROM audit_events
		WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
			AND JSON_UNQUOTE(json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome')) IN ('succeeded', 'failed')
		GROUP BY cluster, instance, correlation_id, job
	) AS terminal
		ON terminal.id > victim.id
		AND terminal.cluster = victim.cluster AND terminal.instance = victim.instance
		AND terminal.correlation_id = victim.correlation_id AND terminal.correlation_id != ''
		AND terminal.job = JSON_UNQUOTE(json_extract(CASE WHEN json_valid(victim.tail) THEN victim.tail ELSE '{}' END, '$.result.job'))
	WHERE victim.started_at_ms < ?
		AND victim.action = 'agent:upgrade'
		AND victim.status NOT IN (` + activeAuditStatusSQL + `)
		AND (victim.status NOT IN ('accepted', 'observing') OR terminal.id IS NOT NULL)
	ORDER BY victim.started_at_ms, victim.id
	LIMIT 18446744073709551615
) AS batch ON batch.id = target.id`

func deleteUpgradeAuditSQLFor(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return deleteUpgradeAuditSQLMySQL
	}
	return deleteUpgradeAuditSQL
}

// deleteCompletedAuditSQLFor renders the upgrade-evidence statement for dialect.
// The SQLite branch stays byte-identical to deleteCompletedAuditSQL: its
// INDEXED BY hint and its DELETE-from-a-self-reading-subquery are only valid
// there, and rewriting the SQLite statement into the MySQL shape would give up
// the index pin that keeps a purge from scanning the whole audit table.
func deleteCompletedAuditSQLFor(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return deleteCompletedAuditSQLMySQL
	}
	return deleteCompletedAuditSQL
}

// deleteAuditBatchSQLFor renders the bounded batch for dialect. The SQLite
// branch stays byte-identical to deleteAuditBatchSQL for the same index-pin
// reason; both branches keep "RowsAffected < AuditPurgeBatchSize means the
// ordinary window is drained".
func deleteAuditBatchSQLFor(dialect gatewaySQLDialect) string {
	if dialect == gatewayDialectMySQL {
		return deleteAuditBatchSQLMySQL
	}
	return deleteAuditBatchSQL
}

// auditPurgeDialect resolves the SQL dialect for a purge. Stores opened by
// tests carry no dialect field, so fall back to the process-wide dialect the
// way the monitor retention renderer does.
func (s *GatewayStore) auditPurgeDialect() gatewaySQLDialect {
	if s != nil && s.dialect != "" {
		return s.dialect
	}
	return gatewayDialect()
}

// AuditPurgeResult reports one retention or purge run.
type AuditPurgeResult struct {
	// Before is the retention cutoff. Rows started strictly before it are
	// eligible unless they are protected.
	Before time.Time
	// Deleted is the number of rows removed by this run.
	Deleted int64
	// Batches counts the deletion statements that removed rows.
	Batches int
	// ReceiptID is the audit row that records this run.
	ReceiptID int64
}

// auditPurgeReceipt is the receipt tail. It is updated after every batch so the
// record is accurate even when a later batch fails.
type auditPurgeReceipt struct {
	Before  string `json:"before"`
	Deleted int64  `json:"deleted"`
	Batches int    `json:"batches"`
}

// PurgeAuditBefore requires a current platform admin for remote callers.
func (s *GatewayStore) PurgeAuditBefore(auth TokenAuth, before time.Time) (int64, error) {
	if !s.UserIsPlatformAdmin(auth.UserID) {
		return 0, ErrUnauthorized
	}
	result, err := s.purgeAuditBefore(auth.UserID, auth.TokenID, before)
	return result.Deleted, err
}

// PurgeLocalAuditBefore is for the local maintenance command with direct
// database access only. HTTP handlers must use PurgeAuditBefore for
// authorization.
func (s *GatewayStore) PurgeLocalAuditBefore(before time.Time) (int64, error) {
	result, err := s.purgeAuditBefore(fmt.Sprintf("local:uid:%d", os.Geteuid()), "", before)
	return result.Deleted, err
}

// PurgeExpiredAudit is the scheduled retention entry point. It records the run
// under AuditRetentionActor, never as a local operator or a Gateway user.
func (s *GatewayStore) PurgeExpiredAudit(before time.Time) (AuditPurgeResult, error) {
	return s.purgeAuditBefore(AuditRetentionActor, "", before)
}

// auditPurgePause runs between deletion batches. Production sleeps the table
// value DOOPS_AUDIT_PURGE_BATCH_PAUSE (100ms by default); tests replace it to
// observe batching without waiting.
var auditPurgePause = func() { time.Sleep(timeouts.AuditPurgeBatchPause) }

// purgeAuditBefore deletes completed audit rows older than before.
//
//   - The receipt is written and completed before the first deletion: a purge
//     whose receipt cannot be written deletes nothing.
//   - Each batch deletes at most AuditPurgeBatchSize ordinary rows and applies
//     the upgrade-evidence rule in the same transaction. The receipt update from
//     its success state back to running shares that transaction, so batches
//     fail one at a time: a failed batch keeps the earlier batches and reopens
//     the receipt as an error with the counts already removed.
//   - Once a batch removes fewer than AuditPurgeBatchSize ordinary rows, the
//     same transaction proves no eligible row remains as of its commit.
func (s *GatewayStore) purgeAuditBefore(userID, tokenID string, before time.Time) (AuditPurgeResult, error) {
	result := AuditPurgeResult{Before: before.UTC()}
	cutoff := formatTime(result.Before)
	purge := auditPurgeSession{store: s, userID: userID, tokenID: tokenID, receipt: auditPurgeReceipt{Before: cutoff}}

	receiptID, err := purge.openReceipt()
	if err != nil {
		return result, err
	}
	result.ReceiptID = receiptID
	if err := purge.record(nil); err != nil {
		// The receipt never reached a terminal state and nothing was deleted,
		// so remove it instead of leaving a running row that looks in flight.
		// A discard failure is reported alongside: a silently orphaned
		// 'running' receipt is indistinguishable from an in-flight purge.
		if discardErr := purge.discardReceipt(); discardErr != nil {
			return result, fmt.Errorf("%w; audit purge receipt %d could not be discarded: %v", err, purge.id, discardErr)
		}
		return result, err
	}

	for {
		deleted, complete, err := purge.deleteBatch(before)
		if err != nil {
			return result, purge.fail(err)
		}
		result.Deleted += deleted
		if deleted > 0 {
			result.Batches++
		}
		if complete {
			return result, nil
		}
		auditPurgePause()
	}
}

// discardReceipt rolls back a receipt that could not be finalized before any
// deletion ran. A receipt already moved to a terminal state is left in place.
// The delete is retried briefly: on a contended store a transient busy error
// must not strand the running receipt.
func (p *auditPurgeSession) discardReceipt() error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * timeouts.AuditPurgeBatchPause)
		}
		if _, err = p.store.db.Exec(`DELETE FROM audit_events
			WHERE id = ? AND action = 'audit:purge' AND status IN ('running', 'executing')`, p.id); err == nil {
			return nil
		}
	}
	return err
}

// auditPurgeSession is one purge and its receipt. It is confined to one goroutine.
type auditPurgeSession struct {
	store   *GatewayStore
	userID  string
	tokenID string
	id      int64
	receipt auditPurgeReceipt
}

func (p *auditPurgeSession) openReceipt() (int64, error) {
	tx, err := p.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	nowUTC := time.Now().UTC()
	res, err := tx.Exec(`INSERT INTO audit_events
		(user_id, token_id, action, session, command_summary, status, error, tail, ended_at, scope_json, started_at_ms, started_at)
		VALUES (?, ?, 'audit:purge', 'audit-purge', ?, 'running', '', '', '', '', ?, ?)`,
		p.userID, p.tokenID, "audit:purge before="+p.receipt.Before, nowUTC.UnixMilli(), formatTime(nowUTC))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	p.id = id
	return id, nil
}

// deleteBatch runs one transaction that deletes the next batch, applies the
// upgrade-evidence rule and records the running counts on the receipt.
func (p *auditPurgeSession) deleteBatch(before time.Time) (int64, bool, error) {
	tx, err := p.store.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	dialect := p.store.auditPurgeDialect()
	deleted, complete, err := drainAuditTx(tx, before.UTC().UnixMilli(), dialect)
	if err != nil {
		return 0, false, err
	}
	if complete && deleted > 0 {
		// Another writer may have added rows after this transaction took its
		// read snapshot. Delete them here so the committed receipt covers
		// every row this run removed.
		extra, _, err := drainAuditTx(tx, before.UTC().UnixMilli(), dialect)
		if err != nil {
			return 0, false, err
		}
		deleted += extra
	}
	next := p.receipt
	next.Deleted += deleted
	if deleted > 0 {
		next.Batches++
	}
	if _, err := writeAuditPurgeReceipt(tx, p.id, next); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	p.receipt = next
	return deleted, complete, nil
}

// drainAuditTx deletes one bounded ordinary batch first, and applies the
// upgrade-evidence rule once the ordinary window is drained. Doing ordinary
// batches first ensures multi-million-row tables stay strictly bounded to
// AuditPurgeBatchSize rows per transaction instead of deleting the entire table
// inside one unbounded upgrade-evidence statement.
func drainAuditTx(tx *sql.Tx, cutoffMs int64, dialect gatewaySQLDialect) (int64, bool, error) {
	batch, err := execRowsAffected(tx, deleteAuditBatchSQLFor(dialect), cutoffMs, AuditPurgeBatchSize)
	if err != nil {
		return 0, false, err
	}
	if batch >= AuditPurgeBatchSize {
		return batch, false, nil
	}
	upgrades, err := execRowsAffected(tx, deleteUpgradeAuditSQLFor(dialect), cutoffMs)
	if err != nil {
		return 0, false, err
	}
	return batch + upgrades, true, nil
}

// reclaimableGatewayTables enumerates tables that maintenance commands may
// compact to return freed pages back to the storage engine.
var reclaimableGatewayTables = map[string]bool{
	"audit_events":               true,
	"agent_resource_audit":       true,
	"platform_events":            true,
	"webhook_deliveries":         true,
	"inspection_metric_points":   true,
	"agent_observations":         true,
	"monitor_run_items":          true,
	"monitor_resource_snapshots": true,
}

// ReclaimTables compacts freed storage for the named allowlisted Gateway tables.
// On MySQL (with innodb_file_per_table=ON) it executes OPTIMIZE TABLE so InnoDB
// rebuilds and shrinks the .ibd tablespace. On SQLite it checkpoints the WAL and
// runs VACUUM once.
func (s *GatewayStore) ReclaimTables(ctx context.Context, tables ...string) error {
	if len(tables) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tables))
	validated := make([]string, 0, len(tables))
	for _, raw := range tables {
		name := strings.TrimSpace(raw)
		if !reclaimableGatewayTables[name] {
			return fmt.Errorf("reclaim_table_unsupported: table %q is not in the reclaim allowlist", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		validated = append(validated, name)
	}
	dialect := s.auditPurgeDialect()
	if dialect == gatewayDialectMySQL {
		for _, table := range validated {
			if err := s.reclaimMySQLTable(ctx, table); err != nil {
				return err
			}
		}
		return nil
	}
	rows, err := s.db.QueryContext(ctx, gatewaySQLiteWALCheckpointSQL())
	if err == nil {
		for rows.Next() {
		}
		_ = rows.Close()
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("reclaim_sqlite_vacuum_failed: %w", err)
	}
	return nil
}

func (s *GatewayStore) reclaimMySQLTable(ctx context.Context, table string) error {
	rows, err := s.db.QueryContext(ctx, "OPTIMIZE TABLE "+gatewayIdentifierFor(gatewayDialectMySQL, table))
	if err != nil {
		return fmt.Errorf("reclaim_mysql_optimize_failed: table=%s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var tbl, op, msgType, msgText string
		if err := rows.Scan(&tbl, &op, &msgType, &msgText); err != nil {
			return fmt.Errorf("reclaim_mysql_optimize_scan_failed: table=%s: %w", table, err)
		}
		if strings.EqualFold(strings.TrimSpace(msgType), "error") {
			return fmt.Errorf("reclaim_mysql_optimize_error: table=%s: %s", table, msgText)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reclaim_mysql_optimize_rows_failed: table=%s: %w", table, err)
	}
	return nil
}

func execRowsAffected(execer auditExecer, query string, args ...any) (int64, error) {
	res, err := execer.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// record completes the receipt with the current counts, as success when cause
// is nil and as an error otherwise. It never deletes rows.
func (p *auditPurgeSession) record(cause error) error {
	return p.store.FinishAudit(p.id, auditPurgeFinish(p.receipt, cause))
}

// fail records the failure on the completed receipt. The receipt is written as
// success before the first batch so a crash cannot leave it looking in flight;
// that means a failure after a committed batch must be allowed to move it from
// success to error. This is the one controlled exception to terminal audit
// immutability, and it is guarded by action='audit:purge' so it can only touch
// a retention receipt. FinishAudit is deliberately not used: its WHERE clause
// only accepts running/executing rows and would leave the receipt claiming
// success while returning a terminal-state conflict.
//
// The tail carries the counts from batches that actually committed; a failed
// batch rolls back its own deletion, so p.receipt already equals the real
// number of rows removed.
func (p *auditPurgeSession) fail(cause error) error {
	data, err := json.Marshal(p.receipt)
	if err != nil {
		return fmt.Errorf("%w; audit purge receipt %d could not encode the failure: %v", cause, p.id, err)
	}
	res, err := p.store.db.Exec(`UPDATE audit_events
		SET status = 'error', error = 'audit_purge_failed', tail = ?, ended_at = ?
		WHERE id = ? AND action = 'audit:purge'`,
		string(data), formatTime(time.Now().UTC()), p.id)
	if err != nil {
		return fmt.Errorf("%w; audit purge receipt %d could not record the failure: %v", cause, p.id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w; audit purge receipt %d could not confirm the failure: %v", cause, p.id, err)
	}
	if affected == 1 {
		return cause
	}
	// MySQL reports 0 when the row already has these values; confirm the
	// receipt still exists rather than assuming it was lost.
	var status string
	if err := p.store.db.QueryRow(`SELECT status FROM audit_events WHERE id = ? AND action = 'audit:purge'`, p.id).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w; audit purge receipt %d is no longer present", cause, p.id)
		}
		return fmt.Errorf("%w; audit purge receipt %d could not confirm the failure: %v", cause, p.id, err)
	}
	if status != "error" {
		return fmt.Errorf("%w; audit purge receipt %d is %s, requested error", cause, p.id, status)
	}
	return cause
}

// auditPurgeFinish turns the receipt counts into a terminal audit update.
func auditPurgeFinish(evidence auditPurgeReceipt, cause error) AuditFinish {
	finish := AuditFinish{Status: "success", EndedAt: time.Now().UTC()}
	if cause != nil {
		// Stable category only: a raw error may carry row data.
		finish.Status, finish.Error = "error", "audit_purge_failed"
	}
	if data, err := json.Marshal(evidence); err == nil {
		finish.Tail = string(data)
	}
	return finish
}

// writeAuditPurgeReceipt stores the running counts on the receipt row. It only
// accepts a row whose action is still audit:purge, so a purged or overwritten
// receipt stops the purge instead of losing its accounting.
func writeAuditPurgeReceipt(tx *sql.Tx, id int64, evidence auditPurgeReceipt) (int64, error) {
	data, err := json.Marshal(evidence)
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(`UPDATE audit_events SET tail = ? WHERE id = ? AND action = 'audit:purge'`,
		string(data), id)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected == 1 {
		return affected, nil
	}
	// MySQL's default CLIENT_FOUND_ROWS=false reports changed rows, so writing
	// the same tail twice (notably a zero-row purge after record(nil) already
	// wrote deleted=0) yields affected=0 even though the receipt is alive.
	// Confirm existence in the same transaction instead of treating "unchanged"
	// as "deleted"; SQLite counts matched rows and keeps the fast path.
	var present int
	if err := tx.QueryRow(`SELECT 1 FROM audit_events WHERE id = ? AND action = 'audit:purge'`, id).Scan(&present); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("audit purge receipt %d is no longer present", id)
		}
		return 0, err
	}
	return 1, nil
}

// StartAuditRetention runs audit retention once immediately and then every
// interval. A non-positive retention keeps every row and starts nothing.
func (h *GatewayHub) StartAuditRetention(retention, interval time.Duration) {
	if retention <= 0 {
		log.Printf("[gateway] audit retention disabled; audit events are kept forever (-audit-retention)")
		return
	}
	if interval <= 0 {
		interval = AuditRetentionInterval()
	}
	log.Printf("[gateway] audit retention started (retention=%s, interval=%s, batch=%d, actor=%s)",
		retention, interval, AuditPurgeBatchSize, AuditRetentionActor)
	go func() {
		h.runAuditRetention(retention)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			h.runAuditRetention(retention)
		}
	}()
}

func (h *GatewayHub) runAuditRetention(retention time.Duration) {
	cutoff := time.Now().UTC().Add(-retention)
	result, err := h.store.PurgeExpiredAudit(cutoff)
	if err != nil {
		log.Printf("[gateway] audit retention failed after deleting %d rows in %d batches before %s: %v",
			result.Deleted, result.Batches, result.Before.Format(time.RFC3339), err)
		return
	}
	if result.Deleted > 0 {
		log.Printf("[gateway] audit retention deleted %d rows in %d batches older than %s",
			result.Deleted, result.Batches, result.Before.Format(time.RFC3339))
	}
}

// StartExpiredTokenCleanup deletes tokens expired longer than ExpiredTokenGrace
// once immediately and then every interval. It is independent of audit
// retention: expired credentials must not accumulate either way.
func (h *GatewayHub) StartExpiredTokenCleanup(interval time.Duration) {
	if interval <= 0 {
		interval = ExpiredTokenSweepInterval()
	}
	log.Printf("[gateway] expired token cleanup started (grace=%s, interval=%s)", ExpiredTokenGrace, interval)
	go func() {
		h.pruneExpiredTokens()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			h.pruneExpiredTokens()
		}
	}()
}

func (h *GatewayHub) pruneExpiredTokens() {
	deleted, err := h.store.DeleteExpiredTokens(time.Now().UTC().Add(-ExpiredTokenGrace))
	if err != nil {
		log.Printf("[gateway] expired token cleanup failed: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("[gateway] removed %d tokens expired more than %s ago", deleted, ExpiredTokenGrace)
	}
}
