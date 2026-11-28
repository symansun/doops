package server

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// INT-38: the audit retention statements are dialect pairs. The SQLite text
// keeps its INDEXED BY pin and its DELETE-from-a-self-reading-subquery shape;
// MySQL 8.4 rejects that shape (Error 1064 for AS MATERIALIZED and INDEXED BY,
// Error 1093 for the bare subquery), so the MySQL renderings select ids through
// a derived table. These tests hold both renderings to the same rows on a real
// mysql:8.4 and prove the SQLite text really cannot run there.

// auditPurgeSeedRow is one audit_events fixture row. tail is the raw text so a
// row can carry invalid JSON, which the terminal CTE must treat as {}.
type auditPurgeSeedRow struct {
	action        string
	status        string
	correlationID string
	tail          string
	startedAtMs   int64
}

const auditPurgeSeedInsertSQL = `INSERT INTO audit_events
	(action, status, tail, started_at_ms, started_at, correlation_id, scope_json, command_summary, error, ended_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, '', '', '')`

// auditPurgeParitySeeds builds the fixture that exercises every protection
// rule: active states, an abandoned controller admission, an admission with an
// empty correlation, an admission whose terminal row is not terminal yet, and a
// receipt row. expectedDeleted returns the indexes of rows a purge must remove.
func auditPurgeParitySeeds(cutoff time.Time) ([]auditPurgeSeedRow, []int) {
	old := cutoff.Add(-time.Hour).UnixMilli()
	fresh := cutoff.Add(time.Hour).UnixMilli()
	seeds := []auditPurgeSeedRow{
		{action: "exec", status: "success", tail: `{}`, startedAtMs: old},                                                                                     // 0: ordinary completed -> deleted
		{action: "exec", status: "running", tail: `{}`, startedAtMs: old},                                                                                     // 1: live -> kept
		{action: "exec", status: "outcome_unknown", tail: `{}`, startedAtMs: old},                                                                             // 2: unresolved -> kept
		{action: "exec", status: "executing", tail: `{}`, startedAtMs: old},                                                                                   // 3: live -> kept
		{action: "agent:upgrade", status: "accepted", correlationID: "corr-ok", tail: `{"result":{"job":"job-1"}}`, startedAtMs: old},                         // 4: replaced admission -> deleted
		{action: "agent:upgrade", status: "success", correlationID: "corr-ok", tail: `{"result":{"job":"job-1","outcome":"succeeded"}}`, startedAtMs: old},    // 5: terminal -> deleted
		{action: "agent:upgrade", status: "accepted", correlationID: "corr-orphan", tail: `{"result":{"job":"job-2"}}`, startedAtMs: old},                     // 6: no terminal -> kept
		{action: "agent:upgrade", status: "accepted", correlationID: "", tail: `{"result":{"job":"job-3"}}`, startedAtMs: old},                                // 7: empty correlation -> kept
		{action: "agent:upgrade", status: "success", correlationID: "", tail: `{"result":{"job":"job-3","outcome":"succeeded"}}`, startedAtMs: old},           // 8: terminal -> deleted
		{action: "agent:upgrade", status: "accepted", correlationID: "corr-pending", tail: `{"result":{"job":"job-4"}}`, startedAtMs: old},                    // 9: terminal outcome not terminal -> kept
		{action: "agent:upgrade", status: "success", correlationID: "corr-pending", tail: `{"result":{"job":"job-4","outcome":"unknown"}}`, startedAtMs: old}, // 10: ordinary status -> deleted
		{action: "audit:purge", status: "success", tail: `{}`, startedAtMs: old},                                                                              // 11: receipt -> kept
		{action: "exec", status: "success", tail: `{}`, startedAtMs: fresh},                                                                                   // 12: newer than cutoff -> kept
		{action: "exec", status: "success", tail: `not json`, startedAtMs: old},                                                                               // 13: invalid tail -> deleted
		{action: "exec", status: "accepted", correlationID: "", tail: `{}`, startedAtMs: old},                                                                 // 14: ordinary accepted -> deleted by batch
	}
	return seeds, []int{0, 4, 5, 8, 10, 13, 14}
}

func openAuditPurgeParitySQLiteStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open SQLite purge store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openAuditPurgeParityMySQLStore(t *testing.T) *GatewayStore {
	t.Helper()
	db := gatewayMySQLTestOpenEmpty(t)
	if _, err := installGatewayMySQLSchema(context.Background(), db); err != nil {
		t.Fatalf("install MySQL schema: %v", err)
	}
	return &GatewayStore{db: db, dialect: gatewayDialectMySQL, backend: "mysql"}
}

func auditPurgeInsertSeeds(t *testing.T, db *sql.DB, rows []auditPurgeSeedRow, marker string) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		res, err := db.Exec(auditPurgeSeedInsertSQL, row.action, row.status, row.tail, row.startedAtMs, formatTime(time.UnixMilli(row.startedAtMs)), row.correlationID, marker)
		if err != nil {
			t.Fatalf("insert audit seed: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("audit seed id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func auditPurgeAllIDs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("list audit ids: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan audit id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list audit ids: %v", err)
	}
	return ids
}

func auditPurgeIDSet(ids []int64) map[int64]bool {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func auditPurgeRemoved(before, after []int64) []int64 {
	keep := auditPurgeIDSet(after)
	var removed []int64
	for _, id := range before {
		if !keep[id] {
			removed = append(removed, id)
		}
	}
	return removed
}

func auditPurgeSorted(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func requireAuditMySQLNumber(t *testing.T, err error, want uint16) {
	t.Helper()
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		t.Fatalf("got err %v (%T), want a MySQL error %d", err, err, want)
	}
	if mysqlErr.Number != want {
		t.Fatalf("got MySQL error %d (%s), want %d", mysqlErr.Number, mysqlErr.Message, want)
	}
}

// TestAuditPurgeParityAcrossDialects is guard ① and ③: the same fixture purged
// on SQLite and on MySQL must leave exactly the same rows, and every
// active-status row must survive on both.
func TestAuditPurgeParityAcrossDialects(t *testing.T) {
	sqliteStore := openAuditPurgeParitySQLiteStore(t)
	mysqlStore := openAuditPurgeParityMySQLStore(t)
	previousPause := auditPurgePause
	auditPurgePause = func() {}
	t.Cleanup(func() { auditPurgePause = previousPause })

	cutoff := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	seeds, deletedIndexes := auditPurgeParitySeeds(cutoff)

	sqliteIDs := auditPurgeInsertSeeds(t, sqliteStore.db, seeds, "int38-parity")
	mysqlIDs := auditPurgeInsertSeeds(t, mysqlStore.db, seeds, "int38-parity")
	if !reflect.DeepEqual(sqliteIDs, mysqlIDs) {
		t.Fatalf("seeded ids differ: sqlite=%v mysql=%v", sqliteIDs, mysqlIDs)
	}

	sqliteResult, err := sqliteStore.PurgeExpiredAudit(cutoff)
	if err != nil {
		t.Fatalf("sqlite purge: %v", err)
	}
	mysqlResult, err := mysqlStore.PurgeExpiredAudit(cutoff)
	if err != nil {
		t.Fatalf("mysql purge: %v (result=%+v)", err, mysqlResult)
	}

	wantDeleted := make([]int64, 0, len(deletedIndexes))
	for _, index := range deletedIndexes {
		wantDeleted = append(wantDeleted, sqliteIDs[index])
	}
	wantDeleted = auditPurgeSorted(wantDeleted)

	removedBySQLite := auditPurgeRemoved(sqliteIDs, auditPurgeAllIDs(t, sqliteStore.db))
	removedByMySQL := auditPurgeRemoved(mysqlIDs, auditPurgeAllIDs(t, mysqlStore.db))
	if !reflect.DeepEqual(auditPurgeSorted(removedBySQLite), auditPurgeSorted(removedByMySQL)) {
		t.Fatalf("one purge deleted different rows: sqlite=%v mysql=%v", auditPurgeSorted(removedBySQLite), auditPurgeSorted(removedByMySQL))
	}
	if !reflect.DeepEqual(auditPurgeSorted(removedBySQLite), wantDeleted) {
		t.Fatalf("purge deleted %v, want %v", auditPurgeSorted(removedBySQLite), wantDeleted)
	}
	if sqliteResult.Deleted != int64(len(wantDeleted)) || mysqlResult.Deleted != int64(len(wantDeleted)) {
		t.Fatalf("purge counts sqlite=%d mysql=%d, want %d", sqliteResult.Deleted, mysqlResult.Deleted, len(wantDeleted))
	}
	for _, index := range []int{1, 2, 3} {
		for name, ids := range map[string][]int64{"sqlite": sqliteIDs, "mysql": mysqlIDs} {
			var count int
			if err := map[string]*GatewayStore{"sqlite": sqliteStore, "mysql": mysqlStore}[name].db.QueryRow(
				`SELECT COUNT(*) FROM audit_events WHERE id = ?`, ids[index]).Scan(&count); err != nil {
				t.Fatalf("count %s active row: %v", name, err)
			}
			if count != 1 {
				t.Fatalf("%s deleted active-status row %d (%s)", name, ids[index], seeds[index].status)
			}
		}
	}
	if sqliteResult.ReceiptID == 0 || mysqlResult.ReceiptID == 0 {
		t.Fatalf("purge receipts missing: sqlite=%d mysql=%d", sqliteResult.ReceiptID, mysqlResult.ReceiptID)
	}
}

// TestAuditPurgeBatchParityAcrossDialects is the batch half of guard ① and the
// target of mutation ③: one batch must cap at AuditPurgeBatchSize rows, take
// them oldest-first, and pick the same rows on both backends.
func TestAuditPurgeBatchParityAcrossDialects(t *testing.T) {
	sqliteStore := openAuditPurgeParitySQLiteStore(t)
	mysqlStore := openAuditPurgeParityMySQLStore(t)

	cutoff := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	// The reverse timestamp order makes "ORDER BY id" pick a different first
	// batch than "ORDER BY started_at_ms, id". Every row must sit below the
	// cutoff, so the base is two hours before it and the offsets stay under an
	// hour and a half.
	const total = AuditPurgeBatchSize + 3
	rows := make([]auditPurgeSeedRow, 0, total)
	for i := 0; i < total; i++ {
		rows = append(rows, auditPurgeSeedRow{
			action:      "exec",
			status:      "success",
			tail:        `{}`,
			startedAtMs: cutoff.Add(-2*time.Hour).UnixMilli() + int64(total-i)*1000,
		})
	}
	sqliteIDs := auditPurgeInsertSeeds(t, sqliteStore.db, rows, "int38-batch")
	mysqlIDs := auditPurgeInsertSeeds(t, mysqlStore.db, rows, "int38-batch")
	if !reflect.DeepEqual(sqliteIDs, mysqlIDs) {
		t.Fatalf("seeded ids differ: sqlite=%v mysql=%v", sqliteIDs, mysqlIDs)
	}

	type batchRun struct {
		removed  []int64
		complete bool
	}
	runBatch := func(store *GatewayStore) batchRun {
		t.Helper()
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("begin batch tx: %v", err)
		}
		defer tx.Rollback()
		before := auditPurgeAllIDsInTx(t, tx)
		deleted, err := execRowsAffected(tx, deleteAuditBatchSQLFor(store.auditPurgeDialect()), cutoff.UnixMilli(), AuditPurgeBatchSize)
		if err != nil {
			t.Fatalf("batch (%s): %v", store.backend, err)
		}
		after := auditPurgeAllIDsInTx(t, tx)
		t.Logf("batch %s rows_affected=%d before_ids=%d after_ids=%d", store.backend, deleted, len(before), len(after))
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit batch: %v", err)
		}
		return batchRun{removed: auditPurgeRemoved(before, after), complete: deleted < AuditPurgeBatchSize}
	}

	sqliteRun := runBatch(sqliteStore)
	mysqlRun := runBatch(mysqlStore)
	if len(sqliteRun.removed) != AuditPurgeBatchSize || len(mysqlRun.removed) != AuditPurgeBatchSize {
		t.Fatalf("batch removed sqlite=%d mysql=%d rows, want %d", len(sqliteRun.removed), len(mysqlRun.removed), AuditPurgeBatchSize)
	}
	if sqliteRun.complete || mysqlRun.complete {
		t.Fatalf("a full batch must not report the window drained: sqlite=%v mysql=%v", sqliteRun.complete, mysqlRun.complete)
	}
	if !reflect.DeepEqual(auditPurgeSorted(sqliteRun.removed), auditPurgeSorted(mysqlRun.removed)) {
		t.Fatalf("one batch deleted different rows: sqlite=%v mysql=%v", auditPurgeSorted(sqliteRun.removed), auditPurgeSorted(mysqlRun.removed))
	}
	// The oldest rows are the ones inserted last (timestamp runs backwards).
	wantRemoved := auditPurgeSorted(sqliteIDs[3:])
	if !reflect.DeepEqual(auditPurgeSorted(sqliteRun.removed), wantRemoved) {
		t.Fatalf("batch deleted %v, want the oldest %d rows %v", auditPurgeSorted(sqliteRun.removed), AuditPurgeBatchSize, wantRemoved)
	}
}

