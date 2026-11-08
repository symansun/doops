package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/symansun/doops/backend/agent/api"
)

type dosqlCredentialLease struct {
	SessionID string                       `json:"session_id"`
	Bindings  []api.AgentCredentialBinding `json:"bindings"`
}

func dosqlCredentialLeasePath(session string) string {
	return filepath.Join(filepath.Dir(workspaceRoot()), ".doops-dosql-bindings", session+".json")
}

func bindDoSQLCredentialsForPrompt(session string, bindings []api.AgentCredentialBinding) (func(), error) {
	if validateSession(session) != nil {
		return nil, errors.New("dosql_credential_session_invalid")
	}
	lease := dosqlCredentialLease{SessionID: session}
	for _, binding := range bindings {
		if binding.Use != string(CredentialUseDatabase) {
			continue
		}
		if err := validateDoSQLBinding(binding); err != nil {
			return nil, err
		}
		lease.Bindings = append(lease.Bindings, binding)
	}
	path := dosqlCredentialLeasePath(session)
	if len(lease.Bindings) == 0 {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("dosql_credential_lease_unavailable")
		}
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("dosql_credential_lease_unavailable")
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("dosql_credential_lease_unavailable")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("dosql_credential_lease_unavailable")
	}
	cleanup := func() { _ = os.Remove(path) }
	if err := json.NewEncoder(file).Encode(lease); err != nil {
		_ = file.Close()
		cleanup()
		return nil, errors.New("dosql_credential_lease_unavailable")
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, errors.New("dosql_credential_lease_unavailable")
	}
	return cleanup, nil
}

func authorizedDoSQLCredential(session, name string) (api.AgentCredentialBinding, error) {
	if validateSession(session) != nil || strings.TrimSpace(name) == "" {
		return api.AgentCredentialBinding{}, errors.New("dosql_credential_scope_invalid")
	}
	path := dosqlCredentialLeasePath(session)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return api.AgentCredentialBinding{}, errors.New("dosql_credential_lease_invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return api.AgentCredentialBinding{}, errors.New("dosql_credential_lease_invalid")
	}
	defer file.Close()
	var lease dosqlCredentialLease
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&lease) != nil || decoder.Decode(&struct{}{}) != io.EOF || lease.SessionID != session {
		return api.AgentCredentialBinding{}, errors.New("dosql_credential_lease_invalid")
	}
	var matched *api.AgentCredentialBinding
	for index := range lease.Bindings {
		binding := &lease.Bindings[index]
		if (binding.Name != name && binding.CredentialID != name) || binding.Use != string(CredentialUseDatabase) {
			continue
		}
		if matched != nil {
			return api.AgentCredentialBinding{}, errors.New("dosql_credential_binding_ambiguous")
		}
		matched = binding
	}
	if matched == nil {
		return api.AgentCredentialBinding{}, fmt.Errorf("dosql_credential_reference_not_bound")
	}
	return *matched, nil
}

func validateDoSQLBinding(binding api.AgentCredentialBinding) error {
	if binding.Use != string(CredentialUseDatabase) ||
		!validCredentialMetadataToken(binding.Name, 128) ||
		!validKubernetesCredentialName(binding.Namespace) ||
		!validKubernetesCredentialName(binding.ResourceName) ||
		!validCredentialMetadataToken(binding.CredentialID, 128) ||
		!validCredentialMetadataToken(binding.GrantID, 128) ||
		!validCredentialMetadataToken(binding.VersionID, 128) ||
		len(binding.Keys) == 0 {
		return errors.New("dosql_credential_binding_invalid")
	}
	if binding.SecretType != "Opaque" || binding.ResourceVersion == "" {
		return errors.New("dosql_credential_binding_invalid")
	}
	return nil
}
