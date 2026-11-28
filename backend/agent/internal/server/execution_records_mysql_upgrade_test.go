package server

// INT-91: an installed MySQL schema predates the INT-87 truncation marker, and
// the MySQL backend has no migration runner. The runtime schema preparation must
// add the two columns in place, leave old rows readable as "not truncated", and
// be a no-op on every later start.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestExecutionRecordColumnsUpgradeOnMySQL(t *testing.T) {
	gatewayMySQLCredentialE2EPort(t) // fail closed when the port is unset
	store := openGatewayMySQLCredentialE2EStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Turn the freshly installed table into the pre-INT-87 shape and write one
	// row through it, exactly what an upgraded deployment carries.
	if _, err := store.db.ExecContext(ctx,
		"ALTER TABLE execution_records DROP COLUMN terminal_truncated, DROP COLUMN terminal_bytes"); err != nil {
		t.Fatalf("drop the new columns to simulate an older install: %v", err)
	}
	legacyTerminal := `{"jsonrpc":"2.0","id":9,"result":{"stdout":"legacy result"}}`
	now := time.Now().UnixMilli()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO execution_records
		(execution_id, tool, cluster, instance, owner_id, status, error_text, terminal,
		 created_at_ms, updated_at_ms, finished_at_ms, expires_at_ms)
		VALUES ('exec-legacy-row', 'doops_shell', 'dev', 'local', 'owner-legacy', 'succeeded', '', ?, ?, ?, ?, ?)`,
		legacyTerminal, now, now, now, now+int64(time.Hour/time.Millisecond)); err != nil {
		t.Fatalf("insert the legacy row: %v", err)
	}
	var remaining int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_records'
		AND COLUMN_NAME IN ('terminal_truncated', 'terminal_bytes')`).Scan(&remaining); err != nil {
		t.Fatalf("inspect before the upgrade: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("simulated schema still has %d of the new columns", remaining)
	}

	// Restart the store against the same database: preparation must add them.
	gatewaySetDialect(gatewayDialectSQLite)
	if err := store.Close(); err != nil {
		t.Fatalf("close the pre-upgrade store: %v", err)
	}
	reopened, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("reopen the MySQL store against the old schema: %v", err)
	}
	t.Cleanup(func() {
		gatewaySetDialect(gatewayDialectSQLite)
		_ = reopened.Close()
	})
	if got := gatewayDialect(); got != gatewayDialectMySQL {
		t.Fatalf("dialect after reopen = %v, want mysql", got)
	}

	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'execution_records'
		AND COLUMN_NAME IN ('terminal_truncated', 'terminal_bytes')`).Scan(&remaining); err != nil {
		t.Fatalf("inspect after the upgrade: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("after the upgrade execution_records has %d of the 2 new columns", remaining)
	}

	var truncated int
	var bytes int64
	if err := reopened.db.QueryRowContext(ctx, `SELECT terminal_truncated, terminal_bytes FROM execution_records
		WHERE execution_id = 'exec-legacy-row'`).Scan(&truncated, &bytes); err != nil {
		t.Fatalf("read the legacy row: %v", err)
	}
	if truncated != 0 || bytes != 0 {
		t.Fatalf("legacy row reads truncated=%d bytes=%d, want 0/0", truncated, bytes)
	}

	// Read it back through the same path a client uses: the payload must be the
	// untouched terminal, not the INT-87 wrapper.
	registry := newExecutionRegistry(time.Minute)
	if err := registry.restore(reopened, time.Now()); err != nil {
		t.Fatalf("restore the record: %v", err)
	}
	record, ok := registry.get("exec-legacy-row")
	if !ok {
		t.Fatalf("legacy row did not come back from MySQL")
	}
	if record.terminalTruncated || record.terminalBytes != 0 {
		t.Fatalf("restored row claims truncated=%v bytes=%d, want untruncated", record.terminalTruncated, record.terminalBytes)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(legacyTerminal), &envelope); err != nil {
		t.Fatalf("decode the expected result: %v", err)
	}
	got, err := json.Marshal(record.view()["result"])
	if err != nil {
		t.Fatalf("encode the read-back result: %v", err)
	}
	if string(got) != string(envelope.Result) {
		t.Fatalf("read-back result = %s, want the stored %s", got, envelope.Result)
	}

	// Second startup: nothing to add, and no DDL is issued.
	added, err := gatewayMySQLEnsureExecutionRecordColumns(ctx, reopened.db)
	if err != nil {
		t.Fatalf("second preparation failed: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("second preparation added %v, want nothing", added)
	}
}
