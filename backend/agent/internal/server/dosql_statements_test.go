package server

import (
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

func TestSelectDirectDoSQLBindingRequiresExactlyOneDatabaseBinding(t *testing.T) {
	db := func(name string) api.AgentCredentialBinding {
		return api.AgentCredentialBinding{Name: name, Use: string(CredentialUseDatabase)}
	}
	if _, err := selectDirectDoSQLBinding(nil); err == nil {
		t.Fatal("missing binding accepted")
	}
	if _, err := selectDirectDoSQLBinding([]api.AgentCredentialBinding{db("a"), db("b")}); err == nil ||
		!strings.Contains(err.Error(), "a,b") {
		t.Fatalf("ambiguous binding error = %v", err)
	}
	if got, err := selectDirectDoSQLBinding([]api.AgentCredentialBinding{db("a"), {Name: "reg", Use: string(CredentialUseImagePull)}}); err != nil || got.Name != "a" {
		t.Fatalf("single binding = %#v, %v", got, err)
	}
}
