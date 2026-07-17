package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCredentialPlanToolParsesBoundWorkspace(t *testing.T) {
	_, _ = createCredentialPlanWorkspaceFixture(t)
	originalClone := cloneCredentialPlanSource
	cloneCredentialPlanSource = func(string, string, string) error { return nil }
	t.Cleanup(func() { cloneCredentialPlanSource = originalClone })

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callTool(t, conn, "doops_credential_plan", map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
	})
	var plan CredentialPlan
	if err := json.Unmarshal([]byte(result), &plan); err != nil {
		t.Fatalf("decode credential plan: %v", err)
	}
	if plan.Cluster != "oilan" || plan.Instance != "prod" || len(plan.CredentialRefs) != 1 {
		t.Fatalf("unexpected credential plan: %#v", plan)
	}
}

func createCredentialPlanWorkspaceFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	t.Setenv("DOOPS_PLAN_WORKSPACE_BASE", root)
	workspace := filepath.Join(root, "credential-plan")
	if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "release.yaml"), []byte(`
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: oilan-release
spec:
  application: oilan
  environment: production
  configurationSource: deploy/environments.yaml
  credentialRefs:
    - name: cnb-registry
      use: imagePull
      namespace: oilan
      registryRepository: team/app
      registryReference: latest
      workload:
        kind: Deployment
        name: oilan-api
`), 0600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "environments.yaml"), []byte(`
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: oilan-prod
      cluster: oilan
      instance: prod
`), 0600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}
	runTestCommand(t, workspace, "git", "init", "-q", "-b", "main")
	runTestCommand(t, workspace, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, workspace, "git", "config", "user.name", "DoOps Test")
	runTestCommand(t, workspace, "git", "add", "deploy/release.yaml", "deploy/environments.yaml")
	runTestCommand(t, workspace, "git", "commit", "-q", "-m", "credential plan fixture")
	commit := strings.TrimSpace(runTestCommand(t, workspace, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o600); err != nil {
		t.Fatalf("write ready marker: %v", err)
	}
	return commit, workspace
}

func TestAgentCredentialPlanToolRejectsUnknownFields(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"unexpected":        true,
	}))
	if err == nil || err.Error() != "invalid credential plan params" {
		t.Fatalf("unknown field error = %v, want invalid credential plan params", err)
	}
}

func TestAgentCredentialPlanToolRejectsUnsafeSourceTransport(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "ext::sh -c 'id'",
		"source_branch":     "main",
	}))
	if err == nil || !strings.Contains(err.Error(), "must be an HTTPS URL") {
		t.Fatalf("unsafe source transport error = %v", err)
	}
}

func TestAgentCredentialMaterializeToolAcceptsBoundSession(t *testing.T) {
	bin, _ := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_TEST_SECRET_TYPE", "Opaque")
	t.Setenv("DOOPS_TEST_SECRET_KEYS", "API_KEY")

	result, err := handleCredentialMaterializeTool(context.Background(), mustJSON(t, map[string]any{
		"session_id":      "credential-apply",
		"credential_id":   "cred_opaque",
		"grant_id":        "grant_opaque",
		"version_id":      "ver_opaque",
		"credential_type": CredentialTypeOpaque,
		"use":             CredentialUseOpaqueSecret,
		"namespace":       "app",
		"required_keys":   []string{"API_KEY"},
		"payload":         map[string]any{"data": map[string]string{"API_KEY": "session-canary-secret"}},
	}))
	if err != nil {
		t.Fatalf("materialize tool with session binding: %v", err)
	}
	if strings.Contains(result, "session-canary-secret") {
		t.Fatalf("materialize result exposed credential payload: %s", result)
	}
}