func auditPurgeAllIDsInTx(t *testing.T, tx *sql.Tx) []int64 {
	t.Helper()
	rows, err := tx.Query(`SELECT id FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatalf("list audit ids in tx: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan audit id in tx: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list audit ids in tx: %v", err)
	}
	return ids
}

// TestAuditPurgeMySQLRejectsSQLiteRendering is guard ③: the SQLite text must
// stay dialect-specific, and the MySQL renderings must not carry SQLite-only
// clauses. It also pins the exact errors a regression would produce.
func TestAuditPurgeMySQLRejectsSQLiteRendering(t *testing.T) {
	store := openAuditPurgeParityMySQLStore(t)
	_, err := store.db.Exec(deleteCompletedAuditSQL, int64(0))
	requireAuditMySQLNumber(t, err, 1064)
	// The batch statement hits INDEXED BY before it reaches the LIMIT subquery,
	// so the SQLite rendering fails with 1064 here. Mutation ② removes the hint
	// and exposes 1235 on the bare IN (SELECT ... LIMIT ?) shape.
	_, err = store.db.Exec(deleteAuditBatchSQL, int64(0), AuditPurgeBatchSize)
	requireAuditMySQLNumber(t, err, 1064)

	for name, rendered := range map[string]string{
		"completed": deleteCompletedAuditSQLFor(gatewayDialectMySQL),
		"batch":     deleteAuditBatchSQLFor(gatewayDialectMySQL),
	} {
		for _, forbidden := range []string{"MATERIALIZED", "INDEXED BY"} {
			if containsFold(rendered, forbidden) {
				t.Fatalf("MySQL %s rendering carries SQLite-only %q: %s", name, forbidden, rendered)
			}
		}
		if !containsFold(rendered, "JOIN (") || !containsFold(rendered, ") AS batch ON batch.id = target.id") {
			t.Fatalf("MySQL %s rendering lost its materialized derived table: %s", name, rendered)
		}
	}
	if deleteCompletedAuditSQLFor(gatewayDialectSQLite) != deleteCompletedAuditSQL || deleteAuditBatchSQLFor(gatewayDialectSQLite) != deleteAuditBatchSQL {
		t.Fatal("SQLite renderings must stay byte-identical to the pinned statements")
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
