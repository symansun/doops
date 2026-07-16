package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeWorkflowCaller struct {
	toolName  string
	arguments map[string]interface{}
	output    string
	calls     int
	onCall    func()
}

func TestValidateWorkflowInputsRejectsCredentialMaterial(t *testing.T) {
	for _, key := range []string{
		"token", "client_secret", "registry-password", "private.key", "accessToken",
		"dbPassword", "secretKey", "authorization", "cookie", "githubToken",
		"registryCredential", "githubPAT", "passphrase", "dockerConfigJson", "sshIdentity",
	} {
		t.Run(key, func(t *testing.T) {
			if err := validateWorkflowInputs(map[string]string{key: "synthetic"}); err == nil {
				t.Fatalf("validateWorkflowInputs(%q) succeeded, want rejection", key)
			}
		})
	}
	if err := validateWorkflowInputs(map[string]string{"releaseId": strings.Repeat("a", 40)}); err != nil {
		t.Fatalf("non-sensitive workflow input rejected: %v", err)
	}
	for _, value := range []string{
		"Bearer synthetic-canary",
		"https://user:synthetic@example.test/repo.git",
		"https://example.test/repo.git?access_token=synthetic",
		"https://example.test/repo.git?X-Goog-Signature=synthetic",
		"scheduled release Bearer synthetic-canary",
		"sk-synthetic",
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("a", 82),
		"glpat-" + strings.Repeat("a", 20),
		"AKIA" + strings.Repeat("A", 16),
		"-----BEGIN PRIVATE KEY-----\nsynthetic\n-----END PRIVATE KEY-----",
	} {
		if err := validateWorkflowInputs(map[string]string{"releaseId": value}); err == nil {
			t.Fatalf("secret-bearing workflow input value %q was accepted", value)
		}
	}
}

func (f *fakeWorkflowCaller) CallStructured(toolName string, arguments map[string]interface{}, destination interface{}) error {
	f.calls++
	f.toolName = toolName
	f.arguments = arguments
	if f.onCall != nil {
		f.onCall()
	}
	return json.Unmarshal([]byte(f.output), destination)
}

