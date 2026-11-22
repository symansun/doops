package server

import (
	"os"
	"path/filepath"
	"regexp"
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
		if spec.action == nil {
			t.Fatalf("tool %s has no action mapping", spec.name)
		}
		seen[spec.name] = true
	}
}

var webToolLiteral = regexp.MustCompile(`"(doops_[a-z_]+)"`)

// TestWebClientToolCatalogueSubsetOfGatewayTools keeps the Next.js console from
// calling a tool the Gateway would reject. The Web console lives outside this Go
// module, so a missing file only skips the check.
func TestWebClientToolCatalogueSubsetOfGatewayTools(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "lib", "gateway.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("web console not present at %s", path)
		}
		t.Fatalf("read %s: %v", path, err)
	}
	known := map[string]bool{}
	for _, name := range gatewayToolNames() {
		known[name] = true
	}
	var missing []string
	for _, match := range webToolLiteral.FindAllStringSubmatch(string(raw), -1) {
		if !known[match[1]] {
			missing = append(missing, match[1])
		}
	}
	if len(missing) > 0 {
		t.Fatalf("web console calls tools the Gateway does not authorise: %v", missing)
	}
}

// TestInternalToolsAreNotCallerFacing pins the boundary for tools the Gateway
// relays on its own behalf: they must not be authorised for callers, and must not
// shadow a caller-facing dispatch entry.
func TestInternalToolsAreNotCallerFacing(t *testing.T) {
	for name := range internalAgentToolHandlers {
		if action := actionForTool(name, nil); action != "" {
			t.Fatalf("internal relay tool %s is authorised for callers as %q", name, action)
		}
		if _, ok := agentToolHandlers[name]; ok {
			t.Fatalf("internal relay tool %s is also in the caller-facing dispatch map", name)
		}
	}
}
