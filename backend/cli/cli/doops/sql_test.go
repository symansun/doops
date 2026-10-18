package main

import (
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