func TestRunCICDCommandRoutesApplyThroughPushAndAsk(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create repository marker: %v", err)
	}
	workflow := filepath.Join(root, "backend", "deploy", "workflows", "oilan-agent-bootstrap.yaml")
	if err := os.MkdirAll(filepath.Dir(workflow), 0o755); err != nil {
		t.Fatalf("create workflow directory: %v", err)
	}
	if err := os.WriteFile(workflow, []byte("apiVersion: doops.sh/v2\nkind: DeploymentTemplate\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	caller := &fakeWorkflowCaller{output: workflowResultJSON("apply", 1)}
	var pushedRoot string
	var events []string
	var prepareBody struct {
		Cluster         string `json:"cluster"`
		Instance        string `json:"instance"`
		SessionID       string `json:"session_id"`
		WorkflowPath    string `json:"workflow_path"`
		WorkspaceCommit string `json:"workspace_commit"`
		Mode            string `json:"mode"`
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, "prepare")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/credentials/prepare" {
			t.Fatalf("unexpected credential prepare request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer workflow-token" {
			t.Fatalf("credential prepare authorization mismatch: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&prepareBody); err != nil {
			t.Fatalf("decode credential prepare body: %v", err)
		}
		_, _ = fmt.Fprint(w, `{
			"apiVersion":"doops.sh/v2",
			"kind":"CredentialRun",
			"id":"credrun_123",
			"mode":"apply",
			"cluster":"doops-edu",
			"instance":"edu-coder",
			"materializations":[{
				"credentialId":"cred_registry",
				"versionId":"credver_1",
				"resourceName":"registry-pull",
				"namespace":"apps",
				"secretType":"kubernetes.io/dockerconfigjson",
				"keys":[".dockerconfigjson"],
				"resourceVersion":"42",
				"digest":"sha256:manifest",
				"fingerprint":"sha256:fingerprint",
				"expiresAt":"2026-08-01T00:00:00Z",
				"status":"verified"
			}]
		}`)
	}))
	defer gateway.Close()
	originalPush := pushWorkflowWorkspace
	originalOpen := openWorkflowAgent
	pushWorkflowWorkspace = func(_ Server, src, _ string, dryRun bool, _ []string, sessionID string) (string, error) {
		events = append(events, "push")
		pushedRoot = src
		if dryRun {
			t.Fatal("workspace push must occur before the agent apply")
		}
		if sessionID != "release-test" {
			t.Fatalf("unexpected session %q", sessionID)
		}
		return strings.Repeat("a", 40), nil
	}
	openWorkflowAgent = func(_ Server, _ *SessionStore, _ string, _ bool) (workflowAgentCaller, func()) {
		caller.onCall = func() { events = append(events, "prompt") }
		return caller, func() {}
	}
	t.Cleanup(func() {
		pushWorkflowWorkspace = originalPush
		openWorkflowAgent = originalOpen
	})

	err := runCICDCommand(
		context.Background(),
		[]string{
			"run",
			"-f", workflow,
			"-target", "agent-runtime",
			"--set", "releaseId=" + strings.Repeat("b", 40),
		},
		[]Server{{
			Name:     "agent-runtime",
			Gateway:  gateway.URL,
			Cluster:  "doops-edu",
			Instance: "edu-coder",
			Token:    "workflow-token",
		}},
		nil,
		NewSessionStore(),
		"release-test",
		false,
	)
	if err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if pushedRoot != root {
		t.Fatalf("workflow repository root mismatch: got %q want %q", pushedRoot, root)
	}
	if got, want := strings.Join(events, ","), "push,prepare,prompt"; got != want {
		t.Fatalf("workflow operation order mismatch: got %s want %s", got, want)
	}
	if prepareBody.Cluster != "doops-edu" ||
		prepareBody.Instance != "edu-coder" ||
		prepareBody.SessionID != "release-test" ||
		prepareBody.WorkflowPath != "backend/deploy/workflows/oilan-agent-bootstrap.yaml" ||
		prepareBody.WorkspaceCommit != strings.Repeat("a", 40) ||
		prepareBody.Mode != "apply" {
		t.Fatalf("credential prepare body mismatch: %#v", prepareBody)
	}
	if caller.toolName != "doops_agent_prompt" {
		t.Fatalf("expected generic Ask tool, got %q", caller.toolName)
	}
	if caller.arguments["workspace_commit"] != strings.Repeat("a", 40) {
		t.Fatalf("workspace commit not bound to Ask: %#v", caller.arguments)
	}
	if caller.arguments["response_format"] != "json" {
		t.Fatalf("Ask must request structured result: %#v", caller.arguments)
	}
	if caller.arguments["operation"] != "apply" {
		t.Fatalf("apply must use the explicit native operation: %#v", caller.arguments)
	}
	var instruction map[string]interface{}
	if err := json.Unmarshal([]byte(caller.arguments["instruction"].(string)), &instruction); err != nil {
		t.Fatalf("decode instruction: %v", err)
	}
	if instruction["skill"] != "$doops-cicd" ||
		instruction["executionMode"] != "apply" ||
		instruction["workflowPath"] != "backend/deploy/workflows/oilan-agent-bootstrap.yaml" {
		t.Fatalf("unexpected Skill instruction: %#v", instruction)
	}
	runID, _ := instruction["runId"].(string)
	if !strings.HasPrefix(runID, "release-test-") || runID == "release-test" {
		t.Fatalf("workflow run id must identify one invocation: %#v", instruction)
	}
	credentialRun, ok := instruction["credentialRun"].(map[string]interface{})
	if !ok {
		t.Fatalf("credential run is missing from instruction: %#v", instruction)
	}
	if credentialRun["id"] != "credrun_123" {
		t.Fatalf("credential run id missing from instruction: %#v", credentialRun)
	}
	materializations, ok := credentialRun["materializations"].([]interface{})
	if !ok || len(materializations) != 1 {
		t.Fatalf("credential materializations missing from instruction: %#v", credentialRun)
	}
	materialization, ok := materializations[0].(map[string]interface{})
	if !ok ||
		materialization["credentialId"] != "cred_registry" ||
		materialization["versionId"] != "credver_1" ||
		materialization["status"] != "verified" {
		t.Fatalf("credential materialization mismatch: %#v", materialization)
	}
	if _, leaked := materialization["payload"]; leaked {
		t.Fatalf("credential materialization instruction must not include payload: %#v", materialization)
	}
	inputs := instruction["inputs"].(map[string]interface{})
	if inputs["releaseId"] != strings.Repeat("b", 40) {
		t.Fatalf("release input not forwarded: %#v", inputs)
	}
	resultContract := instruction["resultContract"].(map[string]interface{})
	if resultContract["apiVersion"] != "doops.sh/v2" ||
		resultContract["kind"] != "DeploymentRun" ||
		resultContract["requireEvidence"] != true {
		t.Fatalf("unexpected structured result contract: %#v", resultContract)
	}
}

