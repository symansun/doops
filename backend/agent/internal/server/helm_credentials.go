package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/user/doops/agent/api"
)

type helmCredentialLease struct {
	SessionID string                       `json:"session_id"`
	Bindings  []api.AgentCredentialBinding `json:"bindings"`
}

type helmRepositoryAuth struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func helmCredentialLeasePath(session string) string {
	return filepath.Join(filepath.Dir(workspaceRoot()), ".doops-helm-bindings", session+".json")
}

// Only the Gateway-authorized prompt creates this metadata lease. It is outside
// the writable session checkout and contains no credential payload.
func bindHelmCredentialsForPrompt(session string, bindings []api.AgentCredentialBinding) (func(), error) {
	if validateSession(session) != nil {
		return nil, errors.New("helm_credential_session_invalid")
	}
	lease := helmCredentialLease{SessionID: session}
	for _, binding := range bindings {
		if binding.Use != string(CredentialUseHelmPull) {
			continue
		}
		if binding.SecretType != "" ||
			binding.ContextKind != "helm-native" || !validNativeHelmSocketPath(binding.ContextSocket) ||
			!validCredentialSHA256Digest(binding.Digest) ||
			!strings.HasPrefix(binding.CredentialID, "cred_") ||
			!strings.HasPrefix(binding.GrantID, "credgrant_") ||
			!strings.HasPrefix(binding.VersionID, "credver_") ||
			!validLowerHex(strings.TrimPrefix(binding.CredentialID, "cred_"), 24) ||
			!validLowerHex(strings.TrimPrefix(binding.GrantID, "credgrant_"), 24) ||
			!validLowerHex(strings.TrimPrefix(binding.VersionID, "credver_"), 24) {
			return nil, errors.New("helm_credential_binding_invalid")
		}
		lease.Bindings = append(lease.Bindings, binding)
	}
	path := helmCredentialLeasePath(session)
	if len(lease.Bindings) == 0 {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("helm_credential_lease_unavailable")
		}
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("helm_credential_lease_unavailable")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		return nil, errors.New("helm_credential_lease_unavailable")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("helm_credential_lease_unavailable")
	}
	cleanup := func() { _ = os.Remove(path) }
	err = json.NewEncoder(file).Encode(lease)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		cleanup()
		return nil, errors.New("helm_credential_lease_unavailable")
	}
	return cleanup, nil
}

func authorizedHelmCredential(ctx context.Context, params api.HelmParams) (*helmRepositoryAuth, string, error) {
	if validateSession(params.SessionID) != nil {
		return nil, "", errors.New("helm_credential_session_invalid")
	}
	path := helmCredentialLeasePath(params.SessionID)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, "", errors.New("helm_credential_lease_invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", errors.New("helm_credential_lease_invalid")
	}
	defer file.Close()
	var lease helmCredentialLease
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&lease) != nil || decoder.Decode(&struct{}{}) != io.EOF || lease.SessionID != params.SessionID {
		return nil, "", errors.New("helm_credential_lease_invalid")
	}
	var matched *helmRepositoryAuth
	var digest string
	var matchedCandidates []ambiguousCredentialCandidate
	for _, binding := range lease.Bindings {
		auth, err := requestHelmCredential(ctx, params, binding)
		if err != nil {
			clearHelmRepositoryAuth(matched)
			return nil, "", err
		}
		if auth == nil {
			continue
		}
		matchedCandidates = append(matchedCandidates, ambiguousCredentialCandidate{
			Name: binding.Name,
			ID:   binding.CredentialID,
		})
		if len(matchedCandidates) == 1 {
			matched, digest = auth, binding.Digest
			continue
		}
		clearHelmRepositoryAuth(matched)
		matched, digest = nil, ""
		clearHelmRepositoryAuth(auth)
	}
	if len(matchedCandidates) > 1 {
		return nil, "", fmt.Errorf(
			"helm_credential_binding_ambiguous: multiple credentials match the chart HTTPS origin (candidates: %s); select one with --credential <name-or-id>",
			formatAmbiguousCredentialCandidates(matchedCandidates),
		)
	}
	return matched, digest, nil
}

type ambiguousCredentialCandidate struct {
	Name string
	ID   string
}

func clearHelmRepositoryAuth(auth *helmRepositoryAuth) {
	if auth == nil {
		return
	}
	auth.URL = ""
	auth.Username = ""
	auth.Password = ""
}

// formatAmbiguousCredentialCandidates formats public, non-sensitive credential
// identifiers in deterministic order (name ascending, then credential ID
// ascending). Callers must pass only public metadata; never URLs, secrets,
// usernames, passwords, tokens, or socket paths.
func formatAmbiguousCredentialCandidates(candidates []ambiguousCredentialCandidate) string {
	normalized := make([]ambiguousCredentialCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		id := strings.TrimSpace(candidate.ID)
		if name == "" && id == "" {
			continue
		}
		normalized = append(normalized, ambiguousCredentialCandidate{Name: name, ID: id})
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Name != normalized[j].Name {
			return normalized[i].Name < normalized[j].Name
		}
		return normalized[i].ID < normalized[j].ID
	})
	formatted := make([]string, 0, len(normalized))
	for _, candidate := range normalized {
		switch {
		case candidate.Name != "" && candidate.ID != "":
			formatted = append(formatted, candidate.Name+" ["+candidate.ID+"]")
		case candidate.ID != "":
			formatted = append(formatted, candidate.ID)
		default:
			formatted = append(formatted, candidate.Name)
		}
	}
	return strings.Join(formatted, ", ")
}

// The private broker returns credentials only for its HTTPS origin, or 204
// for an unrelated Chart. Broker failures must not become anonymous downloads.
func requestHelmCredential(ctx context.Context, params api.HelmParams, binding api.AgentCredentialBinding) (*helmRepositoryAuth, error) {
	requestBody, err := json.Marshal(map[string]interface{}{
		"session_id": params.SessionID, "namespace": params.Namespace,
		"release": params.Release, "chart": params.Chart, "binding": binding,
	})
	if err != nil {
		return nil, errors.New("helm_credential_payload_invalid")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", binding.ContextSocket)
	}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://native/helm", strings.NewReader(string(requestBody)))
	if err != nil {
		return nil, errors.New("helm_native_context_unavailable")
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return nil, errors.New("helm_native_context_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("helm_native_context_unavailable")
	}
	var auth helmRepositoryAuth
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&auth) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("helm_credential_payload_invalid")
	}
	return &auth, nil
}

func validNativeHelmSocketPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path &&
		filepath.Base(path) == "broker.sock" &&
		strings.HasPrefix(filepath.Base(filepath.Dir(path)), "doops-native-helm-") &&
		!strings.ContainsAny(path, "\x00\r\n")
}
