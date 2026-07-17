package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

type credentialPlanToolParams struct {
	SessionID        string            `json:"session_id"`
	WorkflowPath     string            `json:"workflow_path"`
	SourceRepository string            `json:"source_repository"`
	SourceBranch     string            `json:"source_branch"`
	SourceRevision   string            `json:"source_revision"`
	Inputs           map[string]string `json:"inputs,omitempty"`
}

var cloneCredentialPlanSource = checkoutWorkflowSource

func handleCredentialPlanTool(argBytes json.RawMessage) (string, error) {
	var args credentialPlanToolParams
	decoder := json.NewDecoder(bytes.NewReader(argBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid credential plan params")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid credential plan params")
	}
	args.SessionID = strings.TrimSpace(args.SessionID)
	args.WorkflowPath = strings.TrimSpace(args.WorkflowPath)
	args.SourceRepository = strings.TrimSpace(args.SourceRepository)
	args.SourceBranch = strings.TrimSpace(args.SourceBranch)
	args.SourceRevision = strings.TrimSpace(args.SourceRevision)
	if args.SessionID == "" || args.WorkflowPath == "" ||
		args.SourceRepository == "" || args.SourceBranch == "" ||
		args.WorkflowPath != "deploy/environments.yaml" ||
		!validCredentialWorkspaceCommit(args.SourceRevision) {
		return "", fmt.Errorf("session_id, deploy/environments.yaml, source_repository, source_branch, and source_revision are required")
	}
	if err := validateCredentialPlanSourceRepository(args.SourceRepository); err != nil {
		return "", err
	}
	for key, value := range args.Inputs {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(key) != key ||
			strings.TrimSpace(value) != value || credentialSecretLikeKey(key) ||
			credentialScalarContainsInlineMaterial(value) {
			return "", fmt.Errorf("invalid credential plan inputs")
		}
	}
	if err := cloneCredentialPlanSource(
		args.SessionID,
		args.SourceRepository,
		args.SourceBranch,
		args.SourceRevision,
	); err != nil {
		return "", err
	}
	root, err := workspacePath(args.SessionID)
	if err != nil {
		return "", err
	}
	command := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	head, err := command.Output()
	if err != nil || strings.TrimSpace(string(head)) != args.SourceRevision {
		return "", fmt.Errorf("workflow source revision mismatch")
	}
	workflowData, err := readCredentialDeclarationFile(root, args.WorkflowPath)
	if err != nil {
		return "", err
	}
	var identity credentialWorkflowIdentity
	if err := yaml.Unmarshal(workflowData, &identity); err != nil ||
		identity.APIVersion != "doops.sh/v3" ||
		identity.Kind != "EnvironmentRegistry" {
		return "", invalidCredentialDeclaration(
			fmt.Errorf("credential plan root must be doops.sh/v3 EnvironmentRegistry"),
		)
	}
	plan, err := parseCredentialPlanForInputs(root, args.WorkflowPath, args.Inputs)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode credential plan: %w", err)
	}
	return string(result), nil
}

func validateCredentialPlanSourceRepository(repository string) error {
	parsed, err := url.Parse(strings.TrimSpace(repository))
	if err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		return nil
	}
	return fmt.Errorf("workflow source repository must be an HTTPS URL without inline credentials, query, or fragment")
}

func handleCredentialMaterializeTool(ctx context.Context, argBytes json.RawMessage) (string, error) {
	var args CredentialMaterializeRequest
	decoder := json.NewDecoder(bytes.NewReader(argBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid credential materialize params")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid credential materialize params")
	}
	if err := validateSession(strings.TrimSpace(args.SessionID)); err != nil {
		return "", fmt.Errorf("invalid credential materialize session: %w", err)
	}
	result, err := materializeCredential(ctx, args)
	clear(args.Payload)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode credential materialization: %w", err)
	}
	return string(encoded), nil
}

func handleCredentialCleanupTool(ctx context.Context, argBytes json.RawMessage) (string, error) {
	var args CredentialCleanupRequest
	decoder := json.NewDecoder(bytes.NewReader(argBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid credential cleanup params")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid credential cleanup params")
	}
	if err := validateSession(strings.TrimSpace(args.SessionID)); err != nil {
		return "", fmt.Errorf("invalid credential cleanup session: %w", err)
	}
	if err := cleanupCredential(ctx, args); err != nil {
		return "", err
	}
	return `{"status":"removed"}`, nil
}
