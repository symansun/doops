package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const configureCanary = "credential-configure-payload-canary"

type configureHTTPMock struct {
	mu          sync.Mutex
	requests    []credentialCLIRequest
	resource    credentialResource
	managed     []credentialResource
	grant       credentialGrant
	grants      []credentialGrant
	override    map[string]string
	status      map[string]int
	verifyCalls int
	failVerify  int
}

func newConfigureHTTPMock() *configureHTTPMock {
	resource := credentialResource{ID: "cred_1", Name: "database", Scope: "personal", Type: "opaque", State: "active"}
	grant := credentialGrant{
		ID: "grant_1", CredentialID: "cred_1", GranteeID: "user_1", State: "active",
		Scope: credentialGrantScope{Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: "database"},
	}
	return &configureHTTPMock{
		resource: resource, managed: []credentialResource{resource},
		grant: grant, grants: []credentialGrant{grant}, override: map[string]string{}, status: map[string]int{},
	}
}

func (mock *configureHTTPMock) serveHTTP(w http.ResponseWriter, r *http.Request) {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	mock.requests = append(mock.requests, credentialCLIRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Auth: r.Header.Get("Authorization"), Body: body,
	})
	key := r.Method + " " + r.URL.Path
	if status := mock.status[key]; status != 0 {
		w.WriteHeader(status)
	}
	if raw, ok := mock.override[key]; ok {
		io.WriteString(w, raw)
		return
	}
	var response any
	switch key {
	case "GET /v1/credentials":
		response = map[string]any{"credentials": mock.managed}
	case "POST /v1/credentials":
		response = mock.resource
	case "PUT /v1/credentials/cred_1/payload", "POST /v1/credentials/cred_1/import-secret":
		response = credentialVersion{ID: "version_1", CredentialID: "cred_1", State: "staged", PayloadDigest: "sha256:metadata"}
	case "POST /v1/credentials/cred_1/grants":
		response = mock.grant
	case "GET /v1/credentials/cred_1/grants":
		response = map[string]any{"grants": mock.grants}
	case "POST /v1/credentials/cred_1/verify":
		mock.verifyCalls++
		var request struct {
			Scope credentialGrantScope `json:"scope"`
		}
		json.Unmarshal(body, &request)
		var evidence []map[string]string
		for _, grant := range mock.grants {
			if grant.State == "active" && configureCredentialScopeKey(grant.Scope) == configureCredentialScopeKey(request.Scope) {
				item := map[string]string{"status": "verified", "resourceVersion": "42"}
				switch grant.Scope.Use {
				case "imagePull", "gitCheckout", "helmPull":
					item["digest"] = "sha256:" + strings.Repeat("a", 64)
				}
				evidence = append(evidence, item)
			}
		}
		status := "verified"
		if mock.failVerify == mock.verifyCalls {
			status = "failed"
		}
		response = map[string]any{"status": status, "evidence": evidence}
	case "POST /v1/credentials/cred_1/promote":
		response = map[string]any{"credential_id": "cred_1", "version_id": "version_1", "promoted": true}
	default:
		http.Error(w, "unexpected request", http.StatusNotFound)
		return
	}
	encoded, _ := json.Marshal(response)
	var metadata map[string]any
	json.Unmarshal(encoded, &metadata)
	metadata["payload"] = configureCanary
	json.NewEncoder(w).Encode(metadata)
}

func configureTestServers(t *testing.T, mock *configureHTTPMock) (string, []Server) {
	t.Helper()
	gateway := httptest.NewServer(http.HandlerFunc(mock.serveHTTP))
	t.Cleanup(gateway.Close)
	config := filepath.Join(t.TempDir(), "config.json")
	servers := []Server{{Name: "db", Gateway: gateway.URL, Cluster: "cluster-a", Instance: "instance-a"}}
	data, err := json.Marshal(map[string]any{"servers": servers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	auth := AuthStore{Tokens: map[string]string{}}
	auth.SetGateway(gateway.URL, "configure-test-token")
	data, err = json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return config, servers
}

func configureArgs(extra ...string) []string {
	return append([]string{"-session", "configure-test", "credential", "configure",
		"--target", "db", "--namespace", "apps", "--use", "database"}, extra...)
}

func runConfigureBinary(t *testing.T, binary, config, stdin string, args []string) (string, error) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
	command.Stdin = strings.NewReader(stdin)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), configureCanary) {
		t.Fatalf("credential output exposed payload: %s", output)
	}
	return string(output), err
}

