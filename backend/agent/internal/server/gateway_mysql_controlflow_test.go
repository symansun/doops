package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// INT-19: the four numbered-parameter statements now spell plain ? once per
// occurrence and repeat the argument in text order, which is the only binding
// order MySQL has. These tests execute the production statement text against a
// disposable MySQL 8.4 and assert the row values, because a shifted argument
// list stays syntactically valid and would otherwise pass a smoke test.
func TestGatewayMySQLNumberedParametersBindByTextOrder(t *testing.T) {
	db := gatewayMySQLTestOpenEmpty(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ddl := []string{
		"CREATE TABLE inventory_machines (asset_id VARCHAR(64) PRIMARY KEY, name VARCHAR(64) NOT NULL, address VARCHAR(64) NOT NULL, cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL)",
		"CREATE TABLE cluster_inventory_sources (asset_id VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL, kube_system_uid VARCHAR(64) NOT NULL DEFAULT '', cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL)",
		"CREATE TABLE inventory_business_systems (asset_id VARCHAR(64) PRIMARY KEY, name VARCHAR(64) NOT NULL)",
		"CREATE TABLE inventory_business_deployments (asset_id VARCHAR(64) NOT NULL, cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL)",
		"CREATE TABLE inventory_agent_assets (cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL, kind VARCHAR(32) NOT NULL, asset_id VARCHAR(64) NOT NULL, PRIMARY KEY (cluster, instance, kind, asset_id))",
		"CREATE TABLE machine_diagnostic_bindings (asset_id VARCHAR(64) NOT NULL, cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL, host_sha256 VARCHAR(128) NOT NULL, PRIMARY KEY (asset_id, cluster, instance))",
		"CREATE TABLE cluster_inventory_namespaces (name VARCHAR(64) NOT NULL, uid VARCHAR(64) NOT NULL, cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL)",
		"CREATE TABLE inventory_resource_catalog (id VARCHAR(64) NOT NULL, cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL, kind VARCHAR(32) NOT NULL, name VARCHAR(64) NOT NULL, details JSON NULL)",
		"CREATE TABLE credentials (id VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL, type VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL)",
		"CREATE TABLE credential_grants (credential_id VARCHAR(64) NOT NULL, state VARCHAR(32) NOT NULL, scope_json TEXT NOT NULL)",
		// cluster and instance differ on purpose: swapping them must change the
		// answer, otherwise a reordered argument list would stay invisible.
		"INSERT INTO inventory_machines VALUES ('m1','web-1','10.0.0.1','alpha','beta'), ('m2','web-2','10.0.0.2','alpha','gamma')",
		"INSERT INTO inventory_business_systems VALUES ('s1','orders')",
		"INSERT INTO inventory_business_deployments VALUES ('s1','alpha','beta')",
		"INSERT INTO inventory_agent_assets VALUES ('alpha','beta','machine','m1'), ('alpha','beta','system','s1')",
		"INSERT INTO machine_diagnostic_bindings VALUES ('m1','alpha','beta','sha-m1')",
	}
	for _, stmt := range ddl {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}

	target := func(query string, args ...any) *sql.Rows {
		t.Helper()
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		return rows
	}

	// The list statement: four repetitions of (cluster, instance) from the
	// catalog, then the manage flag.
	listQuery := workspaceCatalog + "SELECT kind,id,name,address,linked,assigned FROM assets WHERE linked OR assigned OR ? ORDER BY kind,id"
	rows := target(listQuery, targetPairArgs("alpha", "beta", 4, false)...)
	var got []string
	for rows.Next() {
		var kind, id, name, address string
		var linked, assigned bool
		if err := rows.Scan(&kind, &id, &name, &address, &linked, &assigned); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, kind+":"+id+":"+name+":"+address+"|"+boolText(linked)+"/"+boolText(assigned))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	want := []string{"machine:m1:web-1:10.0.0.1|true/true", "system:s1:orders:|true/true"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("default view selected %q, want %q (a swapped cluster/instance would link nothing)", got, want)
	}
	rows.Close()

	rows = target(listQuery, targetPairArgs("alpha", "beta", 4, true)...)
	got = nil
	for rows.Next() {
		var kind, id, name, address string
		var linked, assigned bool
		if err := rows.Scan(&kind, &id, &name, &address, &linked, &assigned); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, kind+":"+id+"|"+boolText(linked)+"/"+boolText(assigned))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	want = []string{"machine:m1|true/true", "machine:m2|false/false", "system:s1|true/true"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("manage view selected %q, want %q", got, want)
	}
	rows.Close()

	// The existence statement adds kind and id after the same four pairs. If
	// the last two arguments were bound in the other order the machine lookup
	// would answer false and the system lookup true.
	existenceQuery := workspaceCatalog + "SELECT EXISTS(SELECT 1 FROM assets WHERE kind=? AND id=?)"
	for _, probe := range []struct {
		kind, id string
		want     bool
	}{
		{"machine", "m1", true},
		{"system", "m1", false},
		{"machine", "s1", false},
	} {
		var exists bool
		if err := db.QueryRowContext(ctx, existenceQuery, targetPairArgs("alpha", "beta", 4, probe.kind, probe.id)...).Scan(&exists); err != nil {
			t.Fatalf("existence %s/%s: %v", probe.kind, probe.id, err)
		}
		if exists != probe.want {
			t.Fatalf("existence %s/%s = %v, want %v", probe.kind, probe.id, exists, probe.want)
		}
	}

	// declaredAgentResources: five unions plus the machine join and the
	// diagnostic left join repeat the pair, then the credential JSON filters.
	sourceQuery := declaredAgentResourcesSQLFor(gatewayDialectMySQL)
	rows = target(sourceQuery, targetPairArgs("alpha", "beta", 7)...)
	got = nil
	for rows.Next() {
		var kind, id, name, key, identity, credentialType, use string
		if err := rows.Scan(&kind, &id, &name, &key, &identity, &credentialType, &use); err != nil {
			t.Fatalf("scan declared: %v", err)
		}
		got = append(got, kind+":"+id+":"+name+"|"+key+"|"+identity+"|"+credentialType+"|"+use)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows declared: %v", err)
	}
	// m1 comes from its assignment join, s1 from its deployment, nothing from
	// gamma. A swapped pair would return no rows at all.
	if len(got) != 2 || !strings.HasPrefix(got[0], "machine:m1:web-1|host|") || !strings.HasPrefix(got[1], "system:s1:orders|s1|") {
		t.Fatalf("declared resources selected %q, want the alpha/beta machine and system rows", got)
	}
	rows.Close()
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// TestGatewayMySQLControlFlowStatementsExecuteOnMySQL runs the three rendered
// statements that MySQL spells differently and asserts the row-level outcome:
// UPDATE IGNORE skips the conflicting row and leaves the stored row untouched,
// <=> matches NULL to NULL, and the filtered count keeps count()'s empty-set 0.
func TestGatewayMySQLControlFlowStatementsExecuteOnMySQL(t *testing.T) {
	db := gatewayMySQLTestOpenEmpty(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ddl := []string{
		"CREATE TABLE cf_catalog (id VARCHAR(64) PRIMARY KEY, cluster VARCHAR(64) NOT NULL, instance VARCHAR(64) NOT NULL, name VARCHAR(64) NOT NULL, environment VARCHAR(64) NOT NULL, details JSON NULL, identity_key VARCHAR(128) NOT NULL, revision INTEGER NOT NULL, UNIQUE KEY cf_identity (cluster, instance, identity_key))",
		"INSERT INTO cf_catalog VALUES ('r1','alpha','beta','one','prod','{}','k1',1), ('r2','alpha','beta','two','prod','{}','k2',1)",
		"CREATE TABLE cf_samples (series_id VARCHAR(64) NOT NULL, at_ms INTEGER NOT NULL, status VARCHAR(32) NOT NULL, value VARCHAR(64) NULL)",
		"INSERT INTO cf_samples VALUES ('s1',1000,'ok',NULL), ('s1',1000,'ok','x')",
	}
	for _, stmt := range ddl {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("fixture %q: %v", stmt, err)
		}
	}

	// UPDATE OR IGNORE -> UPDATE IGNORE: r2 keeps k2 and reports 0 rows.
	update := gatewayUpdateIgnoreSQL(gatewayDialectMySQL, "cf_catalog SET cluster=?,instance=?,name=?,environment=?,details=?,identity_key=?,revision=revision+1 WHERE id=?")
	res, err := db.ExecContext(ctx, update, "alpha", "beta", "renamed", "prod", "{}", "k1", "r2")
	if err != nil {
		t.Fatalf("update ignore on a duplicate identity: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 0 {
		t.Fatalf("update ignore affected %d rows (%v), want 0: the duplicate identity must be skipped, not applied", n, err)
	}
	var name, identity string
	var revision int
	if err := db.QueryRowContext(ctx, "SELECT name,identity_key,revision FROM cf_catalog WHERE id='r2'").Scan(&name, &identity, &revision); err != nil {
		t.Fatalf("read skipped row: %v", err)
	}
	if name != "two" || identity != "k2" || revision != 1 {
		t.Fatalf("skipped row = %q/%q/%d, want two/k2/1", name, identity, revision)
	}
	res, err = db.ExecContext(ctx, update, "alpha", "beta", "updated", "prod", "{}", "k3", "r2")
	if err != nil {
		t.Fatalf("update ignore on a free identity: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("free-identity update affected %d rows (%v), want 1", n, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT name,identity_key FROM cf_catalog WHERE id='r2'").Scan(&name, &identity); err != nil {
		t.Fatalf("read updated row: %v", err)
	}
	if name != "updated" || identity != "k3" {
		t.Fatalf("updated row = %q/%q, want updated/k3", name, identity)
	}

	// IS ? -> <=>: the NULL sample exists for a NULL probe and not for 'x'.
	nullSafe := "SELECT EXISTS(SELECT 1 FROM cf_samples WHERE series_id=? AND at_ms=? AND status=? AND " + gatewayNullSafeEqualsSQL(gatewayDialectMySQL, "value", "?") + ")"
	var exists bool
	if err := db.QueryRowContext(ctx, nullSafe, "s1", 1000, "ok", nil).Scan(&exists); err != nil {
		t.Fatalf("null-safe NULL probe: %v", err)
	}
	if !exists {
		t.Fatal("null-safe NULL probe found nothing, want the row whose value is NULL")
	}
	if err := db.QueryRowContext(ctx, nullSafe, "s1", 1000, "ok", "x").Scan(&exists); err != nil {
		t.Fatalf("null-safe value probe: %v", err)
	}
	if !exists {
		t.Fatal("null-safe value probe found nothing, want the row whose value is x")
	}

	// count(*) FILTER (WHERE ...) -> COALESCE(SUM(CASE ...), 0).
	filter := "SELECT " + gatewayFilteredCountSQL(gatewayDialectMySQL, "state='open'") + "," + gatewayFilteredCountSQL(gatewayDialectMySQL, "state='missing'") + " FROM (SELECT 'open' AS state UNION ALL SELECT 'open' UNION ALL SELECT 'missing' UNION ALL SELECT NULL) x"
	var open, missing int
	if err := db.QueryRowContext(ctx, filter).Scan(&open, &missing); err != nil {
		t.Fatalf("filtered count: %v", err)
	}
	if open != 2 || missing != 1 {
		t.Fatalf("filtered count = %d open/%d missing, want 2/1", open, missing)
	}
	empty := "SELECT " + gatewayFilteredCountSQL(gatewayDialectMySQL, "state='open'") + " FROM (SELECT 'missing' AS state WHERE 1=0) x"
	if err := db.QueryRowContext(ctx, empty).Scan(&open); err != nil {
		t.Fatalf("filtered count over no rows: %v", err)
	}
	if open != 0 {
		t.Fatalf("filtered count over no rows = %d, want 0 like count()", open)
	}
}

// TestGatewayMySQLMaterializedCTEStaysSQLiteOnly records the one INT-19
// exemption with evidence instead of a claim: the purge statement is not just
// missing a keyword, MySQL rejects its DELETE-subquery shape with error 1093
// (you cannot delete from a table you also select from in a subquery), so a
// syntax-only rendering could never make it runnable.
func TestGatewayMySQLMaterializedCTEStaysSQLiteOnly(t *testing.T) {
	if !strings.Contains(deleteCompletedAuditSQL, "MATERIALIZED") || !strings.Contains(deleteCompletedAuditSQL, "json_extract") {
		t.Fatal("the audit purge statement changed; re-evaluate the INT-19 exemption before keeping the golden at one materialized_cte")
	}
	db := gatewayMySQLTestOpenEmpty(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stmt := deleteCompletedAuditSQL + " INDEXED BY idx_audit_started_ms " + " AND started_at_ms < ?"
	_, err := db.ExecContext(ctx, stmt, 1)
	if err == nil {
		t.Fatal("MySQL accepted the purge statement; the exemption and its golden entry are stale")
	}
	if !strings.Contains(err.Error(), "1064") {
		t.Fatalf("MySQL rejected the purge statement with %v, want a syntax error (1064): evidence that it must be rewritten, not rendered", err)
	}
}
