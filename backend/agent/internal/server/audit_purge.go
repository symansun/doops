package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// Audit retention and expired-token cleanup limits. Zero retention keeps every
// audit row: the gateway never purges audit history unless the operator opts in.
const (
	// AuditPurgeBatchSize bounds the ordinary rows removed by one statement so
	// the single SQLite connection is released between batches.
	AuditPurgeBatchSize = 5000
	// AuditPurgeBatchPause lets other writers (executions and their audit rows)
	// use the database between batches.
	AuditPurgeBatchPause = 100 * time.Millisecond
	// AuditRetentionInterval is how often the gateway runs each maintenance
	// pass.
	AuditRetentionInterval = 6 * time.Hour
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
const auditIndexHint = ` INDEXED BY idx_audit_started_at `

// deleteCompletedAuditSQL removes upgrade evidence that a later terminal
// observation has replaced. Controller admission and polls stay until their
// exact Job has a later success/error row. The CTE is materialized and the
// predicate and the deletion run in one statement, so an admission row is never
// deleted without the terminal row being evaluated in the same snapshot. The
// cutoff is a plain range on started_at so idx_audit_started_at serves it
// (julianday(started_at) could not use any index).
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
	WHERE started_at < ?
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

// auditPurgePause runs between deletion batches. Production sleeps
// AuditPurgeBatchPause; tests replace it to observe batching without waiting.
var auditPurgePause = func() { time.Sleep(AuditPurgeBatchPause) }

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
		purge.discardReceipt()
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
func (p *auditPurgeSession) discardReceipt() {
	_, _ = p.store.db.Exec(`DELETE FROM audit_events
		WHERE id = ? AND action = 'audit:purge' AND status IN ('running', 'executing')`, p.id)
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
	cutoff := formatTime(before.UTC())
	tx, err := p.store.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	deleted, complete, err := drainAuditTx(tx, cutoff, before.UTC().UnixMilli())
	if err != nil {
		return 0, false, err
	}
	if complete && deleted > 0 {
		// Another writer may have added rows after this transaction took its
		// read snapshot. Delete them here so the committed receipt covers
		// every row this run removed.
		extra, _, err := drainAuditTx(tx, cutoff, before.UTC().UnixMilli())
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

// drainAuditTx applies the upgrade-evidence rule and then deletes one bounded
// ordinary batch. It reports whether the ordinary batch was short, meaning no
// ordinary row older than the cutoff is visible in this transaction.
func drainAuditTx(tx *sql.Tx, cutoff string, cutoffMs int64) (int64, bool, error) {
	deleted, err := execRowsAffected(tx, deleteCompletedAuditSQL, cutoff)
	if err != nil {
		return 0, false, err
	}
	batch, err := execRowsAffected(tx, deleteAuditBatchSQL, cutoffMs, AuditPurgeBatchSize)
	if err != nil {
		return 0, false, err
	}
	return deleted + batch, batch < AuditPurgeBatchSize, nil
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

// fail reopens the completed receipt as an error and records the failure. The
// returned error is the purge failure; a receipt that cannot record it is
// reported alongside so the caller cannot mistake the outcome for success.
func (p *auditPurgeSession) fail(cause error) error {
	if err := p.store.FinishAudit(p.id, auditPurgeFinish(p.receipt, cause)); err != nil {
		return fmt.Errorf("%w; audit purge receipt %d could not record the failure: %v", cause, p.id, err)
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
	if affected != 1 {
		return 0, fmt.Errorf("audit purge receipt %d is no longer present", id)
	}
	return affected, nil
}

// StartAuditRetention runs audit retention once immediately and then every
// interval. A non-positive retention keeps every row and starts nothing.
func (h *GatewayHub) StartAuditRetention(retention, interval time.Duration) {
	if retention <= 0 {
		log.Printf("[gateway] audit retention disabled; audit events are kept forever (-audit-retention)")
		return
	}
	if interval <= 0 {
		interval = AuditRetentionInterval
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
		interval = AuditRetentionInterval
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
