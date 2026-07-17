package server

import (
	"context"
	"encoding/json"
	"errors"
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
	commit, sessionID := createCredentialPlanWorkspaceFixture(t)
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
		"session_id":        sessionID,
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   commit,
		"inputs": map[string]string{
			"environment": "oilan",
			"service":     "frontend",
		},
	})
	var plan CredentialPlan
	if err := json.Unmarshal([]byte(result), &plan); err != nil {
		t.Fatalf("decode credential plan: %v", err)
	}
	if plan.Cluster != "doops-edu" || plan.Instance != "edu-coder" || len(plan.CredentialRefs) != 1 {
		t.Fatalf("unexpected credential plan: %#v", plan)
	}
}

func TestAgentCredentialPlanToolRejectsLegacyWorkflowPath(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
	}))
	if err == nil || !strings.Contains(err.Error(), "deploy/environments.yaml") {
		t.Fatalf("legacy workflow path error = %v", err)
	}
}

func TestAgentCredentialPlanToolRejectsV2DocumentAtCanonicalPath(t *testing.T) {
	_, sessionID := createCredentialPlanWorkspaceFixture(t)
	workspace, err := workspacePath(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "deploy", "environments.yaml"), []byte(`
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: disguised-legacy-workflow
`), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, workspace, "git", "add", "deploy/environments.yaml")
	runTestCommand(t, workspace, "git", "commit", "-q", "-m", "replace registry with v2")
	commit := strings.TrimSpace(runTestCommand(t, workspace, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalClone := cloneCredentialPlanSource
	cloneCredentialPlanSource = func(string, string, string, string) error { return nil }
	t.Cleanup(func() { cloneCredentialPlanSource = originalClone })

	_, err = handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        sessionID,
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   commit,
		"inputs": map[string]string{
			"environment": "oilan",
			"service":     "frontend",
		},
	}))
	if err == nil || !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("v2 document at canonical path error = %v", err)
	}
}

func TestAgentCredentialPlanToolRejectsWorkspaceRevisionMismatch(t *testing.T) {
	_, sessionID := createCredentialPlanWorkspaceFixture(t)
	originalClone := cloneCredentialPlanSource
	cloneCredentialPlanSource = func(string, string, string, string) error { return nil }
	t.Cleanup(func() { cloneCredentialPlanSource = originalClone })

	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        sessionID,
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   strings.Repeat("b", 40),
		"inputs": map[string]string{
			"environment": "oilan",
			"service":     "frontend",
		},
	}))
	if err == nil || err.Error() != "workflow source revision mismatch" {
		t.Fatalf("workspace revision mismatch error = %v", err)
	}
}

func TestVerifyWorkflowSourceRevisionRejectsMovedBranch(t *testing.T) {
	commit, sessionID := createCredentialPlanWorkspaceFixture(t)
	workspace, err := workspacePath(sessionID)
	if err != nil {
		t.Fatal(err)
	}
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

func createCredentialPlanWorkspaceFixture(t *testing.T) (string, string) {
	t.Helper()
	workspace := writeEnvironmentRegistryV3Fixture(t)
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Dir(workspace))
	t.Setenv("DOOPS_PLAN_WORKSPACE_BASE", filepath.Dir(workspace))
	runTestCommand(t, workspace, "git", "init", "-q", "-b", "main")
	runTestCommand(t, workspace, "git", "config", "user.email", "test@example.invalid")
	runTestCommand(t, workspace, "git", "config", "user.name", "DoOps Test")
	runTestCommand(t, workspace, "git", "add", "deploy")
	runTestCommand(t, workspace, "git", "commit", "-q", "-m", "credential plan fixture")
	commit := strings.TrimSpace(runTestCommand(t, workspace, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(workspace, ".doops-ready"), []byte(commit+"\n"), 0o600); err != nil {
		t.Fatalf("write ready marker: %v", err)
	}
	return commit, filepath.Base(workspace)
}

func TestAgentCredentialPlanToolRejectsUnknownFields(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
		"unexpected":        true,
	}))
	if err == nil || err.Error() != "invalid credential plan params" {
		t.Fatalf("unknown field error = %v, want invalid credential plan params", err)
	}
}

func TestAgentCredentialPlanToolRejectsUnsafeSourceTransport(t *testing.T) {
	_, err := handleCredentialPlanTool(mustJSON(t, map[string]interface{}{
		"session_id":        "credential-plan",
		"workflow_path":     "deploy/environments.yaml",
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
