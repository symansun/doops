package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runTestCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
	return string(output)
}

func TestAgentCredentialPlanToolParsesBoundWorkspace(t *testing.T) {
	_, _ = createCredentialPlanWorkspaceFixture(t)
	originalClone := cloneCredentialPlanSource
	cloneCredentialPlanSource = func(string, string, string, string) error { return nil }
	t.Cleanup(func() { cloneCredentialPlanSource = originalClone })

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callTool(t, conn, "doops_credential_plan", map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
		"inputs": map[string]string{
			"environment": "production",
			"service":     "api",
		},
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
	if err := os.MkdirAll(filepath.Join(workspace, "deploy", "environments", "production", "services"), 0755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "deploy", "services"), 0755); err != nil {
		t.Fatalf("create service directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "environments.yaml"), []byte(`
apiVersion: doops.sh/v3
kind: EnvironmentRegistry
metadata:
  name: oilan
environments:
  production:
    target:
      name: oilan-prod
      cluster: oilan
      instance: prod
      session: credential-plan
    source:
      repository: https://example.test/app.git
      branch: main
      build: deploy/deploy.yaml
    helm:
      release: oilan-api
      namespace: oilan
      chart: deploy/chart
      values: deploy/values.yaml
      atomic: true
      wait: true
    artifacts:
      targetRegistry: registry.example.test/oilan
      runtimeImagePullSecret:
        kind: KubernetesSecret
        name: cnb-registry
    services:
      api: deploy/environments/production/services/api.yaml
    healthChecks:
      - https://api.example.test/healthz
`), 0600); err != nil {
		t.Fatalf("write environment registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "environments", "production", "services", "api.yaml"), []byte(`
apiVersion: doops.sh/v3
kind: EnvironmentService
metadata:
  name: api
  environment: production
spec:
  serviceDefinition: deploy/services/api.yaml
  artifact:
    source: source.example.test/oilan-api
    target: registry.example.test/oilan/team/app
    identity: sha256-digest
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  helm:
    chart: deploy/chart
    values: deploy/values.yaml
    value: api
    enabled: true
    replicas: 1
    workload: deployment/oilan-api
    container: oilan-api
  configuration:
    valuesPath: api
    files: []
  verification:
    minReadyReplicas: 1
    requireEndpoints: true
    publicUrl: https://api.example.test/healthz
`), 0600); err != nil {
		t.Fatalf("write environment service: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "services", "api.yaml"), []byte(`
apiVersion: doops.sh/v3
kind: ServiceDefinition
metadata:
  name: api
spec:
  service: api
  artifact: oilan-api
  buildManifest: deploy/deploy.yaml
  sourceContext: api
  dockerfile: api/Dockerfile
  helm:
    value: api
    workload: deployment/oilan-api
    container: oilan-api
  verification:
    endpointRequired: true
`), 0600); err != nil {
		t.Fatalf("write service definition: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "deploy.yaml"), []byte(`
apiVersion: doops.sh/v3
kind: BuildDefinition
metadata:
  name: oilan
spec:
  services:
    api:
      context: api
      dockerfile: api/Dockerfile
      image: source.example.test/oilan-api
`), 0600); err != nil {
		t.Fatalf("write build definition: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "values.yaml"), []byte("api: {}\n"), 0600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	runTestCommand(t, workspace, "git", "init", "-q", "-b", "main")
	runTestCommand(t, workspace, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, workspace, "git", "config", "user.name", "DoOps Test")
	runTestCommand(t, workspace, "git", "add", "deploy")
	runTestCommand(t, workspace, "git", "commit", "-q", "-m", "credential plan fixture")
	commit := strings.TrimSpace(runTestCommand(t, workspace, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o600); err != nil {
		t.Fatalf("write ready marker: %v", err)
	}
	return commit, workspace
}

func TestVerifyWorkflowSourceRevisionRejectsMovedBranch(t *testing.T) {
	commit, workspace := createCredentialPlanWorkspaceFixture(t)
	if err := verifyWorkflowSourceRevision(
		context.Background(),
		workspace,
		commit,
		os.Environ(),
	); err != nil {
		t.Fatalf("verify exact workflow revision: %v", err)
	}
	if err := verifyWorkflowSourceRevision(
		context.Background(),
		workspace,
		strings.Repeat("b", 40),
		os.Environ(),
	); err == nil || err.Error() != "workflow source revision changed during checkout" {
		t.Fatalf("moved workflow revision error = %v", err)
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

func TestAgentCredentialPlanToolRejectsUnsafeSourceTransport(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "ext::sh -c 'id'",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
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
