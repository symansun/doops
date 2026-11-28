package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDoagentModeForPromptUsesOnlyNativeAskMode(t *testing.T) {
	tests := []struct {
		name          string
		operation     string
		executionMode string
		want          string
		wantErr       bool
	}{
		{name: "general", want: "auto"},
		{name: "ask", operation: "ask", want: "auto"},
		{name: "apply", operation: "apply", want: "build"},
		{name: "mode is not accepted", executionMode: "apply", wantErr: true},
		{name: "unknown operation", operation: "custom", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := doagentModeForPrompt(test.operation, test.executionMode)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected mode mapping error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("map native mode: %v", err)
			}
			if got != test.want {
				t.Fatalf("native mode mismatch: got %q want %q", got, test.want)
			}
		})
	}
}

func TestDoagentAllowedToolsUsesPermissionEngineForDiagnostics(t *testing.T) {
	ordinary := doagentAllowedTools(false, "", true)
	wantOrdinary := []string{"Read", "glob", "grep", "Skill", "Bash", "mcp_doops_k8s_read", "mcp_doops_web_read", "mcp_doops_kb_read"}
	if !reflect.DeepEqual(ordinary, wantOrdinary) {
		t.Fatalf("ordinary prompt tool directory = %#v, want %#v", ordinary, wantOrdinary)
	}

	apply := doagentAllowedTools(true, "", true)
	wantApply := []string{"Read", "glob", "grep", "Skill", "Bash", "mcp_doops_k8s_read", "mcp_doops_web_read", "mcp_doops_kb_read", "mcp_doops_helm"}
	if !reflect.DeepEqual(apply, wantApply) {
		t.Fatalf("apply prompt tool directory = %#v, want %#v", apply, wantApply)
	}
	for _, skill := range []string{"k8s", "image-build"} {
		tools := doagentAllowedTools(true, skill, true)
		if !sliceContainsString(tools, "mcp_doops_helm") || !sliceContainsString(tools, "Bash") ||
			sliceContainsString(tools, "mcp_doops_web") {
			t.Fatalf("Helm skill %q cannot execute its authorized task: %#v", skill, tools)
		}
	}
	website := doagentAllowedTools(true, "doweb", true)
	if !sliceContainsString(website, "mcp_doops_web") || !sliceContainsString(website, "Bash") || sliceContainsString(website, "mcp_doops_helm") {
		t.Fatalf("website prompt did not retain its domain boundary: %#v", website)
	}
	databaseRead := doagentAllowedTools(false, "dosql", true)
	if !sliceContainsString(databaseRead, "mcp_dosql_read") || !sliceContainsString(databaseRead, "Bash") {
		t.Fatalf("database read prompt boundary is unsafe: %#v", databaseRead)
	}
	databaseApply := doagentAllowedTools(true, "dosql", true)
	if !sliceContainsString(databaseApply, "mcp_dosql_read") || !sliceContainsString(databaseApply, "mcp_dosql_exec") ||
		!sliceContainsString(databaseApply, "Bash") ||
		sliceContainsString(databaseApply, "mcp_doops_helm") || sliceContainsString(databaseApply, "mcp_doops_web") {
		t.Fatalf("database apply prompt boundary is unsafe: %#v", databaseApply)
	}
	// External plugins (e.g. dodns, doemail, third-party plugins) are NOT hardcoded in Core:
	// their tools come strictly from custom synced MCP servers matching their skill slug,
	// with zero leakage into Core mutation MCPs or sibling plugins.
	origSettings := doagentSettingsFile
	doagentSettingsFile = filepath.Join(t.TempDir(), "settings.json")
	t.Cleanup(func() { doagentSettingsFile = origSettings })
	if err := writeCustomMCPServers([]agentMCPServerEntry{
		{Name: "dodns", Transport: "stdio", Command: "python3", ProbedTools: []string{"query", "record_add"}},
		{Name: "doemail", Transport: "stdio", Command: "node", ProbedTools: []string{"dns_plan", "account_create_plan"}},
	}); err != nil {
		t.Fatalf("write custom mcp servers: %v", err)
	}

	extDNSRead := doagentAllowedTools(false, "dodns", true)
	if !sliceContainsString(extDNSRead, "mcp_dodns_query") || !sliceContainsString(extDNSRead, "mcp_doemail_dns_plan") {
		t.Fatalf("ordinary read session should see synced custom MCP tools: %#v", extDNSRead)
	}
	extDNSApply := doagentAllowedTools(true, "dodns", true)
	if !sliceContainsString(extDNSApply, "mcp_dodns_record_add") || !sliceContainsString(extDNSApply, "Bash") ||
		sliceContainsString(extDNSApply, "mcp_doops_helm") || sliceContainsString(extDNSApply, "mcp_doops_web") ||
		sliceContainsString(extDNSApply, "mcp_dosql_exec") || sliceContainsString(extDNSApply, "mcp_doemail_account_create_plan") {
		t.Fatalf("external plugin dodns apply boundary leaked outside own MCP + Bash: %#v", extDNSApply)
	}

	for _, skill := range []string{"unknown", " doweb ", " dodns ", " doemail ", "dosecurity"} {
		tools := doagentAllowedTools(true, skill, true)
		for _, tool := range []string{"mcp_doops_web", "mcp_doops_helm", "mcp_dosql_exec", "mcp_dodns_record_add", "mcp_doemail_account_create_plan"} {
			if sliceContainsString(tools, tool) {
				t.Fatalf("undeclared or untrimmed skill %q gained structured mutation tool %q", skill, tool)
			}
		}
	}
}

