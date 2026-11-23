package server

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestToolCatalogueMatchesAgentDispatch is the contract between the Gateway-side
// tool registry (tool_registry.go) and the Agent-side dispatch table
// (tool_dispatch.go). A tool declared on only one side is either rejected as
// unknown by the Gateway or unreachable in the Agent; both are drift, so the
// test fails with the exact missing names.
func TestToolCatalogueMatchesAgentDispatch(t *testing.T) {
	gateway := gatewayToolNames()
	agent := make([]string, 0, len(agentToolHandlers))
	for name := range agentToolHandlers {
		agent = append(agent, name)
	}
	sort.Strings(agent)
	if strings.Join(gateway, ",") != strings.Join(agent, ",") {
		t.Fatalf("tool catalogue drifted between Gateway and Agent; only in tool_registry.go: %v; only in tool_dispatch.go: %v", diffToolNames(gateway, agent), diffToolNames(agent, gateway))
	}
	for name, handler := range agentToolHandlers {
		if handler == nil {
			t.Fatalf("tool %s has a nil Agent handler", name)
		}
	}
}

func diffToolNames(left, right []string) []string {
	known := make(map[string]bool, len(right))
	for _, name := range right {
		known[name] = true
	}
	var only []string
	for _, name := range left {
		if !known[name] {
			only = append(only, name)
		}
	}
	return only
}

func TestToolCatalogueHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range gatewayToolSpecs {
		if spec.name == "" {
			t.Fatalf("tool registry contains an entry without a name: %#v", spec)
		}
		if seen[spec.name] {
			t.Fatalf("duplicate tool registry entry: %s", spec.name)
		}
		switch spec.visibility {
		case clientCallable:
			if spec.action == nil || spec.publicDenial != "" {
				t.Fatalf("public tool %s must have an action and no internal denial", spec.name)
			}
		case gatewayInternal:
		default:
			t.Fatalf("tool %s has no valid RPC visibility", spec.name)
		}
		seen[spec.name] = true
	}
}

// This snapshot is consumed by the console test. It records public Agent tool
// names only, not permissions or the Gateway-local COS RPC.
func TestPublicAgentToolContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/public-agent-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	if err := json.Unmarshal(raw, &documented); err != nil {
		t.Fatal(err)
	}
	var public []string
	for _, spec := range gatewayToolSpecs {
		if spec.visibility == clientCallable {
			public = append(public, spec.name)
		}
	}
	sort.Strings(public)
	if !slices.Equal(public, documented) {
		t.Fatalf("public-agent-tools.json is stale: want %v, got %v", public, documented)
	}
}

// Internal actions are still required for audit and resource admission. Their
// presence must not make these Agent tools callable from the client socket.
func TestInternalToolsRetainTheirAction(t *testing.T) {
	for name, want := range map[string]GatewayAction{
		"doops_credential_materialize": ActionCredentialUse,
		"doops_credential_import":      ActionCredentialRotate,
		"doops_credential_cleanup":     ActionCredentialRevoke,
		"doops_cos_sync":               ActionWrite,
		diagnosticReadTool:             "",
	} {
		if spec := gatewayToolIndex[name]; spec.visibility != gatewayInternal || actionForTool(name, nil) != want {
			t.Errorf("%s: want Gateway-internal tool with action %q", name, want)
		}
	}
}

func TestGatewayActionMappingUsesDedicatedToolsForCheckAndClean(t *testing.T) {
	if got := actionForTool("doops_shell", json.RawMessage(`{"_doops_action":"info"}`)); got != ActionExec {
		t.Fatalf("doops_shell must map to exec regardless of client override, got %q", got)
	}
	if got := actionForTool("doops_ssh", nil); got != ActionExec {
		t.Fatalf("doops_ssh must map to exec, got %q", got)
	}
	if got := actionForTool("doops_check_deployment", nil); got != ActionCheck {
		t.Fatalf("doops_check_deployment must map to check, got %q", got)
	}
	if got := actionForTool("doops_clean_workspace", nil); got != ActionClean {
		t.Fatalf("doops_clean_workspace must map to clean, got %q", got)
	}
	if got := actionForTool("doops_workspace_read", nil); got != ActionRead {
		t.Fatalf("doops_workspace_read must map to read, got %q", got)
	}
	if got := actionForTool("doops_k8s", nil); got != ActionExec {
		t.Fatalf("doops_k8s must map to exec, got %q", got)
	}
	if got := actionForTool("doops_database_inventory", nil); got != ActionInfo {
		t.Fatalf("doops_database_inventory must map to info, got %q", got)
	}
	if got := resourceKeyForTool(ActionInfo, "doops_database_inventory", nil, "dev", "node"); got != "" {
		t.Fatalf("database inventory must stay a concurrent read, got %q", got)
	}
}

func TestGatewayActionMappingUsesDedicatedToolForAgentUpgrade(t *testing.T) {
	if got := actionForTool("doops_agent_upgrade", nil); got != ActionAgentUpgrade {
		t.Fatalf("expected agent upgrade action, got %q", got)
	}
}

func TestGatewayActionMappingUsesDedicatedToolsForWorkspacePull(t *testing.T) {
	if got := actionForTool("doops_workspace_pull_begin", nil); got != ActionPull {
		t.Fatalf("expected workspace pull begin to map to pull, got %q", got)
	}
	if got := actionForTool("doops_workspace_pull_chunk", nil); got != ActionPull {
		t.Fatalf("expected workspace pull chunk to map to pull, got %q", got)
	}
}
