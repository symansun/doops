package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// INT-58 follow-up: MySQL counts changed rows by default, so a receipt that
// already carries deleted=0 reports affected=0 on the next identical tail write.
// These tests pin the two production defects that produced a false failure on
// every empty retention cycle and a success receipt for a failed multi-batch
// purge.

type auditPurgeReceiptRow struct {
	ID     int64
	Status string
	Error  string
	Tail   string
}

func auditPurgeReadReceipts(t *testing.T, store *GatewayStore) []auditPurgeReceiptRow {
	t.Helper()
	rows, err := store.db.Query("SELECT id, status, error, tail FROM audit_events WHERE action = 'audit:purge' ORDER BY id")
	if err != nil {
		t.Fatalf("list audit purge receipts: %v", err)
	}
	defer rows.Close()
	var receipts []auditPurgeReceiptRow
	for rows.Next() {
		var row auditPurgeReceiptRow
		if err := rows.Scan(&row.ID, &row.Status, &row.Error, &row.Tail); err != nil {
			t.Fatalf("scan audit purge receipt: %v", err)
		}
		receipts = append(receipts, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list audit purge receipts: %v", err)
	}
	return receipts
}

func auditPurgeReadReceipt(t *testing.T, store *GatewayStore, id int64) auditPurgeReceiptRow {
	t.Helper()
	var row auditPurgeReceiptRow
	row.ID = id
	err := store.db.QueryRow("SELECT status, error, tail FROM audit_events WHERE id = ?", id).Scan(&row.Status, &row.Error, &row.Tail)
	if err != nil {
		t.Fatalf("read audit purge receipt %d: %v", id, err)
	}
	return row
}

func auditPurgeDecodeReceipt(t *testing.T, row auditPurgeReceiptRow) auditPurgeReceipt {
	t.Helper()
	var receipt auditPurgeReceipt
	if err := json.Unmarshal([]byte(row.Tail), &receipt); err != nil {
		t.Fatalf("decode receipt %d tail %q: %v", row.ID, row.Tail, err)
	}
	return receipt
}

type auditPurgeDialectCase struct {
	name string
	open func(*testing.T) *GatewayStore
}

func auditPurgeDialectCases() []auditPurgeDialectCase {
	return []auditPurgeDialectCase{
		{name: "sqlite", open: openAuditPurgeParitySQLiteStore},
		{name: "mysql", open: openAuditPurgeParityMySQLStore},
	}
}

// TestAuditPurgeRepeatEmptyReceiptAcrossDialects is defect A: two consecutive
// retention runs with no expired rows must both succeed, leave two success
// receipts, and the second receipt must still say deleted=0. Before the fix
// MySQL's second run reported "receipt ... is no longer present" and then
// conflicted trying to record the failure.
func TestAuditPurgeRepeatEmptyReceiptAcrossDialects(t *testing.T) {
	previousPause := auditPurgePause
	auditPurgePause = func() {}
	t.Cleanup(func() { auditPurgePause = previousPause })

	for _, tc := range auditPurgeDialectCases() {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.open(t)
			cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
			first, err := store.PurgeExpiredAudit(cutoff)
			if err != nil {
				t.Fatalf("first empty purge: %v (result=%+v)", err, first)
			}
			second, err := store.PurgeExpiredAudit(cutoff)
			if err != nil {
				t.Fatalf("second empty purge: %v (result=%+v)", err, second)
			}
			if first.Deleted != 0 || second.Deleted != 0 {
				t.Fatalf("empty purge deleted rows: first=%+v second=%+v", first, second)
			}
			receipts := auditPurgeReadReceipts(t, store)
			if len(receipts) != 2 {
				t.Fatalf("got %d audit:purge receipts, want 2: %+v", len(receipts), receipts)
			}
			for _, row := range receipts {
				if row.Status != "success" || row.Error != "" {
					t.Fatalf("receipt %d status=%q error=%q, want success with no error", row.ID, row.Status, row.Error)
				}
				if decoded := auditPurgeDecodeReceipt(t, row); decoded.Deleted != 0 {
					t.Fatalf("receipt %d tail deleted=%d, want 0", row.ID, decoded.Deleted)
				}
			}
			if receipts[0].ID != first.ReceiptID || receipts[1].ID != second.ReceiptID {
				t.Fatalf("receipt ids %d,%d do not match results %d,%d", receipts[0].ID, receipts[1].ID, first.ReceiptID, second.ReceiptID)
			}
		})
	}
}

