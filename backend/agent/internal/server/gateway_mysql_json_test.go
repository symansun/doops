package server

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestGatewayMySQLJSONExpansionsExecuteOnMySQL runs the rendered JSON statements
// on MySQL 8.4 and compares them with the same statements rendered for SQLite.
// The point is not syntax (the audit proves spelling) but semantics: JSON_TABLE
// must expand the same members json_each expands, the object probe must see the
// same kind values, and the aggregate must produce the same set the reader sorts
// into the same order.
func TestGatewayMySQLJSONExpansionsExecuteOnMySQL(t *testing.T) {
	my := gatewayMySQLTestOpenEmpty(t)
	ctx := t.Context()
	for _, stmt := range []string{
		"CREATE TABLE json_probe(id BIGINT PRIMARY KEY)",
		"CREATE TABLE json_tags(tag VARCHAR(64))",
		"CREATE TABLE json_objects(asset_id VARCHAR(64), objects JSON)",
	} {
		if _, err := my.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	for _, id := range []int{1, 2, 3} {
		if _, err := my.ExecContext(ctx, "INSERT INTO json_probe(id) VALUES(?)", id); err != nil {
			t.Fatalf("fixture id: %v", err)
		}
	}
	for _, tag := range []string{"gamma", "alpha", "beta"} {
		if _, err := my.ExecContext(ctx, "INSERT INTO json_tags(tag) VALUES(?)", tag); err != nil {
			t.Fatalf("fixture tag: %v", err)
		}
	}
	if _, err := my.ExecContext(ctx, "INSERT INTO json_objects(asset_id,objects) VALUES(?,?)", "a1",
		`{"w1":{"kind":"Deployment"},"w2":{"kind":"Pod"},"w.3":{"kind":"StatefulSet"}}`); err != nil {
		t.Fatalf("fixture objects: %v", err)
	}

	// Scalar expansion: IN and NOT IN over a JSON array of numbers.
	inSQL := "SELECT id FROM json_probe WHERE id IN (" + gatewayJSONEachValuesSQL(gatewayDialectMySQL, "?") + ") ORDER BY id"
	got := scanInt64s(t, my, inSQL, "[2,3]")
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("scalar IN selected %v, want [2 3]", got)
	}
	notIn := "SELECT id FROM json_probe WHERE id NOT IN (" + gatewayJSONEachValuesSQL(gatewayDialectMySQL, "?") + ") ORDER BY id"
	got = scanInt64s(t, my, notIn, "[2,3]")
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("scalar NOT IN selected %v, want [1]", got)
	}

	// Object keys: MySQL must project JSON_KEYS before JSON_TABLE; a key with a
	// dot proves the escaped path used by the object probe below.
	keysSQL := "SELECT k FROM (" + gatewayJSONEachKeysSQL(gatewayDialectMySQL, "?") + ") ks ORDER BY k"
	rows, err := my.QueryContext(ctx, keysSQL, `{"beta":1,"alpha":2,"w.3":3}`)
	if err != nil {
		t.Fatalf("object keys: %v", err)
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan key: %v", err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if strings.Join(keys, ",") != "alpha,beta,w.3" {
		t.Fatalf("object keys = %v, want [alpha beta w.3]", keys)
	}

	// Object probe: the production predicate, rendered for both dialects and
	// executed against identical rows.
	probe := func(dialect gatewaySQLDialect, db *sql.DB, key string) bool {
		from, keyExpr, kindExpr := gatewayJSONObjectJoinSQL(dialect, "json_objects x", "x.objects", "o")
		sqlText := "SELECT EXISTS(SELECT 1 FROM " + from + " WHERE x.asset_id=? AND " + keyExpr + "=? AND " + kindExpr + "<>'Pod')"
		var valid bool
		if err := db.QueryRowContext(ctx, sqlText, "a1", key).Scan(&valid); err != nil {
			t.Fatalf("%s object probe for %q: %v", dialect, key, err)
		}
		return valid
	}
	sq := openSQLiteForJSONTest(t)
	for _, key := range []string{"w1", "w2", "w.3", "absent"} {
		if myValid, sqValid := probe(gatewayDialectMySQL, my, key), probe(gatewayDialectSQLite, sq, key); myValid != sqValid {
			t.Fatalf("object probe %q: MySQL says %v, SQLite says %v", key, myValid, sqValid)
		}
	}
	if !probe(gatewayDialectMySQL, my, "w1") || probe(gatewayDialectMySQL, my, "w2") || !probe(gatewayDialectMySQL, my, "w.3") {
		t.Fatalf("object probe answered inconsistently: want w1 true, w2 false (kind Pod), w.3 true (dotted key)")
	}

	// Aggregate: MySQL has no ORDER BY inside JSON_ARRAYAGG, so the array
	// arrives unordered and the reader sorts it. The same aggregation rendered
	// for SQLite already arrives sorted; both decode to the same slice.
	tagsSQL := "SELECT " + gatewayJSONOrderedArraySQL(gatewayDialectMySQL, "t.tag", "t.tag") + " FROM json_tags t"
	var raw string
	if err := my.QueryRowContext(ctx, tagsSQL).Scan(&raw); err != nil {
		t.Fatalf("aggregate tags: %v", err)
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		t.Fatalf("decode aggregate %q: %v", raw, err)
	}
	sort.Strings(tags)
	if strings.Join(tags, ",") != "alpha,beta,gamma" {
		t.Fatalf("aggregate tags after reader sort = %v, want [alpha beta gamma]", tags)
	}
	sqTagsSQL := "SELECT " + gatewayJSONOrderedArraySQL(gatewayDialectSQLite, "t.tag", "t.tag") + " FROM json_tags t"
	if _, err := sq.ExecContext(ctx, "CREATE TABLE json_tags(tag VARCHAR(64))"); err != nil {
		t.Fatalf("sqlite fixture: %v", err)
	}
	for _, tag := range []string{"gamma", "alpha", "beta"} {
		if _, err := sq.ExecContext(ctx, "INSERT INTO json_tags(tag) VALUES(?)", tag); err != nil {
			t.Fatalf("sqlite fixture tag: %v", err)
		}
	}
	var sqRaw string
	if err := sq.QueryRowContext(ctx, sqTagsSQL).Scan(&sqRaw); err != nil {
		t.Fatalf("sqlite aggregate: %v", err)
	}
	if sqRaw != `["alpha","beta","gamma"]` {
		t.Fatalf("sqlite aggregate = %s, want the ordered array the reader sort reproduces", sqRaw)
	}

	// Empty set: json_group_array returns [], JSON_ARRAYAGG returns NULL, and
	// COALESCE keeps the two identical.
	if _, err := my.ExecContext(ctx, "DELETE FROM json_tags"); err != nil {
		t.Fatalf("empty fixture: %v", err)
	}
	if err := my.QueryRowContext(ctx, tagsSQL).Scan(&raw); err != nil || raw != "[]" {
		t.Fatalf("empty aggregate = %q (err %v), want []", raw, err)
	}
}