func configureRequestSnapshot(mock *configureHTTPMock) []credentialCLIRequest {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	return append([]credentialCLIRequest(nil), mock.requests...)
}

func assertConfigureRoutes(t *testing.T, mock *configureHTTPMock, want []string) []credentialCLIRequest {
	t.Helper()
	requests := configureRequestSnapshot(mock)
	got := make([]string, 0, len(requests))
	for _, request := range requests {
		got = append(got, request.Method+" "+request.Path)
		if request.Auth != "Bearer configure-test-token" {
			t.Errorf("request did not use existing Gateway login")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request lifecycle mismatch\ngot: %v\nwant: %v", got, want)
	}
	return requests
}

func TestCredentialConfigureCLI(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	lifecycle := []string{
		"GET /v1/credentials", "PUT /v1/credentials/cred_1/payload",
		"POST /v1/credentials/cred_1/grants", "GET /v1/credentials/cred_1/grants",
		"POST /v1/credentials/cred_1/verify", "POST /v1/credentials/cred_1/promote",
	}
	for _, selection := range []struct {
		name string
		args []string
	}{
		{"existing_id", []string{"--id", "cred_1"}},
		{"reuse_unique_name", []string{"--name", "database", "--type", "opaque", "--scope", "personal"}},
		{"create_missing_name", []string{"--name", "database", "--type", "opaque", "--scope", "personal"}},
	} {
		t.Run(selection.name, func(t *testing.T) {
			mock := newConfigureHTTPMock()
			want := append([]string(nil), lifecycle...)
			if selection.name == "create_missing_name" {
				mock.managed = []credentialResource{}
				want = append([]string{want[0], "POST /v1/credentials"}, want[1:]...)
			}
			config, _ := configureTestServers(t, mock)
			payload := `{"data":{"connection_uri":"` + configureCanary + `"}}`
			output, err := runConfigureBinary(t, binary, config, payload, configureArgs(selection.args...))
			if err != nil || !strings.Contains(output, "phase=promoted credential_id=cred_1 version_id=version_1") {
				t.Fatalf("configure failed: %v\n%s", err, output)
			}
			requests := assertConfigureRoutes(t, mock, want)
			if requests[0].Query.Get("view") != "managed" || len(requests[0].Query) != 1 {
				t.Fatal("configure must resolve ownership through the managed view")
			}
			for _, request := range requests {
				switch request.Method + " " + request.Path {
				case "PUT /v1/credentials/cred_1/payload":
					if !bytes.Equal(request.Body, []byte(payload)) || len(request.Query) != 0 {
						t.Fatal("stdin must use existing staged put without activation")
					}
				case "POST /v1/credentials":
					assertCredentialCLIJSON(t, request, "POST", request.Path, map[string]any{
						"name": "database", "scope": "personal", "type": "opaque",
					})
				case "POST /v1/credentials/cred_1/grants":
					assertCredentialCLIJSON(t, request, "POST", request.Path, map[string]any{
						"grantee_id": "", "scope": map[string]any{
							"cluster": "cluster-a", "instance": "instance-a", "namespace": "apps",
							"use": "database", "workload": map[string]any{"kind": "", "name": ""},
						},
					})
				case "POST /v1/credentials/cred_1/verify":
					var body struct {
						SessionID string `json:"session_id"`
						VersionID string `json:"version_id"`
					}
					json.Unmarshal(request.Body, &body)
					if body.SessionID != "configure-test" || body.VersionID != "version_1" {
						t.Fatal("verification lost session or staged version")
					}
				}
			}
		})
	}
	t.Run("all_active_scopes_not_just_selected_target", func(t *testing.T) {
		mock := newConfigureHTTPMock()
		duplicate := mock.grant
		duplicate.ID, duplicate.GranteeID = "grant_2", "user_2"
		other := mock.grant
		other.ID, other.Scope.Cluster, other.Scope.Instance, other.Scope.Namespace = "grant_3", "cluster-b", "instance-b", "elsewhere"
		revoked := other
		revoked.ID, revoked.State, revoked.Scope.Instance = "grant_4", "revoked", "revoked"
		mock.grants = []credentialGrant{mock.grant, duplicate, other, revoked}
		config, _ := configureTestServers(t, mock)
		output, err := runConfigureBinary(t, binary, config, `{"data":{"key":"`+configureCanary+`"}}`, configureArgs("--id", "cred_1"))
		if err != nil {
			t.Fatalf("configure failed: %v\n%s", err, output)
		}
		want := append(append([]string(nil), lifecycle[:5]...), lifecycle[4:]...)
		requests := assertConfigureRoutes(t, mock, want)
		var body struct {
			Scope credentialGrantScope `json:"scope"`
		}
		json.Unmarshal(requests[5].Body, &body)
		if body.Scope.Cluster != "cluster-b" || body.Scope.Instance != "instance-b" || body.Scope.Namespace != "elsewhere" {
			t.Fatal("configure did not verify the other active target scope")
		}
	})
	for _, source := range []struct {
		name string
		args []string
		uri  string
	}{
		{"selected_keys", []string{"--keys", "username,password"}, ""},
		{"postgres_uri", []string{"--keys", "password", "--password-key", "password", "--connection-uri", "postgres://user@db:5432/app"}, "postgres://user@db:5432/app"},
		{"postgresql_uri", []string{"--keys", "password", "--password-key", "password", "--connection-uri", "postgresql://user@db/app"}, "postgresql://user@db/app"},
		{"mongodb_uri", []string{"--keys", "password", "--password-key", "password", "--connection-uri", "mongodb://user@db:27017/app"}, "mongodb://user@db:27017/app"},
	} {
		t.Run(source.name, func(t *testing.T) {
			mock := newConfigureHTTPMock()
			config, _ := configureTestServers(t, mock)
			args := configureArgs("--id", "cred_1", "--from-secret", "source-secret")
			output, err := runConfigureBinary(t, binary, config, "not JSON: "+configureCanary, append(args, source.args...))
			if err != nil {
				t.Fatalf("import failed: %v\n%s", err, output)
			}
			want := append([]string(nil), lifecycle...)
			want[1] = "POST /v1/credentials/cred_1/import-secret"
			requests := assertConfigureRoutes(t, mock, want)
			body := map[string]any{
				"session_id": "configure-test", "cluster": "cluster-a", "instance": "instance-a",
				"namespace": "apps", "secret_name": "source-secret", "keys": []any{"username", "password"},
			}
			if source.uri != "" {
				body["keys"], body["connection_uri"], body["password_key"] = []any{"password"}, source.uri, "password"
			}
			assertCredentialCLIJSON(t, requests[1], "POST", requests[1].Path, body)
		})
	}
	t.Run("import_does_not_wait_for_stdin", func(t *testing.T) {
		mock := newConfigureHTTPMock()
		config, _ := configureTestServers(t, mock)
		command := exec.Command(binary, configureArgs("--id", "cred_1", "--from-secret", "source-secret", "--keys", "connection_uri")...)
		command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
		stdin, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer stdin.Close()
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		finished := make(chan error, 1)
		go func() { finished <- command.Wait() }()
		select {
		case err := <-finished:
			if err != nil || strings.Contains(output.String(), configureCanary) {
				t.Fatalf("import failed: %v\n%s", err, output.String())
			}
		case <-time.After(10 * time.Second):
			command.Process.Kill()
			<-finished
			t.Fatal("import waited for stdin")
		}
	})
	t.Run("scope_flags_and_evidence", func(t *testing.T) {
		for _, use := range []string{"database", "imagePull", "helmPull", "opaqueSecret", "gitCheckout", "tls"} {
			t.Run(use, func(t *testing.T) {
				mock := newConfigureHTTPMock()
				scope := &mock.grant.Scope
				scope.Use, scope.Workload = use, credentialGrantWorkload{Kind: "deployment", Name: "api"}
				args := configureArgs("--id", "cred_1", "--workload-kind", "Deployment", "--workload-name", "api")
				args[9] = use
				switch use {
				case "database":
					scope.Workload = credentialGrantWorkload{}
					args = configureArgs("--id", "cred_1")
				case "imagePull":
					mock.resource.Type = "registry"
					scope.ImageRepository, scope.ImageDigest = "registry/team/api", "sha256:"+strings.Repeat("a", 64)
					args = append(args, "--image-repository", scope.ImageRepository, "--image-digest", scope.ImageDigest)
				case "helmPull":
					mock.resource.Type = "helmRepository"
					scope.HelmRelease, scope.HelmChart = "api", "https://charts.example/api.tgz"
					args = append(args, "--helm-release", scope.HelmRelease, "--helm-chart", scope.HelmChart)
				case "opaqueSecret":
					scope.RequiredKeys = []string{"password", "username"}
					args = append(args, "--required-keys", "username,password,username")
				case "gitCheckout":
					mock.resource.Type = "gitToken"
				case "tls":
					mock.resource.Type = "tls"
				}
				mock.managed, mock.grants = []credentialResource{mock.resource}, []credentialGrant{mock.grant}
				config, _ := configureTestServers(t, mock)
				output, err := runConfigureBinary(t, binary, config, `{"data":{"value":"`+configureCanary+`"}}`, args)
				if err != nil {
					t.Fatalf("configure failed: %v\n%s", err, output)
				}
				requests := assertConfigureRoutes(t, mock, lifecycle)
				var body struct {
					Scope credentialGrantScope `json:"scope"`
				}
				json.Unmarshal(requests[2].Body, &body)
				if !reflect.DeepEqual(body.Scope, *scope) {
					t.Fatalf("scope flags lost: got %+v want %+v", body.Scope, *scope)
				}
				failures := []struct {
					name, field, value string
				}{
					{"missing_status", "status", ""},
					{"failed_status", "status", "failed"},
					{"error_category", "errorCategory", configureCanary},
					{"missing_resource_version", "resourceVersion", ""},
				}
				if use == "imagePull" || use == "gitCheckout" || use == "helmPull" {
					failures = append(failures, struct{ name, field, value string }{"missing_artifact_digest", "digest", ""})
				}
				for _, failure := range failures {
					t.Run(failure.name, func(t *testing.T) {
						// Keep all other evidence valid so each required field is checked independently.
						evidence := map[string]string{
							"status": "verified", "resourceVersion": "42",
						}
						if use == "imagePull" || use == "gitCheckout" || use == "helmPull" {
							evidence["digest"] = "sha256:" + strings.Repeat("a", 64)
						}
						if failure.value == "" {
							delete(evidence, failure.field)
						} else {
							evidence[failure.field] = failure.value
						}
						raw, err := json.Marshal(map[string]any{"status": "verified", "evidence": []map[string]string{evidence}})
						if err != nil {
							t.Fatal(err)
						}
						mock.mu.Lock()
						mock.requests = nil
						mock.override[lifecycle[4]] = string(raw)
						mock.mu.Unlock()
						output, err := runConfigureBinary(t, binary, config, `{"data":{"value":"`+configureCanary+`"}}`, args)
						if err == nil || !strings.Contains(output, "verification evidence failed") || strings.Contains(output, "phase=promoted") {
							t.Fatalf("invalid evidence must stop configure: %v\n%s", err, output)
						}
						assertConfigureRoutes(t, mock, lifecycle[:5])
					})
				}
			})
		}
	})
	t.Run("fail_closed", func(t *testing.T) {
		for _, test := range []struct {
			name         string
			setup        func(*configureHTTPMock)
			count        int
			importSecret bool
		}{
			{"missing_managed_id", func(m *configureHTTPMock) { m.managed = []credentialResource{} }, 1, false},
			{"ambiguous_ownership", func(m *configureHTTPMock) { m.managed = append(m.managed, m.resource) }, 1, false},
			{"wrong_type", func(m *configureHTTPMock) { m.managed[0].Type = "registry" }, 1, false},
			{"revoked_resource", func(m *configureHTTPMock) { m.managed[0].State = "revoked" }, 1, false},
			{"missing_list_metadata", func(m *configureHTTPMock) { m.override[lifecycle[0]] = `{"payload":"` + configureCanary + `"}` }, 1, false},
			{"denied_import", func(m *configureHTTPMock) {
				key := "POST /v1/credentials/cred_1/import-secret"
				m.status[key], m.override[key] = 403, `{"message":"`+configureCanary+`"}`
			}, 2, true},
			{"audit_unavailable", func(m *configureHTTPMock) { m.status[lifecycle[1]], m.override[lifecycle[1]] = 500, configureCanary }, 2, false},
			{"key_unavailable", func(m *configureHTTPMock) { m.status[lifecycle[1]], m.override[lifecycle[1]] = 503, configureCanary }, 2, false},
			{"wrong_staged_resource", func(m *configureHTTPMock) {
				m.override[lifecycle[1]] = `{"id":"version_1","credential_id":"other","state":"staged"}`
			}, 2, false},
			{"not_staged", func(m *configureHTTPMock) {
				m.override[lifecycle[1]] = `{"id":"version_1","credential_id":"cred_1","state":"active"}`
			}, 2, false},
			{"invalid_staging_response", func(m *configureHTTPMock) { m.override[lifecycle[1]] = configureCanary }, 2, false},
			{"grant_denied", func(m *configureHTTPMock) { m.status[lifecycle[2]], m.override[lifecycle[2]] = 403, configureCanary }, 3, false},
			{"wrong_target_grant", func(m *configureHTTPMock) { m.grant.Scope.Instance = "wrong-target" }, 3, false},
			{"inactive_grant", func(m *configureHTTPMock) { m.grant.State = "revoked" }, 3, false},
			{"grants_denied", func(m *configureHTTPMock) { m.status[lifecycle[3]], m.override[lifecycle[3]] = 403, configureCanary }, 4, false},
			{"missing_self_grant", func(m *configureHTTPMock) { m.grants = nil }, 4, false},
			{"revoked_self_grant", func(m *configureHTTPMock) { m.grants[0].State = "revoked" }, 4, false},
			{"wrong_resource_grant", func(m *configureHTTPMock) { m.grants[0].CredentialID = "other" }, 4, false},
			{"verify_failure_http_200", func(m *configureHTTPMock) { m.failVerify = 1 }, 5, false},
			{"verify_denied", func(m *configureHTTPMock) { m.status[lifecycle[4]], m.override[lifecycle[4]] = 403, configureCanary }, 5, false},
			{"missing_verify_status", func(m *configureHTTPMock) { m.override[lifecycle[4]] = `{}` }, 5, false},
			{"missing_evidence", func(m *configureHTTPMock) { m.override[lifecycle[4]] = `{"status":"verified","evidence":[]}` }, 5, false},
			{"failed_evidence", func(m *configureHTTPMock) {
				m.override[lifecycle[4]] = `{"status":"verified","evidence":[{"status":"failed","errorCategory":"` + configureCanary + `"}]}`
			}, 5, false},
			{"incomplete_evidence", func(m *configureHTTPMock) {
				m.override[lifecycle[4]] = `{"status":"verified","evidence":[{"status":"verified"}]}`
			}, 5, false},
			{"promotion_false", func(m *configureHTTPMock) {
				m.override[lifecycle[5]] = `{"credential_id":"cred_1","version_id":"version_1","promoted":false}`
			}, 6, false},
			{"wrong_promoted_version", func(m *configureHTTPMock) {
				m.override[lifecycle[5]] = `{"credential_id":"cred_1","version_id":"other","promoted":true}`
			}, 6, false},
			{"promotion_denied", func(m *configureHTTPMock) { m.status[lifecycle[5]], m.override[lifecycle[5]] = 403, configureCanary }, 6, false},
		} {
			t.Run(test.name, func(t *testing.T) {
				mock := newConfigureHTTPMock()
				test.setup(mock)
				config, _ := configureTestServers(t, mock)
				args := configureArgs("--id", "cred_1")
				want := append([]string(nil), lifecycle[:test.count]...)
				if test.importSecret {
					args = append(args, "--from-secret", "source", "--keys", "connection_uri")
					want[1] = "POST /v1/credentials/cred_1/import-secret"
				}
				output, err := runConfigureBinary(t, binary, config, `{"data":{"key":"`+configureCanary+`"}}`, args)
				if err == nil || strings.Contains(output, "phase=promoted") {
					t.Fatalf("failure must stop configure: %v\n%s", err, output)
				}
				assertConfigureRoutes(t, mock, want)
			})
		}
	})
	t.Run("second_scope_failure_never_promotes", func(t *testing.T) {
		mock := newConfigureHTTPMock()
		other := mock.grant
		other.ID, other.Scope.Instance = "grant_2", "instance-b"
		mock.grants, mock.failVerify = append(mock.grants, other), 2
		config, _ := configureTestServers(t, mock)
		output, err := runConfigureBinary(t, binary, config, `{}`, configureArgs("--id", "cred_1"))
		if err == nil || strings.Contains(output, "phase=promoted") {
			t.Fatalf("second scope failure did not stop: %v\n%s", err, output)
		}
		assertConfigureRoutes(t, mock, append(append([]string(nil), lifecycle[:5]...), lifecycle[4]))
	})
	t.Run("invalid_arguments_no_network", func(t *testing.T) {
		base := configureArgs("--id", "cred_1")
		for _, test := range []struct {
			name  string
			args  []string
			stdin string
		}{
			{"missing_session", base[2:], `{}`},
			{"unknown_target", append(append([]string(nil), base...), "--target", "missing"), `{}`},
			{"missing_target", append(append([]string(nil), base...), "--target", ""), `{}`},
			{"missing_namespace", append(append([]string(nil), base...), "--namespace", ""), `{}`},
			{"missing_identity", base[:10], `{}`},
			{"conflicting_identity", append(append([]string(nil), base...), "--name", "database", "--type", "opaque", "--scope", "personal"), `{}`},
			{"unsafe_id", configureArgs("--id", "../other"), `{}`},
			{"incomplete_name", configureArgs("--name", "database"), `{}`},
			{"wrong_type", configureArgs("--name", "database", "--scope", "personal", "--type", "registry"), `{}`},
			{"invalid_scope", configureArgs("--name", "database", "--scope", "*", "--type", "opaque"), `{}`},
			{"unknown_use", append(append([]string(nil), base...), "--use", "unknown"), `{}`},
			{"duplicate_route_flag", append(append([]string(nil), base...), "--cluster", "other"), `{}`},
			{"token_flag", append(append([]string(nil), base...), "--token", configureCanary), `{}`},
			{"grantee_flag", append(append([]string(nil), base...), "--grantee-id", "other"), `{}`},
			{"database_asset_flag", append(append([]string(nil), base...), "--database-asset-id", "asset"), `{}`},
			{"positional", append(append([]string(nil), base...), "unexpected"), `{}`},
			{"bad_json", base, configureCanary},
			{"missing_keys", append(append([]string(nil), base...), "--from-secret", "source"), `{}`},
			{"empty_key", append(append([]string(nil), base...), "--from-secret", "source", "--keys", "a,,b"), `{}`},
			{"duplicate_key", append(append([]string(nil), base...), "--from-secret", "source", "--keys", "a,a"), `{}`},
			{"keys_without_secret", append(append([]string(nil), base...), "--keys", "password"), `{}`},
			{"password_without_uri", append(append([]string(nil), base...), "--from-secret", "source", "--keys", "password", "--password-key", "password"), `{}`},
		} {
			t.Run(test.name, func(t *testing.T) {
				mock := newConfigureHTTPMock()
				config, _ := configureTestServers(t, mock)
				output, err := runConfigureBinary(t, binary, config, test.stdin, test.args)
				if err == nil {
					t.Fatalf("invalid arguments accepted: %s", output)
				}
				assertConfigureRoutes(t, mock, []string{})
			})
		}
		for _, uri := range []string{
			"postgres://user:" + configureCanary + "@db/app", "postgres://user:@db/app",
			"mongodb://user:" + configureCanary + "@db/app", "postgres://user@db/app?password=" + configureCanary,
			"postgres://user@db/app?%70assword=" + configureCanary, "postgres://user@db/app?sslpassword=" + configureCanary,
			"postgres://user@db/app?token=" + configureCanary, "mysql://user@db/app",
			"postgres://user@/app", "postgres://user@db/app#" + configureCanary,
			"postgres://user@db/app?password=%zz", "postgres://user:%zz@db/app",
			"postgres://user@db", "postgres://user@db/",
			"postgresql://user@db", "postgresql://user@db/",
			"mongodb://user@db", "mongodb://user@db/",
			"postgres://user@db/app?sslmode=require", "postgresql://user@db/app?sslmode=require",
			"mongodb://user@db/app?authSource=admin",
			"postgres://user@db/app?", "postgresql://user@db/app?", "mongodb://user@db/app?",
			"postgresql://user@db/app#fragment", "mongodb://user@db/app#fragment",
		} {
			t.Run("unsafe_uri", func(t *testing.T) {
				mock := newConfigureHTTPMock()
				config, _ := configureTestServers(t, mock)
				args := append(append([]string(nil), base...), "--from-secret", "source", "--keys", "password",
					"--password-key", "password", "--connection-uri", uri)
				output, err := runConfigureBinary(t, binary, config, "", args)
				if err == nil {
					t.Fatalf("unsafe URI accepted: %s", output)
				}
				assertConfigureRoutes(t, mock, []string{})
			})
		}
	})
	t.Run("help", func(t *testing.T) {
		mock := newConfigureHTTPMock()
		config, _ := configureTestServers(t, mock)
		output, err := runConfigureBinary(t, binary, config, "", []string{"credential", "configure", "-h"})
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"-session", "mutually exclusive", "never reads stdin", "--password-key", "Database and Helm grants require no workload", "every unique active target/use authorization scope",
			"concrete database path", "no query (including an empty ?) or fragment"} {
			if !strings.Contains(output, text) {
				t.Errorf("help missing %q", text)
			}
		}
		assertConfigureRoutes(t, mock, []string{})
	})
}

