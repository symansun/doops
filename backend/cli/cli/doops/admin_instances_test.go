package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminInstanceCommandsListArchiveUnarchiveDelete(t *testing.T) {
	var seen []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(body)))
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/instances":
			_, _ = w.Write([]byte(`{"instances":[{"cluster":"dev","instance":"retired","status":"offline","archived":true,"last_seen":"2026-11-01T00:00:00Z","remote":"10.0.0.2"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/instances/archive":
			_, _ = w.Write([]byte(`{"cluster":"dev","instance":"retired","status":"offline","archived":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/instances/unarchive":
			_, _ = w.Write([]byte(`{"cluster":"dev","instance":"retired","status":"offline","archived":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/instances/delete":
			_, _ = w.Write([]byte(`{"cluster":"dev","instance":"retired","deleted":true,"remaining":{"active_credential_grants":2,"revoking_credential_grants":1,"proxy_routes":1,"user_grants":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gw.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	auth := AuthStore{}
	auth.SetGateway(gw.URL, "admin-tok")
	authBytes, _ := json.Marshal(&auth)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), authBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	servers := []Server{{Name: "gw-admin", Gateway: gw.URL, Cluster: "dev", Instance: "live"}}

	var out bytes.Buffer
	if err := runAdminInstancesWithWriter([]string{"list", "--target", "gw-admin", "--include-history"}, servers, nil, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "cluster=dev instance=retired status=offline archived=true") {
		t.Fatalf("list out = %s", out.String())
	}

	out.Reset()
	if err := runAdminInstancesWithWriter([]string{"archive", "--target", "gw-admin", "--cluster", "dev", "--instance", "retired"}, servers, nil, &out); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out.String(), "archived=true") {
		t.Fatalf("archive out = %s", out.String())
	}

	out.Reset()
	if err := runAdminInstancesWithWriter([]string{"unarchive", "--target", "gw-admin", "--cluster", "dev", "--instance", "retired"}, servers, nil, &out); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if !strings.Contains(out.String(), "archived=false") {
		t.Fatalf("unarchive out = %s", out.String())
	}

	out.Reset()
	if err := runAdminInstancesWithWriter([]string{"delete", "--target", "gw-admin", "--cluster", "dev", "--instance", "retired"}, servers, nil, &out); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, want := range []string{
		"deleted=true",
		"remaining_active_credential_grants=2",
		"remaining_revoking_credential_grants=1",
		"remaining_proxy_routes=1",
		"remaining_user_grants=3",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("delete output missing %q: %s", want, out.String())
		}
	}

	if len(seen) != 4 || !strings.HasPrefix(seen[0], "GET /v1/admin/instances?include=history") ||
		!strings.HasPrefix(seen[3], "POST /v1/admin/instances/delete ") {
		t.Fatalf("unexpected gateway requests: %#v", seen)
	}

	out.Reset()
	if err := runAdminInstancesWithWriter([]string{"-h"}, nil, errors.New("no config"), &out); err != nil {
		t.Fatalf("help without config failed: %v", err)
	}
	if !strings.Contains(out.String(), "doops admin instance delete") {
		t.Fatalf("help output = %s", out.String())
	}
}

func TestCredentialForceOfflineCLIValidationAndRequests(t *testing.T) {
	var requests []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body)))
		switch r.Method {
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{"id":"credgrant_1","credential_id":"cred_1","state":"revoked"}`))
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"credential_id":"cred_1","revoked":true,"force_offline":true,"abandoned_targets":["dev/retired"]}`))
		}
	}))
	defer gw.Close()

	dir := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(dir, "config.json"))
	auth := AuthStore{}
	auth.SetGateway(gw.URL, "admin-tok")
	authBytes, _ := json.Marshal(&auth)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), authBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	servers := []Server{{Name: "gw-admin", Gateway: gw.URL, Cluster: "dev", Instance: "live"}}

	var out bytes.Buffer
	if err := runCredentialGrantRevoke([]string{"--target", "gw-admin", "--id", "cred_1", "--grant-id", "credgrant_1", "--force-offline"}, servers, nil, &out); err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("grant-revoke without reason = %v", err)
	}
	if err := runCredentialRevoke([]string{"--target", "gw-admin", "--id", "cred_1", "--reason", "only-reason"}, servers, nil, &out); err == nil || !strings.Contains(err.Error(), "--reason requires --force-offline") {
		t.Fatalf("revoke with reason only = %v", err)
	}

	out.Reset()
	if err := runCredentialGrantRevoke([]string{"--target", "gw-admin", "--id", "cred_1", "--grant-id", "credgrant_1", "--force-offline", "--reason", "cluster retired"}, servers, nil, &out); err != nil {
		t.Fatalf("grant-revoke force-offline: %v", err)
	}
	if !strings.Contains(out.String(), "force_offline=true") {
		t.Fatalf("grant-revoke out = %s", out.String())
	}

	out.Reset()
	if err := runCredentialRevoke([]string{"--target", "gw-admin", "--id", "cred_1", "--force-offline", "--reason", "cluster retired"}, servers, nil, &out); err != nil {
		t.Fatalf("revoke force-offline: %v", err)
	}
	if !strings.Contains(out.String(), "abandoned_targets=dev/retired") {
		t.Fatalf("revoke out = %s", out.String())
	}
	if len(requests) != 2 || !strings.Contains(requests[0], `"force_offline":true`) || !strings.Contains(requests[1], `"reason":"cluster retired"`) {
		t.Fatalf("requests = %#v", requests)
	}
}
