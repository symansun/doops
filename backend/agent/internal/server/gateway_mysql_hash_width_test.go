package server

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

// INT-74: the issued-token hash form is tokenFastHashPrefix + a hex digest
// (tokenFastHashMaxLength = 71 characters), but the MySQL schema declared
// tokens.hash, agent_enrollments.hash and edge_enrollments.hash as VARCHAR(64).
// SQLite ignores lengths, so a green suite proved nothing: on MySQL in strict
// mode every CreateToken / enrollment issuance failed with
//
//	Error 1406 (22001): Data too long for column 'hash' at row 1
//
// These tests pin the invariant "the declared MySQL width must hold what the
// code writes", not a literal 71: the width is read from the embedded DDL and
// from information_schema, and compared against tokenFastHashMaxLength.

// gatewayMySQLHashWidthTargets is the set of tables whose digest column the
// runtime writes.
func gatewayMySQLHashWidthTargets(t *testing.T) []gatewayMySQLHashColumn {
	t.Helper()
	if len(gatewayMySQLHashColumns) == 0 {
		t.Fatal("gatewayMySQLHashColumns is empty; the width guards would pass vacuously")
	}
	return gatewayMySQLHashColumns
}

// gatewayMySQLDDLHashWidths parses the embedded schema and returns the declared
// width of every hash column named by gatewayMySQLHashColumns, keyed by
// "table.column". Parsing the DDL itself means a width regression is caught
// without a MySQL server, and a missing or non-VARCHAR column is a failure, not
// a silent skip.
func gatewayMySQLDDLHashWidths(t *testing.T) map[string]int64 {
	t.Helper()
	statements, err := gatewayMySQLStatements(gatewayMySQLSchemaSQL)
	if err != nil {
		t.Fatalf("split the embedded MySQL DDL: %v", err)
	}
	create := regexp.MustCompile("(?is)^\\s*CREATE\\s+TABLE\\s+([A-Za-z0-9_]+)\\s*\\(")
	digest := regexp.MustCompile("(?mi)^\\s*hash\\s+([A-Za-z]+)\\s*\\(\\s*([0-9]+)\\s*\\)")
	widths := map[string]int64{}
	for _, statement := range statements {
		table := ""
		if match := create.FindStringSubmatch(statement); match != nil {
			table = match[1]
		}
		matches := digest.FindAllStringSubmatch(statement, -1)
		if table == "" || len(matches) == 0 {
			continue
		}
		if len(matches) > 1 {
			t.Fatalf("%s declares %d hash columns; the width guard expects one", table, len(matches))
		}
		if !strings.EqualFold(matches[0][1], "VARCHAR") {
			t.Fatalf("%s.hash is declared %s, not VARCHAR: the guard cannot derive a width", table, matches[0][1])
		}
		width, err := strconv.ParseInt(matches[0][2], 10, 64)
		if err != nil {
			t.Fatalf("%s.hash width unparsable: %v", table, err)
		}
		widths[table+".hash"] = width
	}
	return widths
}

// TestGatewayMySQLDDLHashWidthHoldsStoredForm is the regression guard that needs
// no database: it fails on the DDL alone, naming the column, when a declared
// width cannot hold the hash the code writes.
func TestGatewayMySQLDDLHashWidthHoldsStoredForm(t *testing.T) {
	if got := len(tokenFastHash("synthetic-token")); got != tokenFastHashMaxLength {
		t.Fatalf("tokenFastHash produced %d characters, want tokenFastHashMaxLength=%d", got, tokenFastHashMaxLength)
	}
	if tokenFastHashMaxLength != len(tokenFastHashPrefix)+2*32 {
		t.Fatalf("tokenFastHashMaxLength=%d does not track the prefix plus a sha256 hex digest", tokenFastHashMaxLength)
	}
	widths := gatewayMySQLDDLHashWidths(t)
	for _, target := range gatewayMySQLHashWidthTargets(t) {
		key := target.table + "." + target.column
		width, ok := widths[key]
		if !ok {
			t.Fatalf("%s: no digest column found in gateway_mysql_schema.sql", key)
		}
		if width != int64(gatewayMySQLHashColumnWidth) {
			t.Fatalf("%s: DDL declares VARCHAR(%d) but gatewayMySQLHashColumnWidth=%d; a fresh install and the startup widen would disagree", key, width, gatewayMySQLHashColumnWidth)
		}
		if width < int64(tokenFastHashMaxLength) {
			t.Fatalf("%s: DDL declares VARCHAR(%d) but the code writes %d characters (tokenFastHashMaxLength); MySQL strict mode rejects that with error 1406", key, width, tokenFastHashMaxLength)
		}
	}
}