func TestParseConfigureCredentialKeyMap(t *testing.T) {
	got, err := parseConfigureCredentialKeyMap("SOURCE_URL=SPRING_DATASOURCE_URL,SOURCE_USER=SPRING_DATASOURCE_USERNAME")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SOURCE_URL":  "SPRING_DATASOURCE_URL",
		"SOURCE_USER": "SPRING_DATASOURCE_USERNAME",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("key map = %#v, want %#v", got, want)
	}
	for _, raw := range []string{
		"SOURCE_URL",
		"SOURCE_URL=SPRING_DATASOURCE_URL,SOURCE_URL=OTHER",
		"SOURCE_URL=SPRING_DATASOURCE_URL,OTHER=SPRING_DATASOURCE_URL",
	} {
		if _, err := parseConfigureCredentialKeyMap(raw); err == nil {
			t.Fatalf("invalid key map accepted: %q", raw)
		}
	}
}

func TestCredentialCommandsDoNotRequireWorkloadBeforeNetwork(t *testing.T) {
	for _, command := range []string{"grant", "verify"} {
		for _, use := range []string{"imagePull", "gitCheckout", "tls", "opaqueSecret"} {
			args := []string{command, "--target", "db", "--id", "cred_1", "--cluster", "a", "--instance", "b",
				"--namespace", "apps", "--use", use}
			if command == "verify" {
				args = append(args, "--version-id", "version_1")
			}
			var output bytes.Buffer
			err := runCredentialCommand(args, "test", nil, nil, strings.NewReader(""), &output)
			if err == nil || strings.Contains(err.Error(), "--workload-kind and --workload-name") {
				t.Fatalf("%s %s still applies a workload authorization gate: %v", command, use, err)
			}
		}
	}
}

func TestConfigureCredentialScopeKeyIgnoresHelmRuntimeScope(t *testing.T) {
	oldScope := credentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: "helmPull",
		Workload:    credentialGrantWorkload{Kind: "Deployment", Name: "frontend"},
		HelmRelease: "zhiyong",
		HelmChart:   "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gold.tgz",
	}
	newScope := oldScope
	newScope.Namespace = "other"
	newScope.Workload = credentialGrantWorkload{Kind: "StatefulSet", Name: "other"}
	newScope.HelmRelease = "other"
	newScope.HelmChart = "https://other.example/charts/other/v/1.0.0.tgz"
	if configureCredentialScopeKey(oldScope) != configureCredentialScopeKey(newScope) {
		t.Fatal("configure did not treat same Chart path with a new version as one scope")
	}
}
