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
	message := buildSQLInstruction(sqlModeApply, "billing-main", "add an external id")
	for _, expected := range []string{
		"使用 DoSql skill",
		"数据库提示：billing-main",
		"签名审批",
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