// TestAuditPurgeFailureReceiptAcrossDialects is defect B: the receipt is
// completed as success before the first batch. A trigger makes the second
// batch fail after the first 5000 rows committed; the receipt must then say
// error with the real committed count, not stay success behind a terminal
// conflict. The trigger is installed on the newest seeded row, so the
// oldest-first first batch is unaffected.
func TestAuditPurgeFailureReceiptAcrossDialects(t *testing.T) {
	previousPause := auditPurgePause
	auditPurgePause = func() {}
	t.Cleanup(func() { auditPurgePause = previousPause })

	for _, tc := range auditPurgeDialectCases() {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.open(t)
			cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
			const total = AuditPurgeBatchSize + 1
			rows := make([]auditPurgeSeedRow, 0, total)
			for i := 0; i < total; i++ {
				rows = append(rows, auditPurgeSeedRow{
					action: "exec",
					// accepted is not the completed-evidence path, so these
					// rows stay for the bounded ordinary batch; success rows
					// would all be removed by deleteCompletedAuditSQL first.
					status:      "accepted",
					tail:        "{}",
					startedAtMs: cutoff.Add(-2*time.Hour).UnixMilli() + int64(i),
				})
			}
			ids := auditPurgeInsertSeeds(t, store.db, rows, "int58-failure")
			marker := ids[len(ids)-1]
			installAuditPurgeFailureTrigger(t, store, marker)

			result, err := store.PurgeExpiredAudit(cutoff)
			if err == nil {
				t.Fatalf("purge succeeded despite the injected failure (result=%+v)", result)
			}
			if !strings.Contains(err.Error(), "injected purge failure") {
				t.Fatalf("purge error lost the trigger cause: %v", err)
			}
			if result.Deleted != AuditPurgeBatchSize || result.Batches != 1 {
				t.Fatalf("purge result=%+v, want deleted=%d batches=1", result, AuditPurgeBatchSize)
			}
			receipt := auditPurgeReadReceipt(t, store, result.ReceiptID)
			if receipt.Status != "error" || receipt.Error != "audit_purge_failed" {
				t.Fatalf("receipt status=%q error=%q, want error/audit_purge_failed (err=%v tail=%s)", receipt.Status, receipt.Error, err, receipt.Tail)
			}
			decoded := auditPurgeDecodeReceipt(t, receipt)
			if decoded.Deleted != AuditPurgeBatchSize || decoded.Batches != 1 {
				t.Fatalf("receipt tail=%+v, want deleted=%d batches=1", decoded, AuditPurgeBatchSize)
			}
			var markerRows int
			if err := store.db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE id = ?", marker).Scan(&markerRows); err != nil {
				t.Fatalf("count failed-batch marker: %v", err)
			}
			if markerRows != 1 {
				t.Fatalf("failed batch removed marker row %d", marker)
			}
			var ordinary int
			if err := store.db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = 'exec'").Scan(&ordinary); err != nil {
				t.Fatalf("count remaining exec rows: %v", err)
			}
			if ordinary != 1 {
				t.Fatalf("got %d remaining exec rows, want 1 (only the marker)", ordinary)
			}
		})
	}
}

func installAuditPurgeFailureTrigger(t *testing.T, store *GatewayStore, marker int64) {
	t.Helper()
	name := "audit_purge_fail_second_batch"
	var statement string
	if store.auditPurgeDialect() == gatewayDialectMySQL {
		statement = fmt.Sprintf("CREATE TRIGGER %s BEFORE DELETE ON audit_events FOR EACH ROW BEGIN IF OLD.id = %d THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected purge failure'; END IF; END", name, marker)
	} else {
		statement = fmt.Sprintf("CREATE TRIGGER %s BEFORE DELETE ON audit_events WHEN OLD.id = %d BEGIN SELECT RAISE(FAIL, 'injected purge failure'); END", name, marker)
	}
	if _, err := store.db.Exec(statement); err != nil {
		t.Fatalf("install purge failure trigger: %v\n%s", err, statement)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec("DROP TRIGGER IF EXISTS " + name)
	})
}