// TestGatewayMySQLJSONRejectsMalformedInput proves no SQL-level json_valid guard
// is needed: both engines reject malformed JSON where it is parsed, so the
// application keeps its Go-side validation and neither dialect silently treats
// bad input as an empty list.
func TestGatewayMySQLJSONRejectsMalformedInput(t *testing.T) {
	my := gatewayMySQLTestOpenEmpty(t)
	ctx := t.Context()
	_, err := my.ExecContext(ctx, "SELECT COUNT(*) FROM ("+gatewayJSONEachValuesSQL(gatewayDialectMySQL, "?")+") x", "not json")
	if err == nil || !strings.Contains(err.Error(), "Invalid JSON") {
		t.Fatalf("MySQL accepted malformed JSON (err %v), want an Invalid JSON rejection", err)
	}
	sq := openSQLiteForJSONTest(t)
	_, err = sq.ExecContext(ctx, "SELECT COUNT(*) FROM ("+gatewayJSONEachValuesSQL(gatewayDialectSQLite, "?")+") x", "not json")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "malformed json") {
		t.Fatalf("SQLite accepted malformed JSON (err %v), want a malformed JSON rejection", err)
	}
}

func scanInt64s(t *testing.T, db *sql.DB, query string, arg string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query, arg)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	return out
}

func openSQLiteForJSONTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE TABLE json_objects(asset_id TEXT, objects TEXT)"); err != nil {
		t.Fatalf("sqlite fixture: %v", err)
	}
	if _, err := db.Exec("INSERT INTO json_objects(asset_id,objects) VALUES(?,?)", "a1",
		`{"w1":{"kind":"Deployment"},"w2":{"kind":"Pod"},"w.3":{"kind":"StatefulSet"}}`); err != nil {
		t.Fatalf("sqlite fixture row: %v", err)
	}
	return db
}

// TestGatewayJSONReaderOwnsOrder pins the second half of the ordering contract:
// MySQL's JSON_ARRAYAGG cannot sort, so the decoded array must be normalized by
// the reader. Without it the two dialects would return the same members in
// different orders.
func TestGatewayJSONReaderOwnsOrder(t *testing.T) {
	raw := json.RawMessage(`[{"username":"zeta","credential_id":"c2"},{"username":"alpha","credential_id":"c1"}]`)
	got := string(normalizeLoginAccounts(raw))
	want := `[{"username":"alpha","credential_id":"c1"},{"username":"zeta","credential_id":"c2"}]`
	if got != want {
		t.Fatalf("normalized login accounts = %s, want %s", got, want)
	}
	if got := string(normalizeLoginAccounts(nil)); got != "" {
		t.Fatalf("empty input became %q, want untouched", got)
	}
}
