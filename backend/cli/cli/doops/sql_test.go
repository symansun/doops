package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSQLMode(t *testing.T) {
	for _, value := range []string{"query", "plan", "apply", "discover", " APPLY "} {
		if _, err := parseSQLMode(value); err != nil {
			t.Fatalf("parseSQLMode(%q): %v", value, err)
		}
	}
	if _, err := parseSQLMode("execute"); err == nil {
		t.Fatal("expected unsupported SQL mode to fail")
	}
}

func TestBuildSQLInstructionKeepsAuthorizationAtGatewayAndDoSQL(t *testing.T) {
	message := buildSQLInstruction(sqlModePlan, "billing-main", "add an external id")
	if strings.Contains(message, "签名审批") {
		t.Fatalf("signed approval no longer exists: %s", message)
	}
	for _, expected := range []string{
		"使用 DoSql skill",
		"数据库提示：billing-main",
		"不要读取或索取密码",
		"用户需求：add an external id",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("instruction missing %q: %s", expected, message)
		}
	}
}

func TestSQLGatewayOperation(t *testing.T) {
	if got := sqlGatewayOperation(sqlModeQuery); got != "ask" {
		t.Fatalf("query operation = %q, want ask", got)
	}
	if got := sqlGatewayOperation(sqlModePlan); got != "ask" {
		t.Fatalf("plan operation = %q, want ask", got)
	}
	if got := sqlGatewayOperation(sqlModeApply); got != "apply" {
		t.Fatalf("apply operation = %q, want apply", got)
	}
	if got := sqlGatewayOperation(sqlModeDiscover); got != "ask" {
		t.Fatalf("discover operation = %q, want ask", got)
	}
}

func TestDiscoverModeAllowsLocalResourceScanWithoutRequest(t *testing.T) {
	if mode, err := parseSQLMode("discover"); err != nil || mode != sqlModeDiscover {
		t.Fatalf("discover mode = %q, err = %v", mode, err)
	}
}

func TestSQLCopyFromRejectsInvalidCombinationsBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"query mode":        {"--target", "t", "--mode", "query", "--copy-from", "a", "--database", "b", "--tables", "x"},
		"with sql":          {"--target", "t", "--mode", "apply", "--copy-from", "a", "--database", "b", "--tables", "x", "--sql", "select 1"},
		"with verify":       {"--target", "t", "--mode", "apply", "--copy-from", "a", "--database", "b", "--tables", "x", "--verify", "select 1"},
		"no destination":    {"--target", "t", "--mode", "apply", "--copy-from", "a", "--tables", "x"},
		"no tables":         {"--target", "t", "--mode", "apply", "--copy-from", "a", "--database", "b"},
		"only blank tables": {"--target", "t", "--mode", "apply", "--copy-from", "a", "--database", "b", "--tables", " , "},
	} {
		t.Run(name, func(t *testing.T) {
			// Every case must fail on argument validation, not on the missing config.
			err := runSQLCommand(args, "s", nil, nil, nil, false)
			if err == nil || strings.Contains(err.Error(), "not found") {
				t.Fatalf("expected a validation error, got %v", err)
			}
		})
	}
}

func TestSQLCredentialRejectsUnsupportedCombinationsBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"with copy-from": {"--target", "t", "--mode", "apply", "--credential", "migrate", "--copy-from", "a", "--database", "b", "--tables", "x"},
		"with request":   {"--target", "t", "--mode", "query", "--credential", "migrate", "--request", "list tables"},
		"plan request":   {"--target", "t", "--mode", "plan", "--credential", "migrate", "--request", "add a column"},
	} {
		t.Run(name, func(t *testing.T) {
			err := runSQLCommand(args, "s", nil, nil, nil, false)
			if err == nil || !strings.Contains(err.Error(), "--credential") {
				t.Fatalf("expected a --credential validation error, got %v", err)
			}
		})
	}
}

func TestStableCredentialCategoryDropsResponseText(t *testing.T) {
	for _, tt := range []struct {
		message string
		want    string
	}{
		{"target_offline", "target_offline"},
		{" credential_import_key_missing\n", "credential_import_key_missing"},
		{"", ""},
		{"404 page not found", ""},
		{`{"message":"synthetic-canary"}`, ""},
		{"Target_Busy", ""},
		{strings.Repeat("a", 129), ""},
	} {
		if got := stableCredentialCategory(tt.message); got != tt.want {
			t.Fatalf("stableCredentialCategory(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestSQLBindingNextGuidesEachBindingFailure(t *testing.T) {
	cases := []struct {
		name    string
		in      error
		wantAll []string
	}{
		{"binding missing", fmt.Errorf("dosql_database_binding_missing"), []string{"next:", "credential configure", "--use database", "--from-secret", "--keys <engine-specific-keys>", "doops guide database"}},
		{"credential missing", fmt.Errorf("dosql_credential_missing: run credential configure"), []string{"--from-secret"}},
		{"not bound", fmt.Errorf("dosql_database_not_bound: x"), []string{"credential list", "--from-secret", "original --database", "do not switch databases"}},
		{"database required", fmt.Errorf("dosql_database_required: target binds a"), []string{"--database <database>"}},
		{"version missing", fmt.Errorf("credential_version_not_found"), []string{"--id <id>"}},
		{"unrelated", fmt.Errorf("dosql_statement_failed"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqlBindingNext(tc.in, "tgt")
			if tc.wantAll == nil {
				if got != tc.in {
					t.Fatalf("unrelated error was rewritten: %v", got)
				}
				return
			}
			for _, want := range tc.wantAll {
				if !strings.Contains(got.Error(), want) {
					t.Errorf("missing %q in %q", want, got.Error())
				}
			}
			if !strings.Contains(got.Error(), tc.in.Error()) {
				t.Errorf("original message lost: %q", got.Error())
			}
			if strings.Contains(got.Error(), "omit --database") {
				t.Errorf("binding recovery must not redirect to another database: %q", got.Error())
			}
		})
	}
	if sqlBindingNext(nil, "tgt") != nil {
		t.Fatal("nil must stay nil")
	}
}
