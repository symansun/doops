package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCredentialPreflightChecksProjectRequirementsWithoutExposingPayload(t *testing.T) {
	const payloadCanary = "preflight-must-not-print-payload"
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/credentials" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("view") != "target" ||
			r.URL.Query().Get("cluster") != "doops-dev" ||
			r.URL.Query().Get("instance") != "workstation-1" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer cli-token" {
			t.Fatalf("unexpected authorization header")
		}
		_, _ = w.Write([]byte(`{"credentials":[
			{"id":"cred_git","name":"cnb-development","scope":"personal","type":"gitToken","state":"active",
			 "grants":[{"id":"grant_git","state":"active","scope":{"use":"gitCheckout"}}],
			 "payload":"` + payloadCanary + `"},
			{"id":"cred_registry","name":"cnb-registry","scope":"platform","type":"registry","state":"active",
			 "grants":[{"id":"grant_registry","state":"active","scope":{"use":"imagePull"}}]},
			{"id":"cred_database","name":"cnb-database","scope":"personal","type":"opaque","state":"active",
			 "grants":[{"id":"grant_database","state":"active","scope":{"use":"database"}}]}
		]}`))
	}))
	defer gateway.Close()

	binary, config := buildCredentialPreflightCLI(t, gateway.URL)
	manifest := writeCredentialPreflightManifest(t, `
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-dev
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
  - credentialRef: cnb-registry
    use: imagePull
  - credentialRef: cnb-database
    use: database
`)
	command := exec.Command(binary, "-session", "dev-preflight", "credential", "preflight", "--manifest", manifest)
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("credential preflight: %v\n%s", err, output)
	}
	if requests.Load() != 1 {
		t.Fatalf("request count: got %d want 1", requests.Load())
	}
	text := string(output)
	for _, expected := range []string{
		"credential_ref=cnb-development use=gitCheckout credential_id=cred_git status=ready",
		"credential_ref=cnb-registry use=imagePull credential_id=cred_registry status=ready",
		"credential_ref=cnb-database use=database credential_id=cred_database status=ready",
		"credential_preflight=ready target=gw-dev requirements=3",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("preflight output missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, payloadCanary) {
		t.Fatalf("preflight output exposed payload: %s", text)
	}
	if strings.Contains(text, manifest) {
		t.Fatalf("preflight output exposed local manifest path: %s", text)
	}
}

func TestCredentialPreflightFailsForMissingAndUnavailableTargetGrant(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"credentials":[
			{"id":"cred_git","name":"wrong-use","type":"gitToken","state":"active",
			 "grants":[{"id":"grant_git","state":"active","scope":{"use":"gitCheckout"}}]},
			{"id":"cred_revoked","name":"revoked-grant","type":"registry","state":"active",
			 "grants":[{"id":"grant_revoked","state":"revoked","scope":{"use":"imagePull"}}]},
			{"id":"cred_ambiguous_1","name":"ambiguous","type":"tls","state":"active",
			 "grants":[{"id":"grant_ambiguous_1","state":"active","scope":{"use":"tls"}}]},
			{"id":"cred_ambiguous_2","name":"ambiguous","type":"tls","state":"active",
			 "grants":[{"id":"grant_ambiguous_2","state":"active","scope":{"use":"tls"}}]}
		]}`))
	}))
	defer gateway.Close()

	binary, config := buildCredentialPreflightCLI(t, gateway.URL)
	manifest := writeCredentialPreflightManifest(t, `
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-dev
credentials:
  - credentialRef: missing
    use: gitCheckout
  - credentialRef: wrong-use
    use: imagePull
  - credentialRef: revoked-grant
    use: imagePull
  - credentialRef: ambiguous
    use: tls
`)
	command := exec.Command(binary, "-session", "dev-preflight", "credential", "preflight", "--manifest", manifest)
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("credential preflight must fail: %s", output)
	}
	text := string(output)
	for _, expected := range []string{
		"credential_ref=missing use=gitCheckout status=missing",
		"credential_ref=wrong-use use=imagePull credential_id=cred_git status=wrong_use credential_type=gitToken",
		"credential_ref=revoked-grant use=imagePull credential_id=cred_revoked status=no_active_target_binding",
		"credential_ref=ambiguous use=tls status=ambiguous matches=2",
		"credential preflight failed: 4 requirement(s) unavailable",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("preflight failure output missing %q: %s", expected, text)
		}
	}
}

func TestDevelopmentCredentialManifestIsStrictMetadataOnly(t *testing.T) {
	manifest := writeCredentialPreflightManifest(t, `
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-dev
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
    password: must-not-be-accepted
`)
	_, err := readDevelopmentCredentialManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "field password not found") {
		t.Fatalf("expected unknown secret field rejection, got %v", err)
	}

	duplicate := writeCredentialPreflightManifest(t, `
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-dev
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
  - credentialRef: cnb-development
    use: gitCheckout
`)
	_, err = readDevelopmentCredentialManifest(duplicate)
	if err == nil || !strings.Contains(err.Error(), "duplicate credential requirement") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}

	for _, invalidRef := range []string{
		"https://user:password@example.com",
		"../../private/token",
		"ghp_012345678901234567890123456789012345",
		"/Users/alice/.config/secret",
	} {
		t.Run("reject_ref_"+strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(invalidRef), func(t *testing.T) {
			path := writeCredentialPreflightManifest(t, `
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-dev
credentials:
  - credentialRef: `+invalidRef+`
    use: gitCheckout
`)
			_, err := readDevelopmentCredentialManifest(path)
			if err == nil || !strings.Contains(err.Error(), "credentialRef must be a lowercase resource name") {
				t.Fatalf("expected unsafe credentialRef rejection, got %v", err)
			}
		})
	}
}

func TestCredentialPreflightRequiresSessionAndMatchingTarget(t *testing.T) {
	manifestPath := writeCredentialPreflightManifest(t, `
apiVersion: doops.sh/v1
kind: DevelopmentCredentialRequirements
target: gw-dev
credentials:
  - credentialRef: cnb-development
    use: gitCheckout
`)
	var output bytes.Buffer
	if err := runCredentialPreflight(
		[]string{"--manifest", manifestPath}, "", nil, nil, &output,
	); err == nil || !strings.Contains(err.Error(), "-session is required") {
		t.Fatalf("expected session requirement, got %v", err)
	}
	if err := runCredentialPreflight(
		[]string{"--manifest", manifestPath, "--target", "other-target"},
		"dev-preflight", nil, nil, &output,
	); err == nil || !strings.Contains(err.Error(), "does not match manifest target") {
		t.Fatalf("expected target mismatch, got %v", err)
	}
}

func buildCredentialPreflightCLI(t *testing.T, gatewayURL string) (string, string) {
	t.Helper()
	binary := buildDoopsCLIBinary(t)
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"servers":[{
		"name":"gw-dev",
		"gateway":"`+gatewayURL+`",
		"cluster":"doops-dev",
		"instance":"workstation-1"
	}]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	auth := AuthStore{Tokens: map[string]string{}}
	auth.SetGateway(gatewayURL, "cli-token")
	authData, err := json.Marshal(&auth)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), "auth.json"), authData, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	return binary, config
}

func writeCredentialPreflightManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doops-credentials.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}
