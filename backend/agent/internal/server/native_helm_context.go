package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/symansun/doops/backend/agent/api"
)

type nativeHelmAuth struct {
	URL      string
	Username string
	Password string
}

type nativeHelmRecord struct {
	SessionID    string
	Namespace    string
	Release      string
	Chart        string
	CredentialID string
	GrantID      string
	VersionID    string
	ResourceVer  string
	Digest       string
	Auth         nativeHelmAuth
	Socket       string
	Listener     net.Listener
	Server       *http.Server
}

var nativeHelmContexts = struct {
	sync.Mutex
	bySocket map[string]*nativeHelmRecord
}{bySocket: make(map[string]*nativeHelmRecord)}

type nativeHelmRequest struct {
	SessionID string                     `json:"session_id"`
	Namespace string                     `json:"namespace"`
	Release   string                     `json:"release"`
	Chart     string                     `json:"chart"`
	Binding   api.AgentCredentialBinding `json:"binding"`
}

func materializeNativeHelmCredential(ctx context.Context, req CredentialMaterializeRequest, auth nativeHelmAuth, digest string) (CredentialMaterialization, error) {
	if ctx.Err() != nil || req.Scope.Use != CredentialUseHelmPull ||
		!validCredentialSHA256Digest(digest) ||
		strings.TrimSpace(auth.URL) == "" || strings.TrimSpace(auth.Username) == "" || strings.TrimSpace(auth.Password) == "" {
		return CredentialMaterialization{}, errors.New("helm_native_context_invalid")
	}
	directory, err := os.MkdirTemp("", "doops-native-helm-")
	if err != nil {
		return CredentialMaterialization{}, errors.New("helm_native_context_unavailable")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		_ = os.RemoveAll(directory)
		return CredentialMaterialization{}, errors.New("helm_native_context_unavailable")
	}
	socket := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(directory)
		return CredentialMaterialization{}, errors.New("helm_native_context_unavailable")
	}
	if err := os.Chmod(socket, 0600); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(directory)
		return CredentialMaterialization{}, errors.New("helm_native_context_unavailable")
	}
	record := &nativeHelmRecord{
		SessionID: req.SessionID, Namespace: req.Scope.Namespace, Release: req.Scope.HelmRelease,
		Chart: req.Scope.HelmChart, CredentialID: req.CredentialID, GrantID: req.GrantID,
		VersionID: req.VersionID, ResourceVer: "native-" + randomHex(16), Digest: digest,
		Auth: auth, Socket: socket, Listener: listener,
	}
	record.Server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		handleNativeHelmRequest(w, request, record)
	})}
	nativeHelmContexts.Lock()
	nativeHelmContexts.bySocket[socket] = record
	nativeHelmContexts.Unlock()
	go func() { _ = record.Server.Serve(listener) }()
	return CredentialMaterialization{
		CredentialID: req.CredentialID, GrantID: req.GrantID, VersionID: req.VersionID,
		ResourceName: credentialSecretName(req.CredentialID, req.GrantID),
		Namespace:    req.Scope.Namespace, Status: "verified", Digest: digest,
		Keys: nil, ResourceVersion: record.ResourceVer,
		ContextKind: "helm-native", ContextSocket: socket,
	}, nil
}

func handleNativeHelmRequest(w http.ResponseWriter, request *http.Request, record *nativeHelmRecord) {
	if request.Method != http.MethodPost || request.URL.Path != "/helm" {
		http.Error(w, "native_helm_context_unavailable", http.StatusNotFound)
		return
	}
	var input nativeHelmRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		input.SessionID != record.SessionID ||
		input.Binding.ContextKind != "helm-native" || input.Binding.ContextSocket != record.Socket ||
		input.Binding.CredentialID != record.CredentialID || input.Binding.GrantID != record.GrantID ||
		input.Binding.VersionID != record.VersionID || input.Binding.ResourceVersion != record.ResourceVer ||
		input.Binding.Digest != record.Digest {
		http.Error(w, "native_helm_context_unavailable", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record.Auth)
}

func cleanupNativeHelmCredential(req CredentialCleanupRequest) (bool, error) {
	nativeHelmContexts.Lock()
	var records []*nativeHelmRecord
	for socket, record := range nativeHelmContexts.bySocket {
		if record.Namespace != req.Namespace || record.CredentialID != req.CredentialID || record.GrantID != req.GrantID ||
			(!req.AnyVersion && record.VersionID != req.VersionID) {
			continue
		}
		delete(nativeHelmContexts.bySocket, socket)
		records = append(records, record)
	}
	nativeHelmContexts.Unlock()
	for _, record := range records {
		_ = record.Server.Close()
		_ = record.Listener.Close()
		_ = os.RemoveAll(filepath.Dir(record.Socket))
	}
	return len(records) != 0, nil
}
