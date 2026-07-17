package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestAgentPromptAuditSummaryNeverIncludesInstruction(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"operation":   "apply",
		"instruction": `{"credentialRun":{"unexpected":"synthetic-canary-secret"}}`,
	})
	if err != nil {
		t.Fatalf("encode tool args: %v", err)
	}
	summary := summarizeToolCall("doops_agent_prompt", raw)
	if strings.Contains(summary, "synthetic-canary-secret") ||
		summary != "doops_agent_prompt operation=apply" {
		t.Fatalf("unsafe agent prompt audit summary: %q", summary)
	}
}

func TestAgentPromptAuditNeverStoresOutputOrRawError(t *testing.T) {
	errMsg, tail := sanitizeAgentPromptAudit(
		"doops_agent_prompt",
		"Authorization: Bearer synthetic-canary",
		"token=synthetic-canary",
	)
	if errMsg != "credential_operation_failed" {
		t.Fatalf("error category = %q, want credential_operation_failed", errMsg)
	}
	if tail != "" {
		t.Fatalf("audit tail = %q, want empty", tail)
	}
}

func TestNonPromptAuditRedactsHighConfidenceCredentialMaterial(t *testing.T) {
	token := "github_pat_" + strings.Repeat("a", 82)
	errMsg, tail := sanitizeAgentPromptAudit(
		"doops_exec",
		"command failed with "+token,
		"stdout "+token,
	)
	if strings.Contains(errMsg, token) || strings.Contains(tail, token) {
		t.Fatalf("non-prompt audit retained credential material: err=%q tail=%q", errMsg, tail)
	}
}

func TestCredentialRunAuthorizationIsBoundAndSingleUse(t *testing.T) {
	store := openCredentialTestStore(t)
	user, token := createCredentialRunAuthorizationUser(t, store)
	auth := TokenAuth{UserID: user.ID, TokenID: token.ID}
	request := credentialPrepareHTTPBody{
		Cluster:          "doops",
		Instance:         "runner",
		SessionID:        "release-session",
		WorkflowPath:     "deploy/release.yaml",
		SourceRepository: "https://example.test/repo.git", SourceBranch: "main", Inputs: map[string]string{"reason": "test"},
		Mode: "dry-run",
	}
	run := CredentialRun{
		APIVersion:       "doops.sh/v2",
		Kind:             "CredentialRun",
		ID:               "credrun_single_use",
		Mode:             request.Mode,
		Cluster:          request.Cluster,
		Instance:         request.Instance,
		Template:         "release",
		Project:          "app",
		Environment:      "production",
		Materializations: []CredentialMaterialization{},
		CreatedAt:        time.Now().UTC(),
	}
	if err := store.IssueCredentialRunAuthorization(auth, request, run); err != nil {
		t.Fatalf("issue credential run authorization: %v", err)
	}

	tampered := run
	tampered.Environment = "staging"
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.SourceRepository, request.SourceBranch, request.Inputs, request.Mode, tampered,
	); !errors.Is(err, ErrCredentialPrepareBindingInvalid) {
		t.Fatalf("tampered credential run error = %v, want grant denied", err)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.SourceRepository, request.SourceBranch, request.Inputs, request.Mode, run,
	); err != nil {
		t.Fatalf("consume credential run authorization: %v", err)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.SourceRepository, request.SourceBranch, request.Inputs, request.Mode, run,
	); !errors.Is(err, ErrCredentialPrepareBindingInvalid) {
		t.Fatalf("replayed credential run error = %v, want grant denied", err)
	}
}

