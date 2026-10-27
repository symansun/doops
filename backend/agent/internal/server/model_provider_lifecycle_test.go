package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modelLifecycleCanary = "synthetic-model-lifecycle-canary"

// modelLifecycleSetup points the in-process Agent at a temporary settings file
// and replaces the real doagent probe, so the lifecycle runs end to end through
// the Gateway HTTP API and the Agent control channel without a live model.
func modelLifecycleSetup(t *testing.T, probeErr error) (*executionLifecycleFixture, string) {
	t.Helper()
	settingsPath := filepath.Join(t.TempDir(), ".agent", "settings.json")
	previousFile, previousProbe := doagentSettingsFile, probeModelProvider
	doagentSettingsFile = settingsPath
	probeModelProvider = func(context.Context, modelProviderPayload) error { return probeErr }
	t.Cleanup(func() { doagentSettingsFile, probeModelProvider = previousFile, previousProbe })
	f, _ := credentialImportFixture(t)
	return f, settingsPath
}

func modelLifecyclePayload() string {
	return `{"model":"gw/text-model","provider":{"gw":{"options":{"apiKey":"` + modelLifecycleCanary +
		`","baseURL":"https://models.example.com/v1"}}}}`
}

func createModelProviderCredential(t *testing.T, f *executionLifecycleFixture) (string, CredentialVersion, CredentialGrant) {
	t.Helper()
	var resource CredentialResource
	json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"target-model","scope":"platform","type":"modelProvider"}`, http.StatusOK), &resource)
	base := "/v1/credentials/" + resource.ID
	var version CredentialVersion
	put := f.http(t, f.admin, http.MethodPut, base+"/payload", modelLifecyclePayload(), http.StatusOK)
	json.Unmarshal(put, &version)
	var grant CredentialGrant
	json.Unmarshal(f.http(t, f.admin, http.MethodPost, base+"/grants",
		`{"scope":{"cluster":"dev","instance":"local","namespace":"default"}}`, http.StatusOK), &grant)
	for _, body := range [][]byte{put} {
		if strings.Contains(string(body), modelLifecycleCanary) {
			t.Fatalf("payload canary leaked in response: %s", body)
		}
	}
	return base, version, grant
}

func modelVerifyBody(versionID, cluster, instance string) string {
	body, _ := json.Marshal(map[string]any{
		"session_id": "model-lifecycle", "version_id": versionID,
		"scope": map[string]string{"cluster": cluster, "instance": instance},
	})
	return string(body)
}

func readDeliveredModel(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return settings
}

// Given a platform modelProvider credential bound to an online target,
// When it is verified, promoted and its grant revoked,
// Then the Agent's fixed settings file gains and then loses the model, and no
// response carries the payload.
func TestModelProviderLifecycleDeliversAndRevokesThroughGateway(t *testing.T) {
	f, settingsPath := modelLifecycleSetup(t, nil)
	base, version, grant := createModelProviderCredential(t, f)

	responses := []string{}
	responses = append(responses, string(f.http(t, f.admin, http.MethodPost, base+"/promote",
		`{"version_id":"`+version.ID+`"}`, http.StatusConflict)))
	responses = append(responses, string(f.http(t, f.admin, http.MethodPost, base+"/verify",
		modelVerifyBody(version.ID, "dev", "local"), http.StatusOK)))
	if _, err := os.Stat(settingsPath); err == nil {
		if settings := readDeliveredModel(t, settingsPath); settings["model"] != nil {
			t.Fatalf("verify must not leave an unpromoted model in place: %v", settings["model"])
		}
	}
	responses = append(responses, string(f.http(t, f.admin, http.MethodPost, base+"/promote",
		`{"version_id":"`+version.ID+`"}`, http.StatusOK)))

	settings := readDeliveredModel(t, settingsPath)
	if settings["model"] != "gw/text-model" {
		t.Fatalf("promoted model not delivered: %v", settings["model"])
	}
	if len(settings["mcp_servers"].([]any)) != 2 || settings["permissions"] == nil {
		t.Fatalf("runtime MCP servers and permissions must be kept: %v", settings)
	}
	if info, err := os.Stat(settingsPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings file mode = %v, %v; want 0600", info, err)
	}

	// Reconnect delivery: the Gateway re-materializes the active version on its own.
	if category := f.hub.deliverModelProvider(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("reconnect delivery category = %q", category)
	}

	revoke, _ := json.Marshal(map[string]string{"grant_id": grant.ID})
	responses = append(responses, string(f.http(t, f.admin, http.MethodDelete, base+"/grants",
		string(revoke), http.StatusOK)))
	if settings := readDeliveredModel(t, settingsPath); settings["model"] != nil || settings["provider"] != nil {
		t.Fatalf("revoked grant must remove the delivered model: %v", settings)
	}
	if _, delivered := readModelProviderRecord(); delivered {
		t.Fatal("revoked grant must remove the delivery record")
	}
	for _, response := range responses {
		if strings.Contains(response, modelLifecycleCanary) {
			t.Fatalf("payload canary leaked in response: %s", response)
		}
	}
	audit, err := f.store.ListAudit(200)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(audit)
	if strings.Contains(string(encoded), modelLifecycleCanary) {
		t.Fatal("payload canary leaked into audit")
	}
}

// Given a modelProvider whose real model call fails,
// When it is verified,
// Then verification reports a stable category, promotion stays blocked and
// the settings file never receives the provider.
func TestModelProviderVerifyFailureBlocksPromotion(t *testing.T) {
	f, settingsPath := modelLifecycleSetup(t, errors.New("model_auth_failed"))
	base, version, _ := createModelProviderCredential(t, f)

	response := string(f.http(t, f.admin, http.MethodPost, base+"/verify",
		modelVerifyBody(version.ID, "dev", "local"), http.StatusBadGateway))
	if !strings.Contains(response, "model_auth_failed") || strings.Contains(response, modelLifecycleCanary) {
		t.Fatalf("verify failure response = %q", response)
	}
	f.http(t, f.admin, http.MethodPost, base+"/promote", `{"version_id":"`+version.ID+`"}`, http.StatusConflict)
	if _, err := os.Stat(settingsPath); err == nil {
		if settings := readDeliveredModel(t, settingsPath); settings["provider"] != nil {
			t.Fatalf("failed verification must not deliver a provider: %v", settings)
		}
	}
}

// Denied authority and unavailable targets fail closed with stable statuses.
func TestModelProviderVerifyDeniesWrongAuthorityAndTarget(t *testing.T) {
	cases := []struct {
		name     string
		cluster  string
		instance string
		outsider bool
		want     int
	}{
		{name: "user without target or ownership", cluster: "dev", instance: "local", outsider: true, want: http.StatusForbidden},
		{name: "target without a binding", cluster: "dev", instance: "other", want: http.StatusForbidden},
		{name: "missing target", want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := modelLifecycleSetup(t, nil)
			base, version, _ := createModelProviderCredential(t, f)
			token := f.admin
			if tc.outsider {
				token = f.user(t, "outsider", ScopeGrant{Cluster: "elsewhere", Instance: "other"})
			}
			f.http(t, token, http.MethodPost, base+"/verify",
				modelVerifyBody(version.ID, tc.cluster, tc.instance), tc.want)
		})
	}
}

// A modelProvider can only be granted for model use, and a second provider on
// the same target is refused rather than silently replacing the first.
func TestModelProviderGrantRejectsWrongUseAndSecondProvider(t *testing.T) {
	f, _ := modelLifecycleSetup(t, nil)
	base, _, _ := createModelProviderCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/grants",
		`{"scope":{"cluster":"dev","instance":"local","namespace":"default","use":"opaqueSecret"}}`,
		http.StatusBadRequest)

	var second CredentialResource
	json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"second-model","scope":"platform","type":"modelProvider"}`, http.StatusOK), &second)
	response := string(f.http(t, f.admin, http.MethodPost, "/v1/credentials/"+second.ID+"/grants",
		`{"scope":{"cluster":"dev","instance":"local","namespace":"default"}}`, http.StatusBadRequest))
	if !strings.Contains(response, "model_provider_already_bound") {
		t.Fatalf("second provider grant response = %q", response)
	}
}