func gatewayMySQLHashWidthTestDatabase(t *testing.T) (addr, database string) {
	t.Helper()
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the real MySQL hash-width tests")
	}
	addr = net.JoinHostPort("127.0.0.1", port)
	adminConfig := mysql.Config{User: "root", Net: "tcp", Addr: addr, AllowNativePasswords: true, Timeout: 5 * time.Second}
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open the MySQL admin connection: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("the disposable MySQL 8.4 server at %s is unavailable: %v (unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip)", addr, err)
	}
	database = "gateway_hashwidth_" + strings.ToLower(randomHex(8))
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatalf("create the hash-width database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE "+database)
	})
	return addr, database
}

func openGatewayMySQLHashWidthStore(t *testing.T, addr, database string) *GatewayStore {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp("+addr+")/"+database)
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	store, err := openGatewayMySQLStore(context.Background())
	if err != nil {
		t.Fatalf("openGatewayMySQLStore: %v", err)
	}
	return store
}

func gatewayMySQLInstalledHashWidths(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()
	widths := map[string]int64{}
	for _, target := range gatewayMySQLHashWidthTargets(t) {
		var declared sql.NullInt64
		err := db.QueryRow("SELECT CHARACTER_MAXIMUM_LENGTH FROM information_schema.COLUMNS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
			target.table, target.column).Scan(&declared)
		if err != nil {
			t.Fatalf("%s.%s: read information_schema: %v", target.table, target.column, err)
		}
		if !declared.Valid {
			t.Fatalf("%s.%s: has no character_maximum_length; the column is missing or not a character type", target.table, target.column)
		}
		widths[target.table+"."+target.column] = declared.Int64
	}
	return widths
}

func requireGatewayMySQLHashWidth(t *testing.T, db *sql.DB, want int64, why string) {
	t.Helper()
	for key, got := range gatewayMySQLInstalledHashWidths(t, db) {
		if got != want {
			t.Fatalf("%s: information_schema reports VARCHAR(%d), want %d (%s)", key, got, want, why)
		}
	}
}

// requireGatewayMySQLHashWidthHoldsStoredForm is the real-database half of the
// guard: whatever width is installed must hold tokenFastHashMaxLength.
func requireGatewayMySQLHashWidthHoldsStoredForm(t *testing.T, db *sql.DB, why string) {
	t.Helper()
	for key, got := range gatewayMySQLInstalledHashWidths(t, db) {
		if got < int64(tokenFastHashMaxLength) {
			t.Fatalf("%s: installed width %d is narrower than tokenFastHashMaxLength=%d (%s); writes would fail with error 1406", key, got, tokenFastHashMaxLength, why)
		}
	}
}

