package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

func TestTruncateUTF8BytesNeverSplitsRunes(t *testing.T) {
	// 3-byte Chinese characters: each "中" is 3 bytes.
	zh := strings.Repeat("中", 10) // 30 bytes
	for limit := 0; limit <= 32; limit++ {
		got := truncateUTF8Bytes(zh, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("limit=%d produced invalid UTF-8: %x", limit, got)
		}
		if len(got) > limit {
			t.Fatalf("limit=%d exceeded byte bound: len=%d", limit, len(got))
		}
		wantBytes := min(limit, len(zh))
		wantBytes -= wantBytes % 3
		if len(got) != wantBytes {
			t.Fatalf("limit=%d len=%d, want %d", limit, len(got), wantBytes)
		}
	}

	// 4-byte emoji + ASCII + malformed raw bytes.
	raw := "abc" + "\xff\xfe" + "🚀中文测试"
	got := truncateUTF8Bytes(raw, 12)
	if !utf8.ValidString(got) {
		t.Fatalf("malformed input produced invalid UTF-8: %x", got)
	}
	if len(got) > 12 {
		t.Fatalf("len=%d > 12", len(got))
	}
	if !strings.HasPrefix(got, "abc\uFFFD") {
		t.Fatalf("expected U+FFFD normalization at malformed prefix, got %q", got)
	}

	// Verify executionRecordFromMemory never produces invalid UTF-8 when truncating Chinese terminal JSON.
	rec := &executionRecord{
		id:       "exec-utf8-1",
		tool:     "shell",
		cluster:  "c1",
		instance: "i1",
		ownerID:  "u1",
		status:   "success",
		errText:  "错误\x88尾",
		terminal: []byte(strings.Repeat("汉", executionRecordTerminalLimit/3+5)),
	}
	row := executionRecordFromMemory(rec, time.Hour)
	if !row.terminalTruncated {
		t.Fatal("expected oversized terminal to be marked truncated")
	}
	if !utf8.ValidString(row.terminal) || !utf8.ValidString(row.errText) {
		t.Fatalf("executionRecordFromMemory produced invalid UTF-8: terminal_valid=%v err_valid=%v",
			utf8.ValidString(row.terminal), utf8.ValidString(row.errText))
	}
	if len(row.terminal) > executionRecordTerminalLimit {
		t.Fatalf("terminal exceeded limit: %d > %d", len(row.terminal), executionRecordTerminalLimit)
	}

	// Verify sanitizeAndCapArtifact and sanitizeMonitorEvidence keep valid UTF-8 on oversized Chinese text.
	art := sanitizeAndCapArtifact(strings.Repeat("巡", maxInspectionArtifactBytes/3+10))
	if !utf8.ValidString(art) || !strings.HasSuffix(art, "\n... [truncated]") {
		t.Fatalf("sanitizeAndCapArtifact invalid UTF-8 or missing suffix: valid=%v", utf8.ValidString(art))
	}
	ev := sanitizeMonitorEvidence(map[string]any{"msg": strings.Repeat("警", 12000)})
	evText, _ := ev["preview"].(string)
	if evText == "" {
		evText, _ = ev["redacted_text"].(string)
	}
	if !utf8.ValidString(evText) || len(evText) > 32768 || evText == "" {
		t.Fatalf("sanitizeMonitorEvidence output invalid: valid=%v len=%d ev=%+v", utf8.ValidString(evText), len(evText), ev)
	}
}

