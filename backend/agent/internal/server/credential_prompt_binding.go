package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/user/doops/agent/api"
)

type credentialPromptBinding struct {
	SessionID        string
	WorkflowPath     string
	SourceRepository string
	SourceBranch     string
	SourceRevision   string
	Inputs           map[string]string
	Mode             string
	Run              CredentialRun
}

func parseCredentialPromptBinding(arguments json.RawMessage, cluster, instance string) (*credentialPromptBinding, error) {
	var prompt api.AgentPromptParams
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prompt); err != nil {
		return nil, errors.New("invalid agent prompt arguments")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid agent prompt arguments")
	}

	var marker struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal([]byte(prompt.Instruction), &marker); err != nil {
		return nil, nil
	}
	if strings.TrimSpace(marker.Task) != "execute-doops-cicd" {
		return nil, nil
	}

	var envelope struct {
		Task            string `json:"task"`
		Skill           string `json:"skill"`
		ExecutionMode   string `json:"executionMode"`
		ManifestPath    string `json:"manifestPath"`
		WorkspaceCommit string `json:"workspaceCommit"`
		Source          struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
		} `json:"source"`
		Inputs        map[string]string `json:"inputs"`
		CredentialRun CredentialRun     `json:"credentialRun"`
	}
	decoder = json.NewDecoder(strings.NewReader(prompt.Instruction))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New("invalid doops-cicd credential binding")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid doops-cicd credential binding")
	}

	mode := strings.TrimSpace(envelope.ExecutionMode)
	operation := strings.ToLower(strings.TrimSpace(prompt.Operation))
	expectedOperation := "apply"
	if mode == "dry-run" {
		expectedOperation = "ask"
	}
	if envelope.Task != "execute-doops-cicd" || envelope.Skill != "$doops-cicd" ||
		(mode != "apply" && mode != "dry-run") || operation != expectedOperation ||
		strings.TrimSpace(prompt.SessionID) == "" ||
		strings.TrimSpace(envelope.ManifestPath) == "" ||
		!validCredentialWorkspaceCommit(envelope.WorkspaceCommit) ||
		strings.TrimSpace(prompt.WorkspaceCommit) != strings.TrimSpace(envelope.WorkspaceCommit) ||
		strings.TrimSpace(envelope.Source.Repository) == "" ||
		strings.TrimSpace(envelope.Source.Branch) == "" ||
		envelope.CredentialRun.APIVersion != "doops.sh/v2" ||
		envelope.CredentialRun.Kind != "CredentialRun" ||
		strings.TrimSpace(envelope.CredentialRun.ID) == "" ||
		envelope.CredentialRun.Cluster != strings.TrimSpace(cluster) ||
		envelope.CredentialRun.Instance != strings.TrimSpace(instance) ||
		envelope.CredentialRun.Mode != mode {
		return nil, errors.New("invalid doops-cicd credential binding")
	}
	for key, value := range envelope.Inputs {
		if credentialSecretLikeKey(key) || credentialScalarContainsInlineMaterial(value) {
			return nil, errors.New("sensitive workflow input is forbidden; use credentialRef")
		}
	}

	return &credentialPromptBinding{
		SessionID:        strings.TrimSpace(prompt.SessionID),
		WorkflowPath:     strings.TrimSpace(envelope.ManifestPath),
		SourceRepository: strings.TrimSpace(envelope.Source.Repository),
		SourceBranch:     strings.TrimSpace(envelope.Source.Branch),
		SourceRevision:   strings.TrimSpace(envelope.WorkspaceCommit),
		Inputs:           envelope.Inputs,
		Mode:             mode,
		Run:              envelope.CredentialRun,
	}, nil
}

func (h *ClientService) consumeCredentialPromptBinding(
	auth TokenAuth,
	cluster string,
	instance string,
	binding *credentialPromptBinding,
) error {
	if binding == nil {
		return nil
	}
	return h.store.ConsumeCredentialRunAuthorization(
		auth,
		cluster,
		instance,
		binding.SessionID,
		binding.WorkflowPath,
		binding.SourceRepository,
		binding.SourceBranch,
		binding.Inputs,
		binding.Mode,
		binding.Run,
	)
}