// gatewayMySQLHashWidthTableShape captures every column of the three digest
// tables except the digest itself, so the upgrade test can prove the widening
// touched nothing else.
func gatewayMySQLHashWidthTableShape(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, IFNULL(COLUMN_DEFAULT, '<null>'), IFNULL(COLLATION_NAME, '<null>') " +
		"FROM information_schema.COLUMNS " +
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN ('tokens', 'agent_enrollments', 'edge_enrollments') " +
		"ORDER BY TABLE_NAME, ORDINAL_POSITION")
	if err != nil {
		t.Fatalf("read the digest table shapes: %v", err)
	}
	defer rows.Close()
	var shape []string
	for rows.Next() {
		var table, column, columnType, nullable, defaultValue, collation string
		if err := rows.Scan(&table, &column, &columnType, &nullable, &defaultValue, &collation); err != nil {
			t.Fatalf("scan the digest table shape: %v", err)
		}
		if column == "hash" {
			continue
		}
		shape = append(shape, fmt.Sprintf("%s.%s %s nullable=%s default=%s collation=%s", table, column, columnType, nullable, defaultValue, collation))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the digest table shapes: %v", err)
	}
	if len(shape) == 0 {
		t.Fatal("the digest table shape is empty")
	}
	sort.Strings(shape)
	return shape
}

