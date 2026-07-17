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
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sourceRoot := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "deploy"), 0755); err != nil {
		t.Fatalf("create source repository: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "deploy", "release.yaml"), []byte(`
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
	if err := os.WriteFile(filepath.Join(sourceRoot, "deploy", "environments.yaml"), []byte(`
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
	runTestCommand(t, sourceRoot, "git", "init", "-q", "-b", "main")
	runTestCommand(t, sourceRoot, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, sourceRoot, "git", "config", "user.name", "DoOps Test")
	runTestCommand(t, sourceRoot, "git", "add", ".")
	runTestCommand(t, sourceRoot, "git", "commit", "-q", "-m", "fixture")

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callTool(t, conn, "doops_credential_plan", map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": sourceRoot,
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

func TestAgentCredentialPlanToolRequiresDeclaredSource(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":    "credential-plan",
		"workflow_path": "deploy/release.yaml",
	}))
	if err == nil || !strings.Contains(err.Error(), "source_repository") {
		t.Fatalf("missing source error = %v", err)
	}
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