func TestSkillScopedBashApplyEvidenceRequiresCompletedBashAndPreservesFailure(t *testing.T) {
	for _, skill := range []string{"dosecurity", "dodns", "doemail", "custom-ops-skill"} {
		if skillRequiresStructuredMutationMCP(skill) {
			t.Fatalf("external or non-Core-MCP skill %q should allow audited Bash or plugin MCP apply evidence", skill)
		}
	}
	for _, skill := range []string{"", "k8s", "image-build", "doweb", "dosql"} {
		if !skillRequiresStructuredMutationMCP(skill) {
			t.Fatalf("Core MCP capability skill %q must require structured MCP attestation", skill)
		}
	}

	// Missing Bash/custom MCP call fails closed.
	empty := newDoagentToolTraceCollector(false)
	empty.allowBashApplyEvidence = true
	if err := empty.requireCompletedToolEvidence(); err == nil || !strings.HasPrefix(err.Error(), "doagent_apply_tool_evidence_missing") {
		t.Fatalf("empty bash apply error = %v, want evidence_missing", err)
	}

	// Completed Bash exit=0 passes and emits Bash receipt.
	zero := int32(0)
	okCollector := newDoagentToolTraceCollector(false)
	okCollector.allowBashApplyEvidence = true
	okCollector.calls["b1"] = doagentToolTrace{
		ToolCallID: "b1", ToolName: "Bash", Status: "completed", ExitCode: &zero,
	}
	if err := okCollector.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("completed Bash apply unexpectedly failed: %v", err)
	}
	_, report, err := okCollector.verifiedMutationResult()
	if err != nil || report["outcome"] != "succeeded" || report["verified"] != true {
		t.Fatalf("unexpected Bash verifiedMutationResult: report=%#v err=%v", report, err)
	}

	// Completed external plugin MCP call passes as verified evidence; isError=true fails truthfully.
	okExtMCP := newDoagentToolTraceCollector(false)
	okExtMCP.allowBashApplyEvidence = true
	okExtMCP.calls["m1"] = doagentToolTrace{
		ToolCallID: "m1", ToolName: "mcp_dodns_record_add", Status: "completed",
		resultJSON: json.RawMessage(`{"ok":true}`),
	}
	if err := okExtMCP.requireCompletedToolEvidence(); err != nil {
		t.Fatalf("completed external plugin MCP apply unexpectedly failed: %v", err)
	}
	failExtMCP := newDoagentToolTraceCollector(false)
	failExtMCP.allowBashApplyEvidence = true
	failExtMCP.calls["m2"] = doagentToolTrace{
		ToolCallID: "m2", ToolName: "mcp_dodns_record_add", Status: "completed",
		resultJSON: json.RawMessage(`{"isError":true,"message":"zone_check_failed"}`),
	}
	if err := failExtMCP.requireCompletedToolEvidence(); err == nil || !strings.Contains(err.Error(), "doagent_apply_tool_failed: mcp_dodns_record_add") {
		t.Fatalf("failed external plugin MCP apply error = %v, want doagent_apply_tool_failed", err)
	}

	// Failed Bash fails truthfully.
	one := int32(1)
	failCollector := newDoagentToolTraceCollector(false)
	failCollector.allowBashApplyEvidence = true
	failCollector.calls["b2"] = doagentToolTrace{
		ToolCallID: "b2", ToolName: "Bash", Status: "failed", ExitCode: &one, ErrorCode: "command_failed",
	}
	if err := failCollector.requireCompletedToolEvidence(); err == nil || !strings.Contains(err.Error(), "doagent_apply_tool_failed: Bash") {
		t.Fatalf("failed Bash apply error = %v, want doagent_apply_tool_failed", err)
	}
}

func sliceContainsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestDoagentPermissionRequestFailsClosedWithoutReply(t *testing.T) {
	rpcCalled := make(chan struct{}, 1)
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"permission.updated","params":{"sessionId":"permission-session","permission":{"id":"permission-1","title":"run mutating tool","toolName":"shell"}}}`)
			fmt.Fprintln(w)
		case "/rpc":
			rpcCalled <- struct{}{}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()

	err := subscribeDoagentSSEWithCollector(
		context.Background(),
		doagent.URL,
		"permission-session",
		func(notificationEvent) {},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "permission required") {
		t.Fatalf("unexpected permission result: %v", err)
	}
	select {
	case <-rpcCalled:
		t.Fatal("DoOps bridge must never reply to doagent permission requests")
	case <-time.After(100 * time.Millisecond):
	}
}