// TestGatewayMySQLHashColumnsAcceptIssuedHashes drives the three real issuance
// paths through a real MySQL store: before the fix each one returned error 1406.
func TestGatewayMySQLHashColumnsAcceptIssuedHashes(t *testing.T) {
	addr, database := gatewayMySQLHashWidthTestDatabase(t)
	store := openGatewayMySQLHashWidthStore(t, addr, database)
	defer store.Close()

	requireGatewayMySQLHashWidth(t, store.db, int64(gatewayMySQLHashColumnWidth), "fresh install from gateway_mysql_schema.sql")
	requireGatewayMySQLHashWidthHoldsStoredForm(t, store.db, "fresh install from gateway_mysql_schema.sql")

	user, err := store.CreateUser("hash-width-user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	createdToken, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "hash width"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	agentEnrollment, err := store.CreateAgentEnrollment("hash-width-cluster", "hash-width-instance")
	if err != nil {
		t.Fatalf("CreateAgentEnrollment: %v", err)
	}
	edgeEnrollment, err := store.CreateEdgeEnrollment("hash-width-cluster", "hash-width-instance")
	if err != nil {
		t.Fatalf("CreateEdgeEnrollment: %v", err)
	}

	for _, probe := range []struct {
		table, id, plain string
	}{
		{"tokens", createdToken.ID, createdToken.Plaintext},
		{"agent_enrollments", agentEnrollment.ID, agentEnrollment.Plaintext},
		{"edge_enrollments", edgeEnrollment.ID, edgeEnrollment.Plaintext},
	} {
		hash := tokenFastHash(probe.plain)
		if len(hash) != tokenFastHashMaxLength {
			t.Fatalf("%s: hash form is %d characters, want tokenFastHashMaxLength=%d", probe.table, len(hash), tokenFastHashMaxLength)
		}
		var stored int
		if err := store.db.QueryRow("SELECT CHAR_LENGTH(hash) FROM "+gatewayMySQLIdentifier(probe.table)+" WHERE id = ?", probe.id).Scan(&stored); err != nil {
			t.Fatalf("%s: read back the stored hash: %v", probe.table, err)
		}
		if stored != tokenFastHashMaxLength {
			t.Fatalf("%s: stored hash is %d characters, want %d", probe.table, stored, tokenFastHashMaxLength)
		}
	}
	// The verification path must still match the stored digest after the widen.
	auth, err := store.VerifyUserToken(createdToken.Plaintext)
	if err != nil {
		t.Fatalf("VerifyUserToken after issuance: %v", err)
	}
	if auth.TokenID != createdToken.ID {
		t.Fatalf("VerifyUserToken returned token %q, want %q", auth.TokenID, createdToken.ID)
	}
}

// TestGatewayMySQLHashWidthUpgradeFromLegacyInstall starts from the schema an
// older release installed (all three digest columns VARCHAR(64)), proves that
// shape really rejects today's hash with error 1406, then reopens the store and
// requires the startup upgrade to widen every column, preserve every other
// column and every row, and stay a no-op on a second start.
func TestGatewayMySQLHashWidthUpgradeFromLegacyInstall(t *testing.T) {
	addr, database := gatewayMySQLHashWidthTestDatabase(t)
	store := openGatewayMySQLHashWidthStore(t, addr, database)

	user, err := store.CreateUser("hash-width-legacy-user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	legacyPlain := "dgw_user_" + randomHex(4) + "_tok_" + randomHex(12) + "_" + randomHex(32)
	legacyID := "tok_" + strings.Split(legacyPlain, "_")[4]
	legacyHash, err := bcrypt.GenerateFromPassword(tokenPassword(legacyPlain), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("build the legacy bcrypt hash: %v", err)
	}
	insertToken := "INSERT INTO tokens (id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, revoked_at, created_at, issued_via) " +
		"VALUES (?, 'user', ?, ?, ?, ?, '', '', '', '', ?, '')"
	if _, err := store.db.Exec(insertToken, legacyID, user.ID, "legacy", "dgw_user_legacy", string(legacyHash), formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert the legacy bcrypt token row: %v", err)
	}

	// The old install shape: exactly what gateway_mysql_schema.sql declared
	// before INT-74.
	for _, target := range gatewayMySQLHashWidthTargets(t) {
		if _, err := store.db.Exec(fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s VARCHAR(64) NOT NULL",
			gatewayMySQLIdentifier(target.table), gatewayMySQLIdentifier(target.column))); err != nil {
			t.Fatalf("downgrade %s.%s to the legacy width: %v", target.table, target.column, err)
		}
	}
	requireGatewayMySQLHashWidth(t, store.db, 64, "legacy install fixture")
	_, tooLong := store.db.Exec(insertToken, "tok_legacyprobe", user.ID, "probe", "dgw_user_probe",
		tokenFastHash("dgw_user_probe_tok_legacyprobe_secret"), formatTime(time.Now().UTC()))
	if tooLong == nil || !strings.Contains(tooLong.Error(), "1406") {
		t.Fatalf("a 64-wide legacy schema must reject the %d-character hash with error 1406, got %v", tokenFastHashMaxLength, tooLong)
	}
	shapeBefore := gatewayMySQLHashWidthTableShape(t, store.db)
	if err := store.Close(); err != nil {
		t.Fatalf("close the legacy-shape store: %v", err)
	}

	upgraded := openGatewayMySQLHashWidthStore(t, addr, database)
	defer upgraded.Close()
	requireGatewayMySQLHashWidth(t, upgraded.db, int64(gatewayMySQLHashColumnWidth), "startup upgrade of a legacy install")
	requireGatewayMySQLHashWidthHoldsStoredForm(t, upgraded.db, "startup upgrade of a legacy install")
	if after := gatewayMySQLHashWidthTableShape(t, upgraded.db); !gatewayMySQLHashWidthStringsEqual(after, shapeBefore) {
		t.Fatalf("the startup upgrade changed columns other than the three digests:\nbefore=%v\nafter=%v", shapeBefore, after)
	}
	var legacyStored string
	if err := upgraded.db.QueryRow("SELECT hash FROM tokens WHERE id = ?", legacyID).Scan(&legacyStored); err != nil {
		t.Fatalf("read the legacy token row after the upgrade: %v", err)
	}
	if legacyStored != string(legacyHash) {
		t.Fatalf("the startup upgrade rewrote the legacy row")
	}
	// A legacy row keeps authenticating, and this same request upgrades it to
	// the fast form now that the column can hold it.
	if _, err := upgraded.VerifyUserToken(legacyPlain); err != nil {
		t.Fatalf("VerifyUserToken for the legacy row after the upgrade: %v", err)
	}
	var upgradedHash string
	if err := upgraded.db.QueryRow("SELECT hash FROM tokens WHERE id = ?", legacyID).Scan(&upgradedHash); err != nil {
		t.Fatalf("read the upgraded token row: %v", err)
	}
	if upgradedHash != tokenFastHash(legacyPlain) {
		t.Fatalf("legacy verification did not rewrite the row in the fast form")
	}
	if _, err := upgraded.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "after upgrade"}); err != nil {
		t.Fatalf("CreateToken on the upgraded schema: %v", err)
	}

	// Idempotency: a second start changes neither the widths nor any other
	// column and does not fail.
	rowsBefore := gatewayMySQLHashWidthRowCounts(t, upgraded.db)
	if err := upgraded.Close(); err != nil {
		t.Fatalf("close the upgraded store: %v", err)
	}
	again := openGatewayMySQLHashWidthStore(t, addr, database)
	defer again.Close()
	requireGatewayMySQLHashWidth(t, again.db, int64(gatewayMySQLHashColumnWidth), "second startup (idempotent)")
	requireGatewayMySQLHashWidthHoldsStoredForm(t, again.db, "second startup (idempotent)")
	if after := gatewayMySQLHashWidthTableShape(t, again.db); !gatewayMySQLHashWidthStringsEqual(after, shapeBefore) {
		t.Fatalf("the second startup changed columns other than the three digests:\nbefore=%v\nafter=%v", shapeBefore, after)
	}
	if after := gatewayMySQLHashWidthRowCounts(t, again.db); !gatewayMySQLHashWidthStringsEqual(after, rowsBefore) {
		t.Fatalf("the second startup changed row counts:\nbefore=%v\nafter=%v", rowsBefore, after)
	}
}

