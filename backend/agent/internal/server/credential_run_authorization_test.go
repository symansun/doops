package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
		Cluster:         "doops",
		Instance:        "runner",
		SessionID:       "release-session",
		WorkflowPath:    "deploy/release.yaml",
		WorkspaceCommit: strings.Repeat("a", 40),
		Mode:            "dry-run",
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
		request.WorkspaceCommit, request.Mode, tampered,
	); !errors.Is(err, ErrCredentialPrepareBindingInvalid) {
		t.Fatalf("tampered credential run error = %v, want grant denied", err)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.WorkspaceCommit, request.Mode, run,
	); err != nil {
		t.Fatalf("consume credential run authorization: %v", err)
	}
	if err := store.ConsumeCredentialRunAuthorization(
		auth, request.Cluster, request.Instance, request.SessionID, request.WorkflowPath,
		request.WorkspaceCommit, request.Mode, run,
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
		WorkflowPath: "deploy/release.yaml", WorkspaceCommit: strings.Repeat("a", 40), Mode: "dry-run",
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
		request.WorkspaceCommit, request.Mode, run,
	); !errors.Is(err, ErrCredentialPrepareBindingInvalid) {
		t.Fatalf("revoked credential run error = %v, want grant denied", err)
	}
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
