package server

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDoopsPlanMCPListsOnlyReservedPlanTools(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n")
	var output bytes.Buffer

	if err := RunDoopsPlanMCP(input, &output); err != nil {
		t.Fatalf("run MCP server: %v", err)
	}

	var response struct {
		Result struct {
			Tools []struct {
				Name        string                 `json:"name"`
				InputSchema map[string]interface{} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	if len(response.Result.Tools) != len(doopsPlanToolNames) {
		t.Fatalf("tool count = %d, want %d", len(response.Result.Tools), len(doopsPlanToolNames))
	}
	for _, tool := range response.Result.Tools {
		if !containsDoopsPlanTool(tool.Name) {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		if additional, ok := tool.InputSchema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("tool %q must reject arguments: %#v", tool.Name, tool.InputSchema)
		}
	}
}

func TestBuildDoopsPlanAttestedResultBindsTrustedContext(t *testing.T) {
	context := trustedReconciliationContext{
		SchemaVersion:      trustedReconciliationContextSchema,
		OperationID:        "op_" + strings.Repeat("0", 32),
		ExecutionMode:      "dry-run",
		MutationAuthorized: false,
		PlanDigest:         "sha256:" + strings.Repeat("1", 64),
		PlanBindingDigest:  "sha256:" + strings.Repeat("2", 64),
		Source: trustedReconciliationSource{
			Repository:      "https://example.test/app.git",
			Revision:        strings.Repeat("b", 40),
			Branch:          "main",
			WorkspaceCommit: strings.Repeat("a", 40),
		},
		Capabilities: map[string]trustedReconciliationCapability{
			"ObserveWorkspaceSource": {
				Tool:            "ObserveWorkspaceSource",
				EvidenceKind:    "source-identity",
				EvidenceSubject: "source-identity",
				CanonicalScope: map[string]interface{}{
					"cluster": "cluster", "instance": "instance", "project": "app",
					"environment": "test", "template": "release",
					"tool": "mcp_doops_plan_ObserveWorkspaceSource",
				},
			},
		},
	}
	capability := context.Capabilities["ObserveWorkspaceSource"]
	scopeDigest, err := digestJSON(capability.CanonicalScope)
	if err != nil {
		t.Fatalf("digest scope: %v", err)
	}
	capability.ScopeDigest = scopeDigest
	context.Capabilities["ObserveWorkspaceSource"] = capability
	context.ContextDigest, err = context.calculatedDigest()
	if err != nil {
		t.Fatalf("digest context: %v", err)
	}
	callContext := doopsPlanCallContext{
		SchemaVersion: doopsPlanCallContextSchema,
		Context:       context,
		CapabilityKey: "ObserveWorkspaceSource",
		Capability:    capability,
	}

	result, err := buildDoopsPlanAttestedResult(
		"ObserveWorkspaceSource",
		callContext,
		map[string]interface{}{},
		map[string]interface{}{"workspaceCommit": strings.Repeat("a", 40)},
		nil,
	)
	if err != nil {
		t.Fatalf("build attested result: %v", err)
	}
	meta := result["_meta"].(map[string]interface{})
	attestation := meta["doops.sh/tool-attestation"].(map[string]interface{})
	if attestation["contextDigest"] != context.ContextDigest ||
		attestation["scopeDigest"] != capability.ScopeDigest ||
		attestation["tool"] != "ObserveWorkspaceSource" {
		t.Fatalf("attestation binding mismatch: %#v", attestation)
	}
	withoutMeta := make(map[string]interface{}, len(result)-1)
	for key, value := range result {
		if key != "_meta" {
			withoutMeta[key] = value
		}
	}
	digest, err := digestJSON(withoutMeta)
	if err != nil {
		t.Fatalf("digest result: %v", err)
	}
	if attestation["resultDigest"] != digest {
		t.Fatalf("result digest = %v, want %s", attestation["resultDigest"], digest)
	}
}

func TestDoopsPlanWorkloadServicesDoesNotMutateRuntimeServices(t *testing.T) {
	runtime := doopsPlanRuntime{
		Services: []string{"backend", "frontend", "gateway"},
		WorkloadChecks: []credentialConfigurationWorkloadCheck{
			{Service: "frontend"},
			{Service: "gateway"},
		},
	}
	original := append([]string(nil), runtime.Services...)

	got := doopsPlanWorkloadServices(runtime)

	if !reflect.DeepEqual(got, []string{"frontend", "gateway"}) {
		t.Fatalf("services = %#v", got)
	}
	if !reflect.DeepEqual(runtime.Services, original) {
		t.Fatalf("runtime services mutated: %#v", runtime.Services)
	}
}

func TestValidateDoopsPlanWorkspaceRejectsTrackedFileTampering(t *testing.T) {
	base := t.TempDir()
	bareBase := t.TempDir()
	workspace := filepath.Join(base, "session-a")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	runTestCommand(t, workspace, "git", "init", "-q")
	runTestCommand(t, workspace, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, workspace, "git", "config", "user.name", "DoOps Test")
	if err := os.WriteFile(filepath.Join(workspace, "deploy.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	runTestCommand(t, workspace, "git", "add", "deploy.yaml")
	runTestCommand(t, workspace, "git", "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runTestCommand(t, workspace, "git", "rev-parse", "HEAD"))
	runTestCommand(t, base, "git", "clone", "-q", "--bare", workspace, filepath.Join(bareBase, "session-a.git"))
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatalf("write ready marker: %v", err)
	}
	t.Setenv("DOOPS_PLAN_WORKSPACE_BASE", base)
	t.Setenv("DOOPS_PLAN_BARE_REPOSITORY_BASE", bareBase)

	if _, err := validateDoopsPlanWorkspace(workspace, commit); err != nil {
		t.Fatalf("validate clean workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".doops-audit-log"), []byte("agent-owned\n"), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
	if _, err := validateDoopsPlanWorkspace(workspace, commit); err != nil {
		t.Fatalf("validate workspace with agent audit log: %v", err)
	}
	agentRuntime := filepath.Join(workspace, ".agent", "sessions", "session-a")
	if err := os.MkdirAll(agentRuntime, 0o700); err != nil {
		t.Fatalf("create agent runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentRuntime, "messages.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write agent runtime file: %v", err)
	}
	if _, err := validateDoopsPlanWorkspace(workspace, commit); err != nil {
		t.Fatalf("validate workspace with agent runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "untracked.yaml"), []byte("unsafe: true\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	if _, err := validateDoopsPlanWorkspace(workspace, commit); err == nil ||
		!strings.Contains(err.Error(), "outside the pushed source snapshot") {
		t.Fatalf("untracked workspace error = %v", err)
	}
	if err := os.Remove(filepath.Join(workspace, "untracked.yaml")); err != nil {
		t.Fatalf("remove untracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy.yaml"), []byte("version: 2\n"), 0o644); err != nil {
		t.Fatalf("tamper tracked file: %v", err)
	}
	if _, err := validateDoopsPlanWorkspace(workspace, commit); err == nil ||
		!strings.Contains(err.Error(), "differs from the pushed source snapshot") {
		t.Fatalf("tampered workspace error = %v", err)
	}
}

func TestResolveDoopsPlanWorkspacePathRejectsAgentRuntime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".agent", "sessions", "unsafe", "values.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create agent runtime path: %v", err)
	}
	if err := os.WriteFile(path, []byte("unsafe: true\n"), 0o600); err != nil {
		t.Fatalf("write agent runtime path: %v", err)
	}

	if _, err := resolveDoopsPlanWorkspacePath(root, ".agent/sessions/unsafe/values.yaml"); err == nil ||
		!strings.Contains(err.Error(), "agent runtime directory") {
		t.Fatalf("agent runtime deployment path error = %v", err)
	}
}

func runTestCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v: %s", name, args, err, output)
	}
	return string(output)
}