func gatewayMySQLHashWidthRowCounts(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var counts []string
	for _, table := range []string{"tokens", "agent_enrollments", "edge_enrollments"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + gatewayMySQLIdentifier(table)).Scan(&count); err != nil {
			t.Fatalf("%s: count rows: %v", table, err)
		}
		counts = append(counts, fmt.Sprintf("%s=%d", table, count))
	}
	return counts
}

func gatewayMySQLHashWidthStringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSQLiteTokenHashLengthFitsMySQLDDL is the cross-database half of the
// invariant. SQLite has no column length, so a SQLite-only suite can stay green
// while MySQL rejects the value; here the rows written by a real SQLite store
// are measured and compared with the width the MySQL DDL declares.
func TestSQLiteTokenHashLengthFitsMySQLDDL(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("OpenGatewayStore: %v", err)
	}
	defer store.Close()
	user, err := store.CreateUser("hash-width-sqlite-user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	createdToken, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "sqlite"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	agentEnrollment, err := store.CreateAgentEnrollment("sqlite-cluster", "sqlite-instance")
	if err != nil {
		t.Fatalf("CreateAgentEnrollment: %v", err)
	}
	edgeEnrollment, err := store.CreateEdgeEnrollment("sqlite-cluster", "sqlite-instance")
	if err != nil {
		t.Fatalf("CreateEdgeEnrollment: %v", err)
	}
	widths := gatewayMySQLDDLHashWidths(t)
	for _, probe := range []struct {
		table, id, plain string
	}{
		{"tokens", createdToken.ID, createdToken.Plaintext},
		{"agent_enrollments", agentEnrollment.ID, agentEnrollment.Plaintext},
		{"edge_enrollments", edgeEnrollment.ID, edgeEnrollment.Plaintext},
	} {
		width, ok := widths[probe.table+".hash"]
		if !ok {
			t.Fatalf("%s.hash: not declared in gateway_mysql_schema.sql", probe.table)
		}
		hash := tokenFastHash(probe.plain)
		var stored int
		if err := store.db.QueryRow("SELECT LENGTH(hash) FROM "+gatewayMySQLIdentifier(probe.table)+" WHERE id = ?", probe.id).Scan(&stored); err != nil {
			t.Fatalf("%s: read back the stored hash: %v", probe.table, err)
		}
		if stored != len(hash) {
			t.Fatalf("%s: stored hash is %d characters, want %d", probe.table, stored, len(hash))
		}
		if int64(stored) > width {
			t.Fatalf("%s.hash: the store wrote %d characters but the MySQL DDL declares VARCHAR(%d); MySQL strict mode rejects that with error 1406", probe.table, stored, width)
		}
	}
}