func TestRevokedCredentialRunAuthorizationCannotBeConsumed(t *testing.T) {
	store := openCredentialTestStore(t)
	user, token := createCredentialRunAuthorizationUser(t, store)
	auth := TokenAuth{UserID: user.ID, TokenID: token.ID}
	request := credentialPrepareHTTPBody{
		Cluster: "doops", Instance: "runner", SessionID: "release-session",
		WorkflowPath: "deploy/release.yaml", SourceRepository: "https://example.test/repo.git", SourceBranch: "main", Inputs: map[string]string{"reason": "test"}, Mode: "dry-run",
	}
	run := CredentialRun{
		APIVersion: "doops.sh/v2", Kind: "CredentialRun", ID: "credrun_revoked",
		Mode: request.Mode, Cluster: request.Cluster, Instance: request.Instance,
		Template: "release", Project: "app", Environment: "production",
		Materializations: []CredentialMaterialization{}, CreatedAt: time.Now().UTC(),
	}
	if err := store.IssueCredentialRunAuthorization(auth, request, run); err != nil {
		t.Fatalf("issue credential run authorization: %v", err)
	}
	if err := store.RevokeCredentialRunAuthorization(run.ID); err != nil {
		t.Fatalf("revoke credential run authorization: %v", err)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.SourceRepository, request.SourceBranch, request.Inputs, request.Mode, run,
	); !errors.Is(err, ErrCredentialPrepareBindingInvalid) {
		t.Fatalf("revoked credential run error = %v, want grant denied", err)
	}
}

func TestOfflineWorkflowDoesNotConsumeCredentialRunAuthorization(t *testing.T) {
	store := openCredentialTestStore(t)
	user, token := createCredentialRunAuthorizationUser(t, store)
	auth := TokenAuth{UserID: user.ID, TokenID: token.ID}
	request, run := credentialRunAuthorizationFixture("offline-runner", "credrun_offline")
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: request.Cluster, Instance: request.Instance,
		Actions: []GatewayAction{ActionAsk, ActionCredentialUse},
	}); err != nil {
		t.Fatalf("grant workflow actions: %v", err)
	}
	if err := store.IssueCredentialRunAuthorization(auth, request, run); err != nil {
		t.Fatalf("issue credential run authorization: %v", err)
	}

	response := invokeCredentialWorkflowPrompt(t, NewGatewayHub(store, GatewayHubOptions{}), auth, request, run)
	if response.Error == nil || response.Error.Code != -32004 {
		t.Fatalf("offline workflow response = %#v, want target offline", response.Error)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.SourceRepository, request.SourceBranch, request.Inputs, request.Mode, run,
	); err != nil {
		t.Fatalf("offline workflow consumed credential authorization: %v", err)
	}
}

