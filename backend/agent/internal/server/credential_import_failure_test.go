package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestCredentialImportFailureReportsTheRealCategory(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantCategory string
		wantStatus   int
	}{
		{"caller canceled", context.Canceled, "operation_canceled", http.StatusConflict},
		{"deadline", fmt.Errorf("relay: %w", context.DeadlineExceeded), "credential_import_timeout", http.StatusGatewayTimeout},
		{"relay timeout", errors.New("operation timed out"), "credential_import_timeout", http.StatusGatewayTimeout},
		{"target offline", errors.New("target offline: dev/local"), "target_offline", http.StatusServiceUnavailable},
		{"agent disconnected", fmt.Errorf("%w: dispatch failed: closed", errGatewayAgentDisconnected), "target_offline", http.StatusServiceUnavailable},
		{"active operation", errors.New("target busy: active operation op_1"), "target_busy", http.StatusConflict},
		{"cancellation pending", errors.New("target busy: cancellation pending"), "target_busy", http.StatusConflict},
		{"queue full", errors.New("target queue full"), "target_queue_full", http.StatusConflict},
		{"queue timeout", errors.New("target queue timeout"), "target_queue_timeout", http.StatusConflict},
		{"audit unavailable", errors.New("execution_audit_unavailable"), "credential_audit_unavailable", http.StatusInternalServerError},
		{"audit completion", errors.New("execution_audit_completion_failed: disk"), "credential_audit_unavailable", http.StatusInternalServerError},
		{"agent without tool", errors.New("credential_import_agent_unsupported"), "credential_import_agent_unsupported", http.StatusBadGateway},
		{"agent refusal", errors.New("credential_import_key_missing"), "credential_import_key_missing", http.StatusBadGateway},
		{"source unavailable", errors.New("credential_import_source_unavailable"), "credential_import_source_unavailable", http.StatusBadGateway},
		{"free text is not echoed", errors.New("kubectl said: synthetic-import-canary"), "credential_import_failed", http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category, status := credentialImportFailure(tt.err)
			if category != tt.wantCategory || status != tt.wantStatus {
				t.Fatalf("got %s/%d, want %s/%d", category, status, tt.wantCategory, tt.wantStatus)
			}
			if strings.Contains(category, "canary") {
				t.Fatal("category echoed error text")
			}
		})
	}
}

// Given an import against a target that is not connected, When the import
// runs, Then the caller gets target_offline with 503, no version is staged and
// the source Secret is never read.
func TestCredentialImportHTTPReportsOfflineTarget(t *testing.T) {
	f, argsLog := credentialImportFixture(t)
	var resource CredentialResource
	if err := json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"offline-db","scope":"personal","type":"opaque"}`, http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"import-offline","cluster":"dev","instance":"not-connected",
		"namespace":"apps","secret_name":"source-db","keys":["DB_PASSWORD"]}`
	response := f.http(t, f.admin, http.MethodPost, "/v1/credentials/"+resource.ID+"/import-secret",
		body, http.StatusServiceUnavailable)
	if strings.TrimSpace(string(response)) != "target_offline" {
		t.Fatalf("response = %q, want target_offline", response)
	}
	if versions, err := f.store.CredentialVersions(resource.ID); err != nil || len(versions) != 0 {
		t.Fatalf("offline import staged a version: %#v, %v", versions, err)
	}
	if logged, _ := os.ReadFile(argsLog); len(logged) != 0 {
		t.Fatalf("offline import reached a source Secret: %s", logged)
	}
}

// Given a connected Agent whose source Secret lacks the requested key, When the
// import runs, Then the caller gets the Agent's category, not a bare failure.
func TestCredentialImportHTTPReportsAgentCategory(t *testing.T) {
	f, _ := credentialImportFixture(t)
	var resource CredentialResource
	if err := json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"missing-key-db","scope":"personal","type":"opaque"}`, http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"import-missing","cluster":"dev","instance":"local",
		"namespace":"apps","secret_name":"source-db","keys":["MISSING"]}`
	response := f.http(t, f.admin, http.MethodPost, "/v1/credentials/"+resource.ID+"/import-secret",
		body, http.StatusBadGateway)
	if strings.TrimSpace(string(response)) != "credential_import_key_missing" {
		t.Fatalf("response = %q, want credential_import_key_missing", response)
	}
	if strings.Contains(string(response), importCanary) {
		t.Fatal("import error exposed the source Secret")
	}
}
