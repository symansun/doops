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

// Given an imported service token, a password login replaces the local record
// with a new, identified credential. Failed imports do not replace it, and a
// local logout must not silently revoke either token at the Gateway.
func TestCLILoginKeepsCredentialIdentityAndLifecycleExplicit(t *testing.T) {
	const (
		aliceToken = "synthetic-alice-service-token"
		bobToken   = "synthetic-bob-login-token"
		revoked    = "synthetic-revoked-token"
		password   = "synthetic-bob-password"
		issuedAt   = "2026-11-08T08:39:00Z"
		bobExpiry  = "2026-11-09T08:39:00Z"
	)
	var requests, logins, inspections atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/auth/me":
			inspections.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+aliceToken {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"gateway_auth_revoked","auth_domain":"gateway","message":"Gateway user token was revoked"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(gatewayLoginResponse{
				TokenID: "tok_alice", Username: "alice", Name: "automation",
				IssuedAt: issuedAt, ExpiresAt: "2028-07-16T08:39:00Z",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/login":
			logins.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
				body["username"] != "bob" || body["password"] != password || body["name"] != "doops login" {
				t.Errorf("password login request lost its explicit account or label")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(gatewayLoginResponse{
				Token: bobToken, TokenID: "tok_bob", Username: "bob", Name: "doops login",
				IssuedAt: issuedAt, ExpiresAt: bobExpiry,
			})
		default:
			t.Errorf("unexpected Gateway request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gateway.Close()

	binary := buildDoopsCLIBinary(t)
	root := t.TempDir()
	config := writeAuthLifecycleConfig(t, root, gateway.URL)
	run := func(input string, args ...string) (string, error) {
		t.Helper()
		command := exec.Command(binary, append([]string{"-session", "auth-lifecycle"}, args...)...)
		command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
		command.Stdin = strings.NewReader(input)
		output, err := command.CombinedOutput()
		for _, secret := range []string{aliceToken, bobToken, revoked, password} {
			if strings.Contains(string(output), secret) {
				t.Fatal("authentication output exposed a credential payload")
			}
		}
		return string(output), err
	}
	readStore := func() (AuthStore, []byte) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		var store AuthStore
		if err := json.Unmarshal(data, &store); err != nil {
			t.Fatal(err)
		}
		return store, data
	}

	output, err := run(aliceToken, "login", "--target", "auth", "--token-stdin")
	if err != nil {
		t.Fatalf("import Alice service token: %v\n%s", err, output)
	}
	store, _ := readStore()
	alice := store.GetGatewayRecord(gateway.URL)
	if alice.Token != aliceToken || alice.TokenID != "tok_alice" || alice.Username != "alice" ||
		alice.Name != "automation" || alice.IssuedAt != issuedAt || alice.ExpiresAt != "2028-07-16T08:39:00Z" ||
		alice.Source != "token-stdin" || inspections.Load() != 1 || logins.Load() != 0 {
		t.Fatal("token import did not preserve the Gateway credential metadata")
	}
	output, err = runPasswordLoginInTerminal(t, binary, config, password, "login", "--target", "auth", "--username", "bob")
	if err != nil {
		t.Fatalf("password login Bob: %v\n%s", err, output)
	}
	for _, secret := range []string{aliceToken, bobToken, password} {
		if strings.Contains(output, secret) {
			t.Fatal("interactive login exposed a credential payload")
		}
	}
	for _, want := range []string{"replaced_token_id=tok_alice", "replaced_username=alice", "cached_token_id=tok_bob", "username=bob", "expires_at=" + bobExpiry} {
		if !strings.Contains(output, want) {
			t.Fatalf("replacement output omitted %q: %s", want, output)
		}
	}
	store, beforeFailure := readStore()
	bob := store.GetGatewayRecord(gateway.URL)
	if bob.Token != bobToken || bob.TokenID != "tok_bob" || bob.Username != "bob" || bob.Name != "doops login" ||
		bob.Source != "password-login" || bob.ExpiresAt != bobExpiry || bob.IssuedAt != issuedAt || logins.Load() != 1 {
		t.Fatal("password login did not persist Bob's new credential identity")
	}
	output, err = run(revoked, "login", "--target", "auth", "--token-stdin")
	if err == nil || !strings.Contains(output, "gateway_auth_revoked") {
		t.Fatalf("revoked token import did not preserve the rejection: %v\n%s", err, output)
	}
	_, afterFailure := readStore()
	if !bytes.Equal(beforeFailure, afterFailure) || logins.Load() != 1 {
		t.Fatal("failed token import changed the cached credential or issued a new token")
	}
	beforeLogout := requests.Load()
	output, err = run("", "logout", "--target", "auth")
	if err != nil || !strings.Contains(output, "token was not revoked") {
		t.Fatalf("local logout did not state its boundary: %v\n%s", err, output)
	}
	store, _ = readStore()
	if len(store.Credentials) != 0 || requests.Load() != beforeLogout {
		t.Fatal("logout retained the local credential or contacted the Gateway")
	}
	output, err = run(aliceToken, "login", "--target", "auth", "--token-stdin")
	if err != nil {
		t.Fatalf("import token after local logout: %v\n%s", err, output)
	}
	store, _ = readStore()
	if imported := store.GetGatewayRecord(gateway.URL); imported.Source != "token-stdin" || imported.Username != "alice" || inspections.Load() != 3 {
		t.Fatal("token reimport bypassed Gateway validation or lost its source")
	}
}

func TestCLILoginRejectsUnknownAuthSchemaBeforeIssuingToken(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gateway.Close()
	binary := buildDoopsCLIBinary(t)
	for _, schema := range []string{
		`{"version":99,"credentials":{}}`,
		`{"version":2,"credentials":{},"tokens":{}}`,
		`{"version":2,"credentials":{"https://gw.example":{"token":""}}}`,
		`{"version":2,"credentials":{"https://gw.example":{"token":"canary"},"https://gw.example:443/":{"token":"other"}}}`,
		`{"tokens":{"https://gw.example":""}}`,
		`{"version":2,"credentials":{},"unexpected":true}`,
		`{"version":2,"credentials":{"https://gw.example":{"token":"canary","unexpected":true}}}`,
	} {
		t.Run(schema, func(t *testing.T) {
			root := t.TempDir()
			config := writeAuthLifecycleConfig(t, root, gateway.URL)
			path := filepath.Join(root, "auth.json")
			if err := os.WriteFile(path, []byte(schema), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, "login", "--target", "auth", "--username", "bob")
			command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "parse Gateway auth store") {
				t.Fatalf("invalid auth schema did not block login: %v\n%s", err, output)
			}
			current, err := os.ReadFile(path)
			if err != nil || string(current) != schema || requests.Load() != 0 {
				t.Fatal("invalid cache was overwritten or login issued an orphan token")
			}
		})
	}
}

func TestCLILoginRejectsCredentialArgumentsBeforeHTTPOrCacheWrite(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gateway.Close()
	binary := buildDoopsCLIBinary(t)
	root := t.TempDir()
	config := writeAuthLifecycleConfig(t, root, gateway.URL)
	path := filepath.Join(root, "auth.json")
	before, err := json.Marshal(AuthStore{
		Version: authStoreVersion,
		Credentials: map[string]GatewayAuth{
			gateway.URL: {Token: "existing-synthetic-token", TokenID: "tok_existing", Username: "alice"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	const canary = "rejected-argv-secret-canary"
	for _, args := range [][]string{
		{"--token", canary}, {"--token=" + canary},
		{"--password", canary}, {"--password=" + canary},
	} {
		command := exec.Command(binary, append([]string{"login", "--target", "auth"}, args...)...)
		command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "flag provided but not defined") {
			t.Fatalf("secret argv flag was not rejected: %v\n%s", err, output)
		}
		if strings.Contains(string(output), canary) {
			t.Fatal("rejected credential argument was echoed to output")
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, current) || requests.Load() != 0 {
			t.Fatal("rejected credential argument reached HTTP or changed the cached token")
		}
	}
}

// Drive the existing hidden password prompt through a real PTY. The password
// enters this helper over stdin and reaches the CLI only after terminal echo
// is disabled; it is never part of either process's argv.
func runPasswordLoginInTerminal(t *testing.T, binary, config, password string, args ...string) (string, error) {
	t.Helper()
	const driver = `import os, pty, select, subprocess, sys, termios, time
password = sys.stdin.buffer.read()
master, slave = pty.openpty()
child = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=slave)
output = bytearray()
sent = False
deadline = time.monotonic() + 30
try:
    while time.monotonic() < deadline:
        if not sent and not termios.tcgetattr(slave)[3] & termios.ECHO:
            os.write(master, password + b"\n")
            sent = True
        if select.select([master], [], [], 0.05)[0]:
            output.extend(os.read(master, 65536))
        if child.poll() is not None:
            while select.select([master], [], [], 0)[0]:
                output.extend(os.read(master, 65536))
            break
    else:
        child.kill()
        child.wait()
        output.extend(b"interactive login exceeded its test deadline")
        sys.stdout.buffer.write(output)
        sys.exit(124)
finally:
    os.close(master)
    os.close(slave)
sys.stdout.buffer.write(output)
sys.exit(child.wait())
`
	command := exec.Command("python3", append([]string{"-c", driver, binary}, args...)...)
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+config)
	command.Stdin = strings.NewReader(password)
	output, err := command.CombinedOutput()
	return string(output), err
}

func writeAuthLifecycleConfig(t *testing.T, root, gateway string) string {
	t.Helper()
	config := filepath.Join(root, "config.json")
	data, err := json.Marshal(RootConfig{Servers: []Server{{Name: "auth", Gateway: gateway, Cluster: "test", Instance: "node"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return config
}
