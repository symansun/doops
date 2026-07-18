package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialCLIManagesMetadataAndReadsPutPayloadOnlyFromStdin(t *testing.T) {
	const payloadCanary = "credential-payload-must-not-be-printed"
	var requests []credentialCLIRequest
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		requests = append(requests, credentialCLIRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Auth:   r.Header.Get("Authorization"),
			Body:   body,
		})
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/credentials":
			_, _ = w.Write([]byte(`{"id":"cred_123","name":"registry","scope":"personal","type":"registry","state":"active","payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/credentials":
			_, _ = w.Write([]byte(`{"credentials":[{"id":"cred_123","name":"registry","scope":"personal","type":"registry","state":"active","payload":"` + payloadCanary + `"}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v1/credentials/cred_123/payload":
			_, _ = w.Write([]byte(`{"id":"credver_456","credential_id":"cred_123","payload_digest":"sha256:abc","state":"staged","payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/credentials/cred_123/grants":
			_, _ = w.Write([]byte(`{"id":"credgrant_789","credential_id":"cred_123","grantee_id":"user_42","cluster":"doops-edu","instance":"edu-coder","uses":["imagePull"],"payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/credentials/cred_123/grants":
			_, _ = w.Write([]byte(`{"id":"credgrant_789","credential_id":"cred_123","state":"revoked","payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/credentials/cred_123/verify":
			_, _ = w.Write([]byte(`{"status":"verified","evidence":[{"status":"verified","resourceVersion":"42","digest":"sha256:manifest","payload":"` + payloadCanary + `"}],"payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/credentials/cred_123/promote":
			_, _ = w.Write([]byte(`{"credential_id":"cred_123","version_id":"credver_456","promoted":true,"payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/credentials/cred_123/revoke":
			_, _ = w.Write([]byte(`{"credential_id":"cred_123","revoked":true,"payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/credential-bundles":
			_, _ = w.Write([]byte(`{"id":"credbundle_1","name":"release","scope":"platform","state":"active","payload":"` + payloadCanary + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/credential-bundles":
			_, _ = w.Write([]byte(`{"id":"credbundle_1","name":"release","scope":"platform","state":"active","payload":"` + payloadCanary + `"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer gateway.Close()

	binary := buildDoopsCLIBinary(t)
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", "..", "..", ".."))
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"servers":[{
		"name":"gw-edu-release-runner",
		"gateway":"`+gateway.URL+`",
		"cluster":"doops-edu",
		"instance":"release-runner"
	}]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	auth := AuthStore{Tokens: map[string]string{}}
	auth.SetGateway(gateway.URL, "cli-token")
	authData, err := json.Marshal(&auth)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), "auth.json"), authData, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	run := func(stdin string, args ...string) string {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Dir = repositoryRoot
		command.Env = append(os.Environ(),
			"DOOPS_CONFIG="+config,
			"DOOPS_CREDENTIAL_PAYLOAD="+payloadCanary,
		)
		command.Stdin = strings.NewReader(stdin)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("doops %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		if strings.Contains(string(output), payloadCanary) {
			t.Fatalf("credential CLI output exposed payload: %s", output)
		}
		return string(output)
	}

	run("", "credential", "create", "--target", "gw-edu-release-runner", "--name", "registry", "--scope", "personal", "--type", "registry")
	run("", "credential", "list", "--target", "gw-edu-release-runner")
	run(`{"server":"registry.example.com","username":"deploy","password":"stdin-wins"}`,
		"credential", "put", "--target", "gw-edu-release-runner", "--id", "cred_123")
	run("", "credential", "grant", "--target", "gw-edu-release-runner", "--id", "cred_123",
		"--cluster", "doops-edu", "--instance", "edu-coder",
		"--namespace", "apps", "--use", "imagePull",
		"--workload-kind", "Deployment", "--workload-name", "api",
		"--image-repository", "team/api", "--image-digest", "sha256:manifest")
	run("", "-session", "credential-verify", "credential", "verify",
		"--target", "gw-edu-release-runner", "--id", "cred_123", "--version-id", "credver_456",
		"--cluster", "doops-edu", "--instance", "edu-coder",
		"--namespace", "apps", "--use", "imagePull",
		"--workload-kind", "Deployment", "--workload-name", "api",
		"--image-repository", "team/api", "--image-digest", "sha256:manifest")
	run("", "credential", "promote", "--target", "gw-edu-release-runner", "--id", "cred_123", "--version-id", "credver_456")
	run("", "credential", "grant-revoke", "--target", "gw-edu-release-runner", "--id", "cred_123", "--grant-id", "credgrant_789")
	run("", "credential", "revoke", "--target", "gw-edu-release-runner", "--id", "cred_123")
	run(`{"name":"release","scope":"platform","items":[{"credential_id":"cred_123","use":"imagePull","namespace":"apps","workload":{"Kind":"Deployment","Name":"api"},"registry_repository":"team/api","registry_reference":"sha256:manifest"}]}`,
		"credential", "bundle-create", "--target", "gw-edu-release-runner")
	run("", "credential", "bundle-get", "--target", "gw-edu-release-runner", "--name", "release")

	command := exec.Command(binary, "credential", "put", "--target", "gw-edu-release-runner", "--id", "cred_123", "--payload", payloadCanary)
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
	command.Stdin = strings.NewReader(`{"password":"stdin-is-required"}`)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("put must reject payload flags, output: %s", output)
	}
	command = exec.Command(binary, "credential", "list", "--gateway", gateway.URL, "--token", "argv-token")
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("credential commands must reject token argv flags, output: %s", output)
	}

	if len(requests) != 10 {
		t.Fatalf("gateway request count mismatch: got %d want 10", len(requests))
	}
	for _, request := range requests {
		if request.Auth != "Bearer cli-token" {
			t.Fatalf("credential request auth mismatch: %#v", request)
		}
	}
	assertCredentialCLIJSON(t, requests[0], http.MethodPost, "/v1/credentials", map[string]any{
		"name": "registry", "scope": "personal", "type": "registry",
	})
	if requests[1].Method != http.MethodGet || requests[1].Path != "/v1/credentials" {
		t.Fatalf("credential list request mismatch: %#v", requests[1])
	}
	if requests[1].Query.Get("view") != "target" ||
		requests[1].Query.Get("cluster") != "doops-edu" ||
		requests[1].Query.Get("instance") != "release-runner" {
		t.Fatalf("credential list target query mismatch: %#v", requests[1].Query)
	}
	assertCredentialCLIJSON(t, requests[2], http.MethodPut, "/v1/credentials/cred_123/payload", map[string]any{
		"server": "registry.example.com", "username": "deploy", "password": "stdin-wins",
	})
	if len(requests[2].Query) != 0 {
		t.Fatalf("credential put must not activate through a query parameter: %#v", requests[2].Query)
	}
	assertCredentialCLIJSON(t, requests[3], http.MethodPost, "/v1/credentials/cred_123/grants", map[string]any{
		"grantee_id": "",
		"scope": map[string]any{
			"cluster":          "doops-edu",
			"instance":         "edu-coder",
			"namespace":        "apps",
			"use":              "imagePull",
			"workload":         map[string]any{"kind": "Deployment", "name": "api"},
			"image_repository": "team/api",
			"image_digest":     "sha256:manifest",
		},
	})
	assertCredentialCLIJSON(t, requests[4], http.MethodPost, "/v1/credentials/cred_123/verify", map[string]any{
		"version_id": "credver_456",
		"session_id": "credential-verify",
		"scope": map[string]any{
			"cluster":          "doops-edu",
			"instance":         "edu-coder",
			"namespace":        "apps",
			"use":              "imagePull",
			"workload":         map[string]any{"kind": "Deployment", "name": "api"},
			"image_repository": "team/api",
			"image_digest":     "sha256:manifest",
		},
	})
	assertCredentialCLIJSON(t, requests[5], http.MethodPost, "/v1/credentials/cred_123/promote", map[string]any{
		"version_id": "credver_456",
	})
	assertCredentialCLIJSON(t, requests[6], http.MethodDelete, "/v1/credentials/cred_123/grants", map[string]any{
		"grant_id": "credgrant_789",
	})
	if requests[7].Method != http.MethodPost || requests[7].Path != "/v1/credentials/cred_123/revoke" || len(requests[7].Body) != 0 {
		t.Fatalf("credential revoke request mismatch: %#v", requests[7])
	}
	if requests[8].Method != http.MethodPost || requests[8].Path != "/v1/credential-bundles" {
		t.Fatalf("credential bundle create request mismatch: %#v", requests[8])
	}
	if requests[9].Method != http.MethodGet || requests[9].Path != "/v1/credential-bundles" ||
		requests[9].Query.Get("name") != "release" {
		t.Fatalf("credential bundle get request mismatch: %#v", requests[9])
	}
}

func TestResolveCredentialGatewayRequiresConfiguredTarget(t *testing.T) {
	if _, _, err := resolveCredentialGateway("", nil, nil); err == nil || !strings.Contains(err.Error(), "--target is required") {
		t.Fatalf("expected target requirement, got %v", err)
	}
}

func TestCredentialCLIHelp(t *testing.T) {
	var output bytes.Buffer
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		output.Reset()
		if err := runCredentialCommand(args, "", nil, nil, strings.NewReader(""), &output); err != nil {
			t.Fatalf("credential help %v: %v", args, err)
		}
		if !strings.Contains(output.String(), "doops credential list") ||
			!strings.Contains(output.String(), "doops credential create") ||
			!strings.Contains(output.String(), "doops credential preflight") ||
			!strings.Contains(output.String(), "doops credential verify") {
			t.Fatalf("credential help %v missing commands: %s", args, output.String())
		}
	}
	output.Reset()
	if err := runCredentialCommand(
		[]string{"create", "-h"}, "", nil, nil, strings.NewReader(""), &output,
	); err != nil {
		t.Fatalf("credential create help: %v", err)
	}
	if !strings.Contains(output.String(), "-name string") ||
		!strings.Contains(output.String(), "-target string") {
		t.Fatalf("credential create help missing flags: %s", output.String())
	}
}

type credentialCLIRequest struct {
	Method string
	Path   string
	Query  url.Values
	Auth   string
	Body   []byte
}

func buildDoopsCLIBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "doops")
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build doops CLI: %v\n%s", err, output)
	}
	return binary
}

func assertCredentialCLIJSON(t *testing.T, request credentialCLIRequest, method, path string, want map[string]any) {
	t.Helper()
	if request.Method != method || request.Path != path {
		t.Fatalf("request route mismatch: %#v", request)
	}
	var got map[string]any
	if err := json.Unmarshal(request.Body, &got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if !jsonEquivalent(got, want) {
		t.Fatalf("request body mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func jsonEquivalent(got, want map[string]any) bool {
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	return bytes.Equal(gotJSON, wantJSON)
}
