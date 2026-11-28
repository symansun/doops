package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/internal/dosql/lexer"
)

func TestPolicyEvaluateAndOverride(t *testing.T) {
	stmts, err := lexer.Split("mysql", "CREATE TABLE t (price FLOAT) ENGINE=InnoDB DEFAULT CHARSET=latin1; UPDATE t SET price = 0;")
	if err != nil {
		t.Fatal(err)
	}
	report := Evaluate("mysql", stmts, Default(), nil)
	if report.Passed || report.Errors != 1 || report.Warnings < 3 {
		t.Fatalf("unexpected report: %+v", report)
	}

	// Overriding the blocking rule allows execution while preserving audit record.
	overridden := Evaluate("mysql", stmts, Default(), []string{"forbid_unbounded_update_delete"})
	if !overridden.Passed || overridden.Errors != 0 || len(overridden.Overrides) != 1 {
		t.Fatalf("expected override to pass: %+v", overridden)
	}
}

func TestPolicyPostgresAndExpandContract(t *testing.T) {
	stmts, err := lexer.Split("postgres", "CREATE INDEX idx_a ON orders(a); ALTER TABLE orders DROP COLUMN legacy;")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseYAML([]byte("schema: dosql.policy.v1\npostgres:\n  require_concurrent_index: error\nrelease:\n  expand_contract_guard: error\n"))
	if err != nil {
		t.Fatal(err)
	}
	report := Evaluate("postgres", stmts, p, nil)
	if report.Passed || report.Errors != 2 {
		t.Fatalf("expected 2 policy errors, got %+v", report)
	}
}

func TestPolicyIgnoresKeywordsInsideLiteralsCommentsAndValidatesSchema(t *testing.T) {
	// 'where' inside a string literal or SQL comment MUST NOT bypass forbid_unbounded_update_delete!
	stmts, err := lexer.Split("mysql", "UPDATE orders SET note = 'where are you'; DELETE /* where id = 1 */ FROM orders; TRUNCATE TABLE logs;")
	if err != nil {
		t.Fatal(err)
	}
	rep := Evaluate("mysql", stmts, Default(), nil)
	if rep.Passed || rep.Errors != 2 || rep.Warnings < 1 {
		t.Fatalf("expected 2 unbounded DML errors + truncate warning, got %+v", rep)
	}

	// LoadFile + invalid schema version fails closed.
	badFile := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(badFile, []byte("schema: dosql.policy.v99\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(badFile); err == nil || !strings.Contains(err.Error(), "dosql_policy_invalid") {
		t.Fatalf("expected dosql_policy_invalid, got %v", err)
	}
}
