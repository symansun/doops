package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeErrorHintConfig(t *testing.T, loggedIn bool, servers ...Server) string {
	t.Helper()
	root := t.TempDir()
	config, err := json.Marshal(RootConfig{Servers: servers})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	auth := AuthStore{Version: authStoreVersion, Credentials: map[string]GatewayAuth{}}
	if loggedIn {
		for _, server := range servers {
			auth.SetGateway(server.Gateway, "synthetic-session-token")
		}
	}
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "auth.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func runErrorHintCLI(t *testing.T, binary, configPath string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "HOME="+filepath.Dir(configPath), "DOOPS_CONFIG="+configPath)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("CLI did not reach a terminal failure: %v\n%s", ctx.Err(), out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected CLI failure with exit code 1, got %v\n%s", err, out)
	}
	if strings.Contains(string(out), "synthetic-session-token") {
		t.Fatalf("CLI exposed the stored Gateway token: %s", out)
	}
	return string(out)
}

func TestIntegrationServerNotFoundHint(t *testing.T) {
	config := writeErrorHintConfig(t, false)
	out := runErrorHintCLI(t, buildDoopsCLIBinary(t), config,
		"-session", "hint-test", "exec", "--target", "ghost", "--cmd", "printf ok")
	for _, want := range []string{`Error: target "ghost" is not configured`, "next: doops add --name"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing target should name the failure and how to configure it (%q):\n%s", want, out)
		}
	}
	if strings.Contains(out, "离线") {
		t.Fatalf("an unconfigured target must not be diagnosed as an offline Agent:\n%s", out)
	}
}

func TestIntegration401Hint(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/rpc" || r.URL.Query().Get("cluster") != "c" || r.URL.Query().Get("instance") != "i" || r.Header.Get("Authorization") != "Bearer synthetic-session-token" {
			t.Errorf("CLI did not use the configured Gateway route and shared login: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"gateway_auth_failed","auth_domain":"gateway","message":"synthetic token rejected"}`))
	}))
	defer gateway.Close()
	config := writeErrorHintConfig(t, true, Server{Name: "demo", Gateway: gateway.URL, Cluster: "c", Instance: "i", Use: "ops"})
	out := runErrorHintCLI(t, buildDoopsCLIBinary(t), config,
		"-session", "hint-test", "exec", "--target", "demo", "--cmd", "printf ok")
	for _, want := range []string{"gateway_auth_failed", "Hint:", "doops login --target"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Gateway 401 should preserve its category and recommend login (%q):\n%s", want, out)
		}
	}
	if requests.Load() != 1 || strings.Contains(out, "网络可达性") {
		t.Fatalf("Gateway authentication must not be retried or misclassified as a network failure (%d requests):\n%s", requests.Load(), out)
	}
}

func TestIntegrationGatewayOnlyGuardPlacement(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer gateway.Close()
	config := writeErrorHintConfig(t, true, Server{Name: "demo", Gateway: gateway.URL, Cluster: "c", Instance: "i", Use: "ops"})
	binary := buildDoopsCLIBinary(t)
	execOut := runErrorHintCLI(t, binary, config,
		"-session", "hint-test", "exec", "--target", "demo", "--cmd", "printf ok")
	if requests.Load() != 1 || !strings.Contains(execOut, "http_401") {
		t.Fatalf("a Gateway-only target's exec must reach the remote connection boundary:\n%s", execOut)
	}
	bashOut := runErrorHintCLI(t, binary, config, "bash", "--target", "demo")
	if !strings.Contains(bashOut, `unknown command "bash"`) || !strings.Contains(bashOut, "doops ssh -h") {
		t.Fatalf("the retired bash command should point to the supported SSH command:\n%s", bashOut)
	}
	if requests.Load() != 1 {
		t.Fatalf("an unknown command contacted the Gateway (%d requests)", requests.Load())
	}
}

func TestIntegrationConnRefusedHint(t *testing.T) {
	gateway := httptest.NewServer(http.NotFoundHandler())
	gateway.Close()
	config := writeErrorHintConfig(t, true, Server{Name: "demo", Gateway: gateway.URL, Cluster: "c", Instance: "i", Use: "ops"})
	out := runErrorHintCLI(t, buildDoopsCLIBinary(t), config,
		"-session", "hint-test", "exec", "--target", "demo", "--cmd", "printf ok")
	for _, want := range []string{"connection refused", "Hint:", "gateway 地址", "doops targets --target"} {
		if !strings.Contains(out, want) {
			t.Fatalf("connection failure should preserve the network error and give the next checks (%q):\n%s", want, out)
		}
	}
}

func TestIntegrationMissingLoginDoesNotContactGateway(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer gateway.Close()
	config := writeErrorHintConfig(t, false, Server{Name: "demo", Gateway: gateway.URL, Cluster: "c", Instance: "i", Use: "ops"})
	out := runErrorHintCLI(t, buildDoopsCLIBinary(t), config,
		"-session", "hint-test", "exec", "--target", "demo", "--cmd", "printf ok")
	if !strings.Contains(out, "no login for Gateway") || !strings.Contains(out, "doops login --target demo") {
		t.Fatalf("missing Gateway login should point to the configured target:\n%s", out)
	}
	if requests.Load() != 0 {
		t.Fatalf("CLI attempted %d unauthenticated Gateway requests", requests.Load())
	}
}