func TestGatewayMySQLShouldInterpolateParams(t *testing.T) {
	cases := []struct {
		name    string
		rawDSN  string
		sqlMode string
		want    bool
	}{
		{"default safe utf8mb4", "u:p@tcp(127.0.0.1:3306)/db?parseTime=true", "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION", true},
		{"aliyun rds empty sql_mode", "u:p@tcp(127.0.0.1:3306)/db?parseTime=true", "", true},
		{"no backslash escapes blocks auto enable", "u:p@tcp(127.0.0.1:3306)/db?parseTime=true", "STRICT_TRANS_TABLES,NO_BACKSLASH_ESCAPES", false},
		{"explicit false in DSN respected", "u:p@tcp(127.0.0.1:3306)/db?interpolateParams=false", "", false},
		{"explicit mixed-case false in DSN respected", "u:p@tcp(127.0.0.1:3306)/db?InterpolateParams=false", "", false},
		{"explicit true in DSN left as parsed", "u:p@tcp(127.0.0.1:3306)/db?interpolateParams=true", "", false},
	}
	for _, tc := range cases {
		if got := gatewayMySQLShouldInterpolateParams(tc.rawDSN, tc.sqlMode); got != tc.want {
			t.Errorf("%s: gatewayMySQLShouldInterpolateParams = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGatewayMySQLApplyRuntimeTransportDefaults(t *testing.T) {
	rawDefault := "u:p@tcp(127.0.0.1:3306)/db?parseTime=true"
	cfg, err := mysql.ParseDSN(rawDefault)
	if err != nil {
		t.Fatalf("parse default DSN: %v", err)
	}
	gatewayMySQLApplyRuntimeTransportDefaults(cfg, rawDefault, "")
	if !cfg.InterpolateParams || !cfg.CheckConnLiveness {
		t.Fatalf("expected InterpolateParams=true and CheckConnLiveness=true, got %v/%v", cfg.InterpolateParams, cfg.CheckConnLiveness)
	}
	if cfg.ReadTimeout != gatewayMySQLDefaultReadTimeout || cfg.WriteTimeout != gatewayMySQLDefaultWriteTimeout {
		t.Fatalf("unexpected default io timeouts: read=%s write=%s", cfg.ReadTimeout, cfg.WriteTimeout)
	}

	rawOverride := "u:p@tcp(127.0.0.1:3306)/db?interpolateParams=false&readTimeout=15s&writeTimeout=20s"
	cfg2, err := mysql.ParseDSN(rawOverride)
	if err != nil {
		t.Fatalf("parse override DSN: %v", err)
	}
	gatewayMySQLApplyRuntimeTransportDefaults(cfg2, rawOverride, "")
	if cfg2.InterpolateParams {
		t.Fatal("explicit interpolateParams=false in DSN was overridden")
	}
	if cfg2.ReadTimeout != 15*time.Second || cfg2.WriteTimeout != 20*time.Second {
		t.Fatalf("explicit io timeouts were overridden: read=%s write=%s", cfg2.ReadTimeout, cfg2.WriteTimeout)
	}
}

func TestGatewayUpsertBatchSQLBothDialects(t *testing.T) {
	u := agentObservationUpsert()
	if got := gatewayUpsertBatchSQLFor(gatewayDialectSQLite, u, 1); got != u.render(gatewayDialectSQLite) {
		t.Fatalf("single-row SQLite batch diverged:\n got %s\nwant %s", got, u.render(gatewayDialectSQLite))
	}
	if got := gatewayUpsertBatchSQLFor(gatewayDialectMySQL, u, 1); got != u.render(gatewayDialectMySQL) {
		t.Fatalf("single-row MySQL batch diverged:\n got %s\nwant %s", got, u.render(gatewayDialectMySQL))
	}

	sqliteBatch := gatewayUpsertBatchSQLFor(gatewayDialectSQLite, u, 3)
	if strings.Count(sqliteBatch, "(?,?,?,?,?,?,?,?,0)") != 3 || !strings.Contains(sqliteBatch, "ON CONFLICT(cluster,instance,kind,key) DO UPDATE SET") {
		t.Fatalf("unexpected SQLite batch upsert: %s", sqliteBatch)
	}
	mysqlBatch := gatewayUpsertBatchSQLFor(gatewayDialectMySQL, u, 3)
	if strings.Count(mysqlBatch, "(?,?,?,?,?,?,?,?,0)") != 3 || !strings.Contains(mysqlBatch, "ON DUPLICATE KEY UPDATE") || !strings.Contains(mysqlBatch, "`key`") {
		t.Fatalf("unexpected MySQL batch upsert: %s", mysqlBatch)
	}
	for _, issue := range gatewayMySQLRuntimeIssues(mysqlBatch) {
		t.Errorf("MySQL batch upsert flagged runtime SQL issue %s: %s", issue.code, mysqlBatch)
	}
}

func TestTouchAgentsMySQLBatchSQLShapeAndSQLiteExec(t *testing.T) {
	q := touchAgentsMySQLBatchSQL(2)
	if strings.Count(q, "WHEN cluster = ? AND instance = ? AND generation = ? THEN ?") != 2 {
		t.Fatalf("unexpected CASE branches: %s", q)
	}
	if !strings.Contains(q, "AND (cluster, instance, generation) IN ((?,?,?),(?,?,?))") {
		t.Fatalf("unexpected tuple IN predicate: %s", q)
	}
	for _, issue := range gatewayMySQLRuntimeIssues(q) {
		t.Errorf("touchAgentsMySQLBatchSQL flagged runtime SQL issue %s: %s", issue.code, q)
	}

	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	for _, a := range []AgentStatus{
		{Cluster: "b-cluster", Instance: "i2", Generation: 2, ConnectedAt: now},
		{Cluster: "a-cluster", Instance: "i1", Generation: 1, ConnectedAt: now},
	} {
		if err := store.MarkAgentOnline(a); err != nil {
			t.Fatalf("MarkAgentOnline %s/%s: %v", a.Cluster, a.Instance, err)
		}
	}
	later := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.TouchAgentsContext(context.Background(), []AgentHeartbeatUpdate{
		{Cluster: "b-cluster", Instance: "i2", Generation: 2, LastSeen: later},
		{Cluster: "a-cluster", Instance: "i1", Generation: 1, LastSeen: later},
		{Cluster: "a-cluster", Instance: "i1", Generation: 99, LastSeen: later.Add(time.Hour)}, // stale generation ignored
	}); err != nil {
		t.Fatalf("TouchAgentsContext: %v", err)
	}
	var gotLastSeen string
	if err := store.db.QueryRow(`SELECT last_seen FROM agent_status WHERE cluster='a-cluster' AND instance='i1'`).Scan(&gotLastSeen); err != nil {
		t.Fatalf("read last_seen: %v", err)
	}
	if gotLastSeen != formatTime(later.UTC()) {
		t.Fatalf("LastSeen = %s, want %s (stale generation must not overwrite)", gotLastSeen, formatTime(later.UTC()))
	}
}

func TestAgentResourceSyncReadyCachesPositiveOnly(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "ready.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	// Freshly opened store already applies asset migrations -> true and cached.
	if !store.agentResourceSyncReady(context.Background()) {
		t.Fatal("expected migrated store to report agentResourceSyncReady=true")
	}
	if !store.resourceSyncReady.Load() {
		t.Fatal("expected positive agentResourceSyncReady result to be cached")
	}
}