func TestGenerateWorkflowRunIDIsUniqueWithinOneSession(t *testing.T) {
	first, err := generateWorkflowRunID("release-test")
	if err != nil {
		t.Fatalf("generate first run id: %v", err)
	}
	second, err := generateWorkflowRunID("release-test")
	if err != nil {
		t.Fatalf("generate second run id: %v", err)
	}
	if first == second || !strings.HasPrefix(first, "release-test-") || !strings.HasPrefix(second, "release-test-") {
		t.Fatalf("run ids must be unique and session-bound: first=%q second=%q", first, second)
	}
}

func TestRunCICDCommandUsesDryRunCredentialPreparation(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create repository marker: %v", err)
	}
	workflow := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(workflow, []byte(`apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "environments.yaml"), []byte(`environments:
  production:
    target:
      name: agent-runtime
      cluster: doops-edu
      instance: edu-coder
`), 0o644); err != nil {
		t.Fatalf("write environment source: %v", err)
	}

	var mode string
	var declarationFieldsPresent bool
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/credentials/prepare" {
			t.Fatalf("unexpected credential prepare request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode credential prepare body: %v", err)
		}
		mode, _ = body["mode"].(string)
		_, workflowPresent := body["workflow_yaml"]
		_, configurationPresent := body["configuration_yaml"]
		declarationFieldsPresent = workflowPresent || configurationPresent
		_, _ = fmt.Fprint(w, `{
			"apiVersion":"doops.sh/v2",
			"kind":"CredentialRun",
			"id":"credrun_dry",
			"mode":"dry-run",
			"cluster":"doops-edu",
			"instance":"edu-coder",
			"materializations":[]
		}`)
	}))
	defer gateway.Close()

	caller := &fakeWorkflowCaller{output: workflowResultJSON("dry-run", 0)}
	originalPush := pushWorkflowWorkspace
	originalOpen := openWorkflowAgent
	var pushDryRun bool
	pushWorkflowWorkspace = func(_ Server, _ string, _ string, dryRun bool, _ []string, _ string) (string, error) {
		pushDryRun = dryRun
		return strings.Repeat("a", 40), nil
	}
	openWorkflowAgent = func(Server, *SessionStore, string, bool) (workflowAgentCaller, func()) {
		return caller, func() {}
	}
	t.Cleanup(func() {
		pushWorkflowWorkspace = originalPush
		openWorkflowAgent = originalOpen
	})

	err := runCICDCommand(
		context.Background(),
		[]string{"run", "-f", workflow, "-target", "agent-runtime", "-model", "quan2go/gpt-5-4", "--dry-run"},
		[]Server{{Name: "agent-runtime", Gateway: gateway.URL, Cluster: "doops-edu", Instance: "edu-coder"}},
		nil,
		NewSessionStore(),
		"release-test",
		false,
	)
	if err != nil {
		t.Fatalf("run dry-run workflow: %v", err)
	}
	if mode != "dry-run" {
		t.Fatalf("credential prepare mode mismatch: got %q", mode)
	}
	if pushDryRun || declarationFieldsPresent {
		t.Fatalf("dry-run must push the workspace and resolve declarations on the Agent")
	}
	if caller.arguments["operation"] != "ask" {
		t.Fatalf("dry-run must use ask operation: %#v", caller.arguments)
	}
	if caller.arguments["model"] != "quan2go/gpt-5-4" {
		t.Fatalf("dry-run model mismatch: %#v", caller.arguments)
	}
}

func TestRunCICDCommandStopsBeforePromptWhenCredentialPrepareFails(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create repository marker: %v", err)
	}
	workflow := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(workflow, []byte(
		"apiVersion: doops.sh/v2\nkind: DeploymentTemplate\n",
	), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"apiVersion":"doops.sh/v2","kind":"CredentialPrepareError","category":"credential_grant_denied"}`))
	}))
	defer gateway.Close()
	caller := &fakeWorkflowCaller{
		output: workflowResultJSON("apply", 1),
	}
	originalPush := pushWorkflowWorkspace
	originalOpen := openWorkflowAgent
	pushWorkflowWorkspace = func(Server, string, string, bool, []string, string) (string, error) {
		return strings.Repeat("a", 40), nil
	}
	openWorkflowAgent = func(Server, *SessionStore, string, bool) (workflowAgentCaller, func()) {
		return caller, func() {}
	}
	t.Cleanup(func() {
		pushWorkflowWorkspace = originalPush
		openWorkflowAgent = originalOpen
	})

	err := runCICDCommand(
		context.Background(),
		[]string{"run", "-f", workflow, "-target", "agent-runtime"},
		[]Server{{Name: "agent-runtime", Gateway: gateway.URL, Cluster: "doops-edu", Instance: "edu-coder"}},
		nil,
		NewSessionStore(),
		"release-test",
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "credential prepare failed: HTTP 403 Forbidden: credential_grant_denied") {
		t.Fatalf("credential prepare failure must be returned, got %v", err)
	}
	if caller.calls != 0 {
		t.Fatalf("credential prepare failure must not call prompt, got %d calls", caller.calls)
	}
}

