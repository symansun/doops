package server

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sosedoff/gitkit"
)

func TestAgentCredentialPlanToolParsesBoundWorkspace(t *testing.T) {
	commit, repository, _ := createCredentialPlanWorkspaceFixture(t)

	gw := NewGateway("0")
	ts := httptest.NewServer(http.HandlerFunc(gw.HandleWebSocket))
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callTool(t, conn, "doops_credential_plan", map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": repository,
		"source_branch":     "main",
		"source_revision":   commit,
	})
	var plan CredentialPlan
	if err := json.Unmarshal([]byte(result), &plan); err != nil {
		t.Fatalf("decode credential plan: %v", err)
	}
	if plan.Cluster != "oilan" || plan.Instance != "prod" || len(plan.CredentialRefs) != 1 {
		t.Fatalf("unexpected credential plan: %#v", plan)
	}
}

func TestAgentCredentialPlanToolRejectsSourceBranchMovingAfterPrepare(t *testing.T) {
	commit, repository, sourceRoot := createCredentialPlanWorkspaceFixture(t)
	if err := os.WriteFile(filepath.Join(sourceRoot, "deploy", "release.yaml"), []byte(`
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: tampered-release
spec:
  application: tampered
  environment: production
	configurationSource: deploy/environments.yaml
`), 0600); err != nil {
		t.Fatalf("advance workflow source: %v", err)
	}
	runTestCommand(t, sourceRoot, "git", "add", "deploy/release.yaml")
	runTestCommand(t, sourceRoot, "git", "commit", "-q", "-m", "advance workflow source")
	runTestCommand(t, sourceRoot, "git", "push", "-q", repository, "main")

	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": repository,
		"source_branch":     "main",
		"source_revision":   commit,
	}))
	if err == nil || !strings.Contains(err.Error(), "source revision changed") {
		t.Fatalf("moved source branch error = %v", err)
	}
}

func createCredentialPlanWorkspaceFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	t.Setenv("DOOPS_PLAN_WORKSPACE_BASE", root)
	sourceRoot := t.TempDir()
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
	runTestCommand(t, sourceRoot, "git", "add", "deploy/release.yaml", "deploy/environments.yaml")
	runTestCommand(t, sourceRoot, "git", "commit", "-q", "-m", "credential plan fixture")
	commit := strings.TrimSpace(runTestCommand(t, sourceRoot, "git", "rev-parse", "HEAD"))
	repositoryRoot := t.TempDir()
	repositoryPath := filepath.Join(repositoryRoot, "credential-plan.git")
	runTestCommand(t, root, "git", "clone", "-q", "--bare", sourceRoot, repositoryPath)
	service := gitkit.New(gitkit.Config{Dir: repositoryRoot, AutoCreate: true})
	if err := service.Setup(); err != nil {
		t.Fatalf("setup Git HTTPS test service: %v", err)
	}
	server := httptest.NewTLSServer(service)
	t.Cleanup(server.Close)
	certificate := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: server.Certificate().Raw,
	})
	caPath := filepath.Join(t.TempDir(), "git-test-ca.pem")
	if err := os.WriteFile(caPath, certificate, 0o600); err != nil {
		t.Fatalf("write Git HTTPS test CA: %v", err)
	}
	t.Setenv("GIT_SSL_CAINFO", caPath)
	repository := server.URL + "/" + filepath.Base(repositoryPath)
	return commit, repository, sourceRoot
}

func TestAgentCredentialPlanToolRejectsWorkspaceCommitMismatch(t *testing.T) {
	_, repository, _ := createCredentialPlanWorkspaceFixture(t)

	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": repository,
		"source_branch":     "main",
		"source_revision":   strings.Repeat("b", 40),
	}))
	if err == nil || !strings.Contains(err.Error(), "source revision changed") {
		t.Fatalf("source revision mismatch error = %v", err)
	}
}

func TestAgentCredentialPlanToolRejectsUnknownFields(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   strings.Repeat("a", 40),
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
		"source_revision":   strings.Repeat("a", 40),
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