func TestRateLimitedWorkflowDoesNotConsumeCredentialRunAuthorization(t *testing.T) {
	store := openCredentialTestStore(t)
	user, token := createCredentialRunAuthorizationUser(t, store)
	auth := TokenAuth{UserID: user.ID, TokenID: token.ID}
	request, run := credentialRunAuthorizationFixture("runner", "credrun_rate_limited")
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: request.Cluster, Instance: request.Instance,
		Actions: []GatewayAction{ActionAsk, ActionCredentialUse},
	}); err != nil {
		t.Fatalf("grant workflow actions: %v", err)
	}
	if err := store.IssueCredentialRunAuthorization(auth, request, run); err != nil {
		t.Fatalf("issue credential run authorization: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{MaxConcurrentOperations: 1, MaxConcurrentPerUser: 1})
	if err := hub.registry.Register(&GatewayAgent{
		Cluster: request.Cluster, Instance: request.Instance, Key: request.Cluster + "/" + request.Instance,
		CredentialPlanProtocol: credentialPlanProtocolV2,
		ConnectedAt:            time.Now().UTC(), LastSeen: time.Now().UTC(), closed: make(chan struct{}),
	}); err != nil {
		t.Fatalf("register workflow agent: %v", err)
	}
	release, err := hub.client.acquireOperationSlot(user.ID)
	if err != nil {
		t.Fatalf("occupy operation slot: %v", err)
	}
	defer release()

	response := invokeCredentialWorkflowPrompt(t, hub, auth, request, run)
	if response.Error == nil || response.Error.Code != -32006 {
		t.Fatalf("rate-limited workflow response = %#v, want operation limit", response.Error)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.SourceRepository, request.SourceBranch, request.Inputs, request.Mode, run,
	); err != nil {
		t.Fatalf("rate-limited workflow consumed credential authorization: %v", err)
	}
}

func TestCredentialPromptBindingRejectsSecretBearingInput(t *testing.T) {
	request, run := credentialRunAuthorizationFixture("runner", "credrun_secret_input")
	instruction, err := json.Marshal(map[string]any{
		"task": "execute-doops-cicd", "skill": "$doops-cicd",
		"executionMode": request.Mode, "manifestPath": request.WorkflowPath,
		"workspaceCommit": credentialRunFixtureCommit,
		"source": map[string]string{
			"repository": request.SourceRepository,
			"branch":     request.SourceBranch,
		},
		"inputs": map[string]string{"registryToken": "synthetic"}, "credentialRun": run,
	})
	if err != nil {
		t.Fatalf("encode workflow instruction: %v", err)
	}
	arguments, err := json.Marshal(map[string]any{
		"operation": "ask", "session_id": request.SessionID,
		"workspace_commit": credentialRunFixtureCommit, "response_format": "json",
		"instruction": string(instruction),
	})
	if err != nil {
		t.Fatalf("encode prompt arguments: %v", err)
	}
	if _, err := parseCredentialPromptBinding(arguments, request.Cluster, request.Instance); err == nil ||
		!strings.Contains(err.Error(), "credentialRef") {
		t.Fatalf("secret-bearing workflow input error = %v", err)
	}
}

func credentialRunAuthorizationFixture(instance, runID string) (credentialPrepareHTTPBody, CredentialRun) {
	request := credentialPrepareHTTPBody{
		Cluster: "doops", Instance: instance, SessionID: "release-session",
		WorkflowPath: "deploy/environments.yaml", SourceRepository: "https://example.test/repo.git",
		SourceBranch: "main", Inputs: map[string]string{"environment": "test", "service": "api"}, Mode: "dry-run",
	}
	run := CredentialRun{
		APIVersion: "doops.sh/v2", Kind: "CredentialRun", ID: runID,
		Mode: request.Mode, Cluster: request.Cluster, Instance: request.Instance,
		Template: "release", Project: "app", Environment: "production",
		Materializations: []CredentialMaterialization{}, CreatedAt: time.Now().UTC(),
	}
	return request, run
}

const credentialRunFixtureCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func invokeCredentialWorkflowPrompt(
	t *testing.T,
	hub *GatewayHub,
	auth TokenAuth,
	request credentialPrepareHTTPBody,
	run CredentialRun,
) api.JSONRPCResponse {
	t.Helper()
	instruction, err := json.Marshal(map[string]any{
		"task": "execute-doops-cicd", "skill": "$doops-cicd",
		"executionMode": request.Mode, "manifestPath": request.WorkflowPath,
		"workspaceCommit": credentialRunFixtureCommit,
		"source": map[string]string{
			"repository": request.SourceRepository,
			"branch":     request.SourceBranch,
		},
		"inputs": request.Inputs, "credentialRun": run,
	})
	if err != nil {
		t.Fatalf("encode workflow instruction: %v", err)
	}
	arguments, err := json.Marshal(map[string]any{
		"operation": "ask", "session_id": request.SessionID,
		"workspace_commit": credentialRunFixtureCommit, "response_format": "json",
		"instruction": string(instruction),
	})
	if err != nil {
		t.Fatalf("encode tool arguments: %v", err)
	}
	params, err := json.Marshal(api.ToolCallParams{Name: "doops_agent_prompt", Arguments: arguments})
	if err != nil {
		t.Fatalf("encode tool params: %v", err)
	}
	var response api.JSONRPCResponse
	if err := hub.client.handleGatewayToolCall(
		context.Background(), auth, request.Cluster, request.Instance,
		api.JSONRPCRequest{JSONRPC: "2.0", ID: 1, Params: params},
		func([]byte) error { return nil },
		func(value any) error {
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				return marshalErr
			}
			return json.Unmarshal(encoded, &response)
		},
	); err != nil {
		t.Fatalf("handle workflow prompt: %v", err)
	}
	return response
}

func createCredentialRunAuthorizationUser(t *testing.T, store *GatewayStore) (GatewayUser, CreatedToken) {
	t.Helper()
	user, err := store.CreateUser("credential-run-user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{
		Kind:   TokenKindUser,
		UserID: user.ID,
		Name:   "credential-run",
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return user, token
}
