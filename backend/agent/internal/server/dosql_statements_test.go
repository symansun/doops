package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestSplitDoSQLStatementsHandlesQuotesCommentsAndDollarBodies(t *testing.T) {
	text := `-- leading comment; not a separator
UPDATE orders SET note = 'a;b' WHERE id = 1;
/* block ; comment */ INSERT INTO orders (note) VALUES ('it''s;ok');
CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1; $body$ LANGUAGE sql;
`
	statements, err := splitDoSQLStatements("postgres", text)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 3 {
		t.Fatalf("want 3 statements, got %d: %#v", len(statements), statements)
	}
	if !strings.Contains(statements[0], "'a;b'") || !strings.Contains(statements[1], "'it''s;ok'") ||
		!strings.Contains(statements[2], "$body$ SELECT 1; $body$") {
		t.Fatalf("quoted separators were split: %#v", statements)
	}
}

func TestSplitDoSQLStatementsMySQLBackslashAndHashComments(t *testing.T) {
	statements, err := splitDoSQLStatements("mysql", "# note; here\nUPDATE t SET v = 'x\\';y' WHERE id = 1; SELECT `a;b` FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 2 || !strings.Contains(statements[0], `'x\';y'`) {
		t.Fatalf("mysql split = %#v", statements)
	}
}