func workflowResultJSON(
	mode string,
	mutationCount int,
) string {
	result := map[string]interface{}{
		"apiVersion": workspaceManifestAPIVersion,
		"kind":       "DeploymentRun",
		"metadata": map[string]interface{}{
			"workspaceCommit": strings.Repeat("a", 40),
		},
		"spec": map[string]interface{}{"mode": mode},
		"status": map[string]interface{}{
			"phase":         "planned",
			"mutationCount": mutationCount,
			"capabilities":  map[string]interface{}{"source-observer": "available"},
			"evidence": []interface{}{
				map[string]interface{}{
					"subject":    "source",
					"observedAt": "2026-07-14T00:00:00Z",
					"toolCall": map[string]interface{}{
						"id":     "tool-source",
						"tool":   "doops-source-observer",
						"input":  map[string]interface{}{"scope": "source"},
						"result": map[string]interface{}{"revision": "immutable"},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(result)
	return string(data)
}

func mutateWorkflowResultJSON(
	t *testing.T,
	base string,
	mutate func(status map[string]interface{}),
) string {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(base), &result); err != nil {
		t.Fatalf("decode workflow result fixture: %v", err)
	}
	status := result["status"].(map[string]interface{})
	mutate(status)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode mutated workflow result: %v", err)
	}
	return string(data)
}

func TestValidateWorkflowResultRejectsSyntheticResultsAndDryRunMutation(t *testing.T) {
	for name, result := range map[string]string{
		"synthetic admitted": `{
			"apiVersion":"doops.sh/v2",
			"kind":"DeploymentRun",
			"spec":{"mode":"dry-run"},
			"status":{"phase":"admitted","mutationCount":0,"evidence":[]}
		}`,
		"dry-run mutation": `{
			"apiVersion":"doops.sh/v2",
			"kind":"DeploymentRun",
			"spec":{"mode":"dry-run"},
			"status":{"phase":"planned","mutationCount":1,"evidence":[]}
		}`,
		"missing evidence field": `{
			"apiVersion":"doops.sh/v2",
			"kind":"DeploymentRun",
			"spec":{"mode":"dry-run"},
			"status":{"phase":"blocked","mutationCount":0}
		}`,
		"empty evidence": `{
			"apiVersion":"doops.sh/v2",
			"kind":"DeploymentRun",
			"spec":{"mode":"dry-run"},
				"status":{"phase":"planned","mutationCount":0,"evidence":[]}
			}`,
		"evidence missing observation time": `{
				"apiVersion":"doops.sh/v2",
				"kind":"DeploymentRun",
				"metadata":{"workspaceCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				"spec":{"mode":"dry-run"},
				"status":{
					"phase":"planned",
					"mutationCount":0,
					"capabilities":{"source-observer":"available"},
					"evidence":[{
						"subject":"source",
						"toolCall":{
							"id":"tool-source",
							"tool":"doops-source-observer",
							"input":{"scope":"source"},
							"result":{"revision":"immutable"}
						}
					}]
				}
			}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateWorkflowResult(
				result,
				"doops.sh/v2",
				"dry-run",
				strings.Repeat("a", 40),
			); err == nil {
				t.Fatal("invalid or synthetic result must be rejected")
			}
		})
	}
}

func TestValidateWorkflowResultRejectsUnboundEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(status map[string]interface{})
	}{
		{
			name: "missing tool call",
			mutate: func(status map[string]interface{}) {
				delete(status["evidence"].([]interface{})[0].(map[string]interface{}), "toolCall")
			},
		},
		{
			name: "unresolved tool reference",
			mutate: func(status map[string]interface{}) {
				status["evidence"].([]interface{})[0].(map[string]interface{})["toolRef"] =
					map[string]interface{}{"tool": "Read", "input": map[string]interface{}{"path": "state.json"}}
			},
		},
		{
			name: "retired evidence field",
			mutate: func(status map[string]interface{}) {
				status["evidence"].([]interface{})[0].(map[string]interface{})["module"] = "Read"
			},
		},
		{
			name: "missing input",
			mutate: func(status map[string]interface{}) {
				delete(
					status["evidence"].([]interface{})[0].(map[string]interface{})["toolCall"].(map[string]interface{}),
					"input",
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := mutateWorkflowResultJSON(
				t,
				workflowResultJSON("dry-run", 0),
				test.mutate,
			)
			if err := validateWorkflowResult(
				result,
				"doops.sh/v2",
				"dry-run",
				strings.Repeat("a", 40),
			); err == nil {
				t.Fatal("evidence must contain an eligible runtime tool call")
			}
		})
	}
}

func TestRunCICDCommandRequiresTargetBeforePush(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create repository marker: %v", err)
	}
	workflow := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(workflow, []byte("kind: DeploymentTemplate\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	originalPush := pushWorkflowWorkspace
	pushes := 0
	pushWorkflowWorkspace = func(Server, string, string, bool, []string, string) (string, error) {
		pushes++
		return strings.Repeat("a", 40), nil
	}
	t.Cleanup(func() { pushWorkflowWorkspace = originalPush })
	server := []Server{{Name: "agent-runtime", Gateway: "https://gateway.example.com"}}

	for name, args := range map[string][]string{
		"missing target": {"run", "-f", workflow, "--dry-run"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runCICDCommand(context.Background(), args, server, nil, NewSessionStore(), "release-test", false); err == nil {
				t.Fatal("invalid invocation must fail")
			}
		})
	}
	if pushes != 0 {
		t.Fatalf("invalid invocations must not push, got %d pushes", pushes)
	}
}

func TestWorkflowAPIVersionAcceptsV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: doops.sh/v3\nkind: EnvironmentDefinition\n"), 0o600); err != nil {
		t.Fatalf("write v3 environment: %v", err)
	}
	version, err := workflowAPIVersion(path)
	if err != nil {
		t.Fatalf("read v3 apiVersion: %v", err)
	}
	if version != "doops.sh/v3" {
		t.Fatalf("apiVersion = %q, want doops.sh/v3", version)
	}
	executionVersion, err := workflowExecutionAPIVersion(path)
	if err != nil {
		t.Fatalf("read v3 execution contract: %v", err)
	}
	if executionVersion != "doops.sh/v3" {
		t.Fatalf("execution apiVersion = %q, want doops.sh/v3", executionVersion)
	}
}

func TestRunCICDCommandRejectsUnsupportedWorkflowBeforePushOrAgentContact(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create repository marker: %v", err)
	}
	workflow := filepath.Join(root, "environment.yaml")
	if err := os.WriteFile(workflow, []byte(
		"apiVersion: doops.sh/v3\nkind: EnvironmentReleaseDeclaration\n",
	), 0o600); err != nil {
		t.Fatalf("write v3 environment: %v", err)
	}

	originalPush := pushWorkflowWorkspace
	originalOpen := openWorkflowAgent
	pushes := 0
	agentContacts := 0
	pushWorkflowWorkspace = func(Server, string, string, bool, []string, string) (string, error) {
		pushes++
		return strings.Repeat("a", 40), nil
	}
	openWorkflowAgent = func(Server, *SessionStore, string, bool) (workflowAgentCaller, func()) {
		agentContacts++
		return nil, func() {}
	}
	t.Cleanup(func() {
		pushWorkflowWorkspace = originalPush
		openWorkflowAgent = originalOpen
	})

	err := runCICDCommand(
		context.Background(),
		[]string{"run", "-f", workflow, "-target", "agent-runtime", "--dry-run"},
		[]Server{{
			Name: "agent-runtime", Gateway: "https://gateway.example.com",
			Cluster: "doops", Instance: "runner",
		}},
		nil,
		NewSessionStore(),
		"release-test",
		false,
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"requires doops.sh/v2 DeploymentTemplate or doops.sh/v3 EnvironmentDefinition",
	) {
		t.Fatalf("unsupported workflow error = %v", err)
	}
	if pushes != 0 || agentContacts != 0 {
		t.Fatalf("unsupported workflow caused side effects: pushes=%d agentContacts=%d", pushes, agentContacts)
	}
}
