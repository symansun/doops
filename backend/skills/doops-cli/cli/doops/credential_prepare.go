package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const credentialRunAPIVersion = "doops.sh/v2"

type credentialPrepareRequest struct {
	Cluster          string            `json:"cluster"`
	Instance         string            `json:"instance"`
	SessionID        string            `json:"session_id"`
	WorkflowPath     string            `json:"workflow_path"`
	SourceRepository string            `json:"source_repository"`
	SourceBranch     string            `json:"source_branch"`
	Inputs           map[string]string `json:"inputs"`
	Mode             string            `json:"mode"`
}

type credentialRun struct {
	APIVersion       string                              `json:"apiVersion"`
	Kind             string                              `json:"kind"`
	ID               string                              `json:"id"`
	Mode             string                              `json:"mode"`
	Cluster          string                              `json:"cluster"`
	Instance         string                              `json:"instance"`
	Template         string                              `json:"template"`
	Project          string                              `json:"project"`
	Environment      string                              `json:"environment"`
	MutationCount    int                                 `json:"mutationCount"`
	Materializations []credentialMaterializationMetadata `json:"materializations"`
	CreatedAt        time.Time                           `json:"createdAt"`
}

type credentialMaterializationMetadata struct {
	CredentialID    string   `json:"credentialId"`
	GrantID         string   `json:"grantId,omitempty"`
	VersionID       string   `json:"versionId"`
	ResourceName    string   `json:"resourceName,omitempty"`
	Namespace       string   `json:"namespace"`
	SecretType      string   `json:"secretType,omitempty"`
	Keys            []string `json:"keys,omitempty"`
	ResourceVersion string   `json:"resourceVersion,omitempty"`
	Digest          string   `json:"digest,omitempty"`
	Fingerprint     string   `json:"fingerprint,omitempty"`
	ExpiresAt       string   `json:"expiresAt,omitempty"`
	Status          string   `json:"status"`
	ErrorCategory   string   `json:"errorCategory,omitempty"`
}

func prepareWorkflowCredentials(
	ctx context.Context,
	server Server,
	token string,
	request credentialPrepareRequest,
) (credentialRun, error) {
	request.Cluster = strings.TrimSpace(request.Cluster)
	request.Instance = strings.TrimSpace(request.Instance)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.WorkflowPath = strings.TrimSpace(request.WorkflowPath)
	request.SourceRepository = strings.TrimSpace(request.SourceRepository)
	request.SourceBranch = strings.TrimSpace(request.SourceBranch)
	request.Mode = strings.TrimSpace(request.Mode)
	if request.Cluster == "" || request.Instance == "" || request.SessionID == "" ||
		request.WorkflowPath == "" || request.SourceRepository == "" || request.SourceBranch == "" ||
		(request.Mode != "apply" && request.Mode != "dry-run") {
		return credentialRun{}, fmt.Errorf("invalid credential prepare request")
	}

	endpoint, err := gatewayURLWithPath(server.Gateway, "/v1/credentials/prepare", nil)
	if err != nil {
		return credentialRun{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return credentialRun{}, fmt.Errorf("encode credential prepare request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return credentialRun{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if token = strings.TrimSpace(token); token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 11 * time.Minute}).Do(httpRequest)
	if err != nil {
		return credentialRun{}, fmt.Errorf("credential prepare request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		category, decodeErr := decodeCredentialPrepareFailure(body)
		if decodeErr != nil {
			return credentialRun{}, fmt.Errorf("invalid credential prepare error response: HTTP %s", response.Status)
		}
		return credentialRun{}, credentialPrepareFailureError(
			response.Status,
			category,
			response.Header.Get("Doops-Agent-Required-Protocol"),
		)
	}

	var run credentialRun
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&run); err != nil {
		return credentialRun{}, fmt.Errorf("decode credential prepare response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return credentialRun{}, fmt.Errorf("credential prepare response must contain exactly one JSON object")
	}
	if run.APIVersion != credentialRunAPIVersion || run.Kind != "CredentialRun" ||
		strings.TrimSpace(run.ID) == "" || run.Mode != request.Mode ||
		run.Cluster != request.Cluster || run.Instance != request.Instance {
		return credentialRun{}, fmt.Errorf("invalid credential prepare response")
	}
	if run.Materializations == nil {
		run.Materializations = []credentialMaterializationMetadata{}
	}
	for _, materialization := range run.Materializations {
		if err := validateCredentialMaterializationMetadata(materialization); err != nil {
			return credentialRun{}, fmt.Errorf("invalid credential prepare response")
		}
	}
	return run, nil
}

func validateCredentialMaterializationMetadata(materialization credentialMaterializationMetadata) error {
	if !validCredentialMetadataToken(materialization.CredentialID, 128) ||
		!validCredentialMetadataToken(materialization.GrantID, 128) ||
		!validCredentialMetadataToken(materialization.VersionID, 128) ||
		!validKubernetesCredentialMetadataName(materialization.Namespace) {
		return fmt.Errorf("invalid credential materialization metadata")
	}
	switch materialization.Status {
	case "planned", "verified", "outcome_unknown":
	default:
		return fmt.Errorf("invalid credential materialization metadata")
	}
	if materialization.ResourceName != "" ||
		materialization.SecretType != "" ||
		len(materialization.Keys) != 0 ||
		materialization.ResourceVersion != "" ||
		materialization.Digest != "" ||
		materialization.Fingerprint != "" ||
		materialization.ExpiresAt != "" ||
		materialization.ErrorCategory != "" {
		return fmt.Errorf("invalid credential materialization metadata")
	}
	return nil
}

func validKubernetesCredentialMetadataName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') ||
			char == '-' || char == '.' {
			continue
		}
		if index == 0 || index == len(value)-1 {
			return false
		}
		return false
	}
	return value[0] != '-' && value[0] != '.' &&
		value[len(value)-1] != '-' && value[len(value)-1] != '.'
}

func validCredentialMetadataToken(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' ||
			char == '.' || char == ':' || char == '/' {
			continue
		}
		return false
	}
	return true
}

func credentialPrepareFailureError(status, category, requiredProtocol string) error {
	requiredProtocol = strings.TrimSpace(requiredProtocol)
	if requiredProtocol == "" {
		return fmt.Errorf("credential prepare failed: HTTP %s: %s", status, category)
	}
	return fmt.Errorf("credential prepare failed: HTTP %s: %s required_protocol=%s", status, category, requiredProtocol)
}

func decodeCredentialPrepareFailure(body []byte) (string, error) {
	var failure struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Category   string `json:"category"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&failure); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF ||
		failure.APIVersion != credentialRunAPIVersion ||
		failure.Kind != "CredentialPrepareError" ||
		!validCredentialErrorCategory(failure.Category) {
		return "", fmt.Errorf("invalid credential prepare failure")
	}
	return failure.Category, nil
}

func validCredentialErrorCategory(category string) bool {
	if category == "" || len(category) > 128 {
		return false
	}
	for _, char := range category {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}