func TestSplitDoSQLStatementsRejectsTransactionControlAndBadInput(t *testing.T) {
	for name, text := range map[string]string{
		"begin":        "BEGIN; UPDATE t SET v = 1; COMMIT;",
		"commit":       "UPDATE t SET v = 1; COMMIT",
		"empty":        " ; -- only comment\n",
		"unterminated": "UPDATE t SET v = 'open",
		"bom":          "SELECT 1\uFEFF",
	} {
		if _, err := splitDoSQLStatements("postgres", text); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
	if _, err := splitDoSQLStatements("oracle", "BEGIN dbms_output.put_line('x'); END"); err == nil {
		t.Fatal("oracle PL/SQL block must be rejected")
	}
}

func TestDoSQLStatementFingerprintIgnoresWhitespaceOnly(t *testing.T) {
	a := doSQLStatementFingerprint([]string{"UPDATE t  SET v = 1", "SELECT 1"})
	b := doSQLStatementFingerprint([]string{"UPDATE t SET\n v = 1", "SELECT   1"})
	c := doSQLStatementFingerprint([]string{"UPDATE t SET v = 2", "SELECT 1"})
	if a != b || a == c {
		t.Fatalf("fingerprint a=%s b=%s c=%s", a, b, c)
	}
}

func TestPlanDirectDoSQLQueryRejectsMutation(t *testing.T) {
	_, err := planDirectDoSQL("postgres", api.AgentPromptParams{Operation: "ask", SQL: "SELECT 1; DELETE FROM t"})
	if err == nil || !strings.Contains(err.Error(), "dosql_query_requires_read_only: statement 2") {
		t.Fatalf("query accepted a mutation: %v", err)
	}
	request, err := planDirectDoSQL("postgres", api.AgentPromptParams{Operation: "ask", SQL: "SELECT 1; WITH x AS (SELECT 2) SELECT * FROM x"})
	if err != nil || !request.ReadOnly || len(request.Statements) != 2 {
		t.Fatalf("read batch rejected: %#v, %v", request, err)
	}
}

func TestPlanDirectDoSQLApplyVerificationAndDryRun(t *testing.T) {
	request, err := planDirectDoSQL("postgres", api.AgentPromptParams{
		Operation: "apply", SQL: "UPDATE t SET v = 1; INSERT INTO t VALUES (2)",
		SQLVerification: "SELECT count(*) FROM t", SQLDryRun: true,
	})
	if err != nil || request.ReadOnly || !request.DryRun || request.Verification == "" || len(request.Statements) != 2 {
		t.Fatalf("apply plan = %#v, %v", request, err)
	}
	if _, err := planDirectDoSQL("postgres", api.AgentPromptParams{
		Operation: "apply", SQL: "UPDATE t SET v = 1", SQLVerification: "DELETE FROM t",
	}); err == nil {
		t.Fatal("mutating verification was accepted")
	}
	if _, err := planDirectDoSQL("postgres", api.AgentPromptParams{
		Operation: "ask", SQL: "SELECT 1", SQLDryRun: true,
	}); err == nil {
		t.Fatal("query accepted --dry-run")
	}
}

func TestInferDoSQLEngineFromCredential(t *testing.T) {
	for _, testCase := range []struct {
		data     map[string]string
		engine   string
		database string
	}{
		{map[string]string{"connection_uri": "postgres://u:p@db:5432/agentcloud"}, "postgres", "agentcloud"},
		{map[string]string{"connection_uri": "oracle://u:p@db:1521/FREEPDB1"}, "oracle", "FREEPDB1"},
		{map[string]string{"SPRING_DATASOURCE_URL": "jdbc:mysql://db:3306/orders?useSSL=false"}, "mysql", "orders"},
	} {
		engine, database, err := inferDoSQLEngine(testCase.data)
		if err != nil || engine != testCase.engine || database != testCase.database {
			t.Fatalf("infer %#v = %s %s %v", testCase.data, engine, database, err)
		}
	}
	if _, _, err := inferDoSQLEngine(map[string]string{"connection_uri": "mongodb://u:p@db/x"}); err == nil {
		t.Fatal("mongodb must not use direct SQL")
	}
}

func TestDoSQLConnectionTargetsKeepCredentialsOutOfErrors(t *testing.T) {
	if _, err := doSQLConnectionTarget("postgres", "other", map[string]string{"connection_uri": "postgres://u:secret@db/agentcloud"}); err == nil ||
		strings.Contains(err.Error(), "secret") {
		t.Fatalf("database mismatch error = %v", err)
	}
	target, err := doSQLConnectionTarget("mysql", "orders", map[string]string{
		"SPRING_DATASOURCE_URL": "jdbc:mysql://db:3307/orders", "SPRING_DATASOURCE_USERNAME": "app", "SPRING_DATASOURCE_PASSWORD": "pw",
	})
	if err != nil || target.Address != "db:3307" || target.Driver != "mysql" {
		t.Fatalf("mysql target = %#v, %v", target, err)
	}
}

func TestResolveDirectDoSQLBindingSelectsByCredentialDatabase(t *testing.T) {
	const canary = "synthetic-resolve-canary"
	uris := map[string]string{
		"orders":    "postgres://app:" + canary + "@db/orders",
		"billing":   "postgres://app:" + canary + "@db/billing",
		"billing-2": "postgres://app:" + canary + "@db/billing",
	}
	read := func(_ context.Context, binding api.AgentCredentialBinding) (map[string]string, error) {
		if binding.Name == "broken" {
			return nil, errors.New("dosql_credential_secret_unavailable")
		}
		return map[string]string{"connection_uri": uris[binding.Name]}, nil
	}
	db := func(name string) api.AgentCredentialBinding {
		return api.AgentCredentialBinding{Name: name, Use: string(CredentialUseDatabase)}
	}
	registry := api.AgentCredentialBinding{Name: "registry", Use: string(CredentialUseImagePull)}
	tests := []struct {
		name     string
		bindings []api.AgentCredentialBinding
		database string
		want     string
		wantErr  string
	}{
		{name: "no database binding", bindings: []api.AgentCredentialBinding{registry}, wantErr: "dosql_credential_missing"},
		{name: "single binding without --database", bindings: []api.AgentCredentialBinding{db("orders"), registry}, want: "orders"},
		{name: "single binding with matching --database", bindings: []api.AgentCredentialBinding{db("orders")}, database: "orders", want: "orders"},
		{name: "single binding with other --database", bindings: []api.AgentCredentialBinding{db("orders")}, database: "billing", wantErr: "dosql_database_not_bound"},
		{name: "several bindings need --database", bindings: []api.AgentCredentialBinding{db("orders"), db("billing")}, wantErr: "dosql_database_required"},
		{name: "several bindings select by database", bindings: []api.AgentCredentialBinding{db("orders"), db("billing")}, database: "billing", want: "billing"},
		{name: "broken binding does not hide the match", bindings: []api.AgentCredentialBinding{db("orders"), db("broken")}, database: "orders", want: "orders"},
		{name: "broken binding is reported", bindings: []api.AgentCredentialBinding{db("orders"), db("broken")}, wantErr: "broken(dosql_credential_secret_unavailable)"},
		{name: "single broken binding keeps its reason", bindings: []api.AgentCredentialBinding{db("broken")}, wantErr: "dosql_credential_secret_unavailable"},
		{name: "two credentials for one database", bindings: []api.AgentCredentialBinding{db("billing"), db("billing-2")}, database: "billing", wantErr: "dosql_credential_ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDirectDoSQLBinding(context.Background(), tt.bindings, tt.database, read)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), canary) {
					t.Fatal("selection error exposed credential material")
				}
				return
			}
			if err != nil || got.Binding.Name != tt.want || got.Database == "" {
				t.Fatalf("selected %#v, %v; want %s", got, err, tt.want)
			}
		})
	}
}
