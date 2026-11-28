package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A newly attached standalone runtime has no local delivery record and no
// Kubernetes client. The active Gateway model must still be delivered over the
// Agent control channel, and its grant must remain revocable.
func TestActiveModelDeliveryToFreshRuntimeWithoutKubernetes(t *testing.T) {
	f, settingsPath := modelLifecycleSetup(t, nil)
	base, version, grant := createModelProviderCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify",
		modelVerifyBody(version.ID, "dev", "local"), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote",
		`{"version_id":"`+version.ID+`"}`, http.StatusOK)

	// Model the empty home of a replacement Agent while the Gateway retains
	// its active model and audit inventory.
	for _, path := range []string{settingsPath, modelProviderRecordPath()} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	f.hub.registry.Get("dev", "local").setDeliveredModelVersion("")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))

	if category := f.hub.deliverModelProvider(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("standalone delivery category = %q", category)
	}
	settings := readDeliveredModel(t, settingsPath)
	if settings["model"] != "gw/text-model" || settings["permissions"] == nil ||
		len(settings["mcp_servers"].([]any)) != 2 {
		t.Fatal("standalone delivery did not install the model and runtime settings")
	}
	record, delivered := readModelProviderRecord()
	if !delivered || record.GrantID != grant.ID || record.VersionID != version.ID {
		t.Fatalf("delivery record does not match the active binding: %#v", record)
	}
	if info, err := os.Stat(settingsPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings mode = %v, %v; want 0600", info, err)
	}

	revoke, _ := json.Marshal(map[string]string{"grant_id": grant.ID})
	f.http(t, f.admin, http.MethodDelete, base+"/grants", string(revoke), http.StatusOK)
	if settings := readDeliveredModel(t, settingsPath); settings["model"] != nil || settings["provider"] != nil {
		t.Fatal("revoking a standalone binding left its model installed")
	}
	if category := f.hub.deliverModelProvider(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("delivery after revocation category = %q", category)
	}
	if _, delivered := readModelProviderRecord(); delivered {
		t.Fatal("delivery after revocation restored a model record")
	}
	audit, err := f.store.ListAudit(200)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(audit)
	if strings.Contains(string(encoded), modelLifecycleCanary) {
		t.Fatal("model payload leaked into audit")
	}
}

// A transient model outage during reconnect must be reported without deleting
// the model that the same active binding already delivered.
func TestModelDeliveryProbeFailurePreservesNativeSettings(t *testing.T) {
	f, settingsPath := modelLifecycleSetup(t, nil)
	base, version, _ := createModelProviderCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify",
		modelVerifyBody(version.ID, "dev", "local"), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote",
		`{"version_id":"`+version.ID+`"}`, http.StatusOK)

	before := make(map[string][]byte)
	for _, path := range []string{settingsPath, modelProviderRecordPath()} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = data
	}
	f.hub.registry.Get("dev", "local").setDeliveredModelVersion("")
	probeModelProvider = func(context.Context, modelProviderPayload) error {
		return errors.New("model_unavailable")
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))

	if category := f.hub.deliverModelProvider(context.Background(), "dev", "local"); category != "model_unavailable" {
		t.Fatalf("failed delivery category = %q, want model_unavailable", category)
	}
	for path, expected := range before {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("failed model probe changed native file %s: %v", filepath.Base(path), err)
		}
	}
	if got := f.hub.registry.Get("dev", "local").deliveredModelVersion(); got != "" {
		t.Fatalf("failed delivery was cached as successful: %q", got)
	}
}
