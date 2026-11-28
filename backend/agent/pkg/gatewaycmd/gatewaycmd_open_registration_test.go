package gatewaycmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/internal/server"
)

func TestEnvFlagEnabledSpellings(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on", " on "} {
		t.Setenv("DOOPS_TEST_FLAG", value)
		if !envFlagEnabled("DOOPS_TEST_FLAG") {
			t.Fatalf("%q was not accepted as enabled", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no", "off", "enabled"} {
		t.Setenv("DOOPS_TEST_FLAG", value)
		if envFlagEnabled("DOOPS_TEST_FLAG") {
			t.Fatalf("%q was accepted as enabled", value)
		}
	}
}

func TestResolveGatewayEnrollmentPolicy(t *testing.T) {
	envMap := func(values map[string]string) func(string) string {
		return func(k string) string { return values[k] }
	}

	t.Run("default opens registration and credential delivery", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(envMap(nil), nil, false, true, true, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.RequireAgentEnrollment || !p.AllowOpenAgentRegistration || !p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("default policy = %+v, want require=false allowOpen=true allowUnenrolledCreds=true", p)
		}
	})

	t.Run("DOOPS_REQUIRE_AGENT_ENROLLMENT=1 enables strict mode", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_REQUIRE_AGENT_ENROLLMENT": "1"}),
			nil, false, true, true, false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.RequireAgentEnrollment || p.AllowOpenAgentRegistration || p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("strict env policy = %+v, want require=true allowOpen=false allowUnenrolledCreds=false", p)
		}
	})

	t.Run("DOOPS_REQUIRE_AGENT_ENROLLMENT=1 overrides legacy open env and ignores unenrolled cred env", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{
				"DOOPS_REQUIRE_AGENT_ENROLLMENT":             "true",
				"DOOPS_ALLOW_OPEN_AGENT_REGISTRATION":        "1",
				"DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY": "1",
			}),
			nil, false, true, true, false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.RequireAgentEnrollment || p.AllowOpenAgentRegistration || p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("precedence policy = %+v", p)
		}
		if p.ConflictWarning == "" {
			t.Fatal("expected ConflictWarning when DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY is ignored")
		}
	})

	t.Run("legacy DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=0 enables strict mode", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_ALLOW_OPEN_AGENT_REGISTRATION": "0"}),
			nil, false, true, true, false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.RequireAgentEnrollment || p.AllowOpenAgentRegistration || p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("legacy closed policy = %+v", p)
		}
	})

	t.Run("explicit -require-agent-enrollment flag overrides open env", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_REQUIRE_AGENT_ENROLLMENT": "0"}),
			map[string]bool{"require-agent-enrollment": true},
			true, true, true, false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !p.RequireAgentEnrollment || p.AllowOpenAgentRegistration || p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("flag strict policy = %+v", p)
		}
	})

	t.Run("open registration with DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1 restricts credentials only", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY": "1"}),
			nil, false, true, true, false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.RequireAgentEnrollment || !p.AllowOpenAgentRegistration || p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("hybrid policy = %+v, want require=false allowOpen=true allowUnenrolledCreds=false", p)
		}
	})

	t.Run("open registration with legacy DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=0 restricts credentials only", func(t *testing.T) {
		p, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY": "0"}),
			nil, false, true, true, false,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.RequireAgentEnrollment || !p.AllowOpenAgentRegistration || p.AllowUnenrolledCredentialDelivery {
			t.Fatalf("legacy cred restricted policy = %+v", p)
		}
	})

	t.Run("invalid strict env fails closed", func(t *testing.T) {
		if _, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_REQUIRE_AGENT_ENROLLMENT": "maybe"}),
			nil, false, true, true, false,
		); err == nil {
			t.Fatal("expected error for invalid DOOPS_REQUIRE_AGENT_ENROLLMENT")
		}
		if _, err := resolveGatewayEnrollmentPolicy(
			envMap(map[string]string{"DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY": "maybe"}),
			nil, false, true, true, false,
		); err == nil {
			t.Fatal("expected error for invalid DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY")
		}
	})

	t.Run("conflicting CLI flags fail closed", func(t *testing.T) {
		if _, err := resolveGatewayEnrollmentPolicy(
			envMap(nil),
			map[string]bool{"require-agent-enrollment": true, "allow-open-agent-registration": true},
			true, true, true, false,
		); err == nil {
			t.Fatal("expected error for conflicting registration flags")
		}
		if _, err := resolveGatewayEnrollmentPolicy(
			envMap(nil),
			map[string]bool{"require-agent-enrollment": true, "allow-unenrolled-credential-delivery": true},
			true, true, true, false,
		); err == nil {
			t.Fatal("expected error for combining -require-agent-enrollment with -allow-unenrolled-credential-delivery")
		}
		if _, err := resolveGatewayEnrollmentPolicy(
			envMap(nil),
			map[string]bool{"require-enrolled-credential-delivery": true, "allow-unenrolled-credential-delivery": true},
			false, true, true, true,
		); err == nil {
			t.Fatal("expected error for conflicting credential delivery flags")
		}
	})
}

func assertAgentEnrollmentRefused(t *testing.T, url string, header http.Header) {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatalf("expected registration at %s to be refused", url)
	}
	if resp == nil {
		t.Fatalf("expected HTTP 401 refusal for %s, got %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	var payload map[string]interface{}
	if err := json.Unmarshal(body.Bytes(), &payload); err != nil {
		t.Fatalf("decode refusal %q: %v", body.String(), err)
	}
	if payload["code"] != "agent_enrollment_auth_failed" {
		t.Fatalf("refusal code = %v, want agent_enrollment_auth_failed (body=%s)", payload["code"], body.String())
	}
}

func TestGatewayServeOpenRegistrationFromEnvironment(t *testing.T) {
	t.Run("default allows unenrolled new target and protects confirmed target", func(t *testing.T) {
		t.Setenv("DOOPS_REQUIRE_AGENT_ENROLLMENT", "")
		t.Setenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION", "")
		t.Setenv("DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY", "")
		t.Setenv("DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY", "")

		port := freeGatewayLockTestPort(t)
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		store, err := server.OpenGatewayStore(dbPath)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		locked, err := store.CreateAgentEnrollment("prod", "locked")
		if err != nil {
			_ = store.Close()
			t.Fatalf("create enrollment: %v", err)
		}
		if err := store.ConfirmAgentEnrollment(locked.ID); err != nil {
			_ = store.Close()
			t.Fatalf("confirm enrollment: %v", err)
		}
		_ = store.Close()

		child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
		waitGatewayLockTestHealth(t, child, port)

		// Unenrolled Agent on a fresh target is admitted by default.
		openURL := fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=open", port)
		conn, _, err := websocket.DefaultDialer.Dial(openURL, nil)
		if err != nil {
			t.Fatalf("default open registration rejected an unenrolled Agent: %v\n%s", err, child.output.String())
		}
		_ = conn.Close()

		// Presenting an invalid Bearer token never falls back to open registration.
		assertAgentEnrollmentRefused(t, openURL, http.Header{"Authorization": []string{"Bearer invalid-canary-token"}})

		// A target with a confirmed enrollment still refuses tokenless connections.
		lockedURL := fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=prod&instance=locked", port)
		assertAgentEnrollmentRefused(t, lockedURL, nil)

		// And accepts its valid enrollment token.
		conn, _, err = websocket.DefaultDialer.Dial(lockedURL, http.Header{"Authorization": []string{"Bearer " + locked.Plaintext}})
		if err != nil {
			t.Fatalf("confirmed target refused its valid enrollment token: %v\n%s", err, child.output.String())
		}
		_ = conn.Close()
		if strings.Contains(child.output.String(), locked.Plaintext) {
			t.Fatal("enrollment token leaked into gateway log output")
		}
	})

	t.Run("DOOPS_REQUIRE_AGENT_ENROLLMENT=1 rejects unenrolled Agent", func(t *testing.T) {
		t.Setenv("DOOPS_REQUIRE_AGENT_ENROLLMENT", "1")
		t.Setenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION", "")
		port := freeGatewayLockTestPort(t)
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		store, err := server.OpenGatewayStore(dbPath)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		enrolled, err := store.CreateAgentEnrollment("dev", "local")
		if err != nil {
			_ = store.Close()
			t.Fatalf("create enrollment: %v", err)
		}
		_ = store.Close()

		child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
		waitGatewayLockTestHealth(t, child, port)

		targetURL := fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port)
		assertAgentEnrollmentRefused(t, targetURL, nil)

		conn, _, err := websocket.DefaultDialer.Dial(targetURL, http.Header{"Authorization": []string{"Bearer " + enrolled.Plaintext}})
		if err != nil {
			t.Fatalf("strict mode refused a valid enrollment token: %v\n%s", err, child.output.String())
		}
		_ = conn.Close()
	})

	t.Run("-require-agent-enrollment flag rejects unenrolled Agent", func(t *testing.T) {
		t.Setenv("DOOPS_REQUIRE_AGENT_ENROLLMENT", "")
		t.Setenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION", "")
		port := freeGatewayLockTestPort(t)
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		child := startGatewayLockHelper(t, append(gatewayServeTestArgs(dbPath, port), "-require-agent-enrollment")...)
		waitGatewayLockTestHealth(t, child, port)

		targetURL := fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port)
		assertAgentEnrollmentRefused(t, targetURL, nil)
	})

	t.Run("legacy DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=0 rejects unenrolled Agent", func(t *testing.T) {
		t.Setenv("DOOPS_REQUIRE_AGENT_ENROLLMENT", "")
		t.Setenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION", "0")
		port := freeGatewayLockTestPort(t)
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		child := startGatewayLockHelper(t, gatewayServeTestArgs(dbPath, port)...)
		waitGatewayLockTestHealth(t, child, port)

		targetURL := fmt.Sprintf("ws://127.0.0.1:%d/v1/agent/connect?cluster=dev&instance=local", port)
		assertAgentEnrollmentRefused(t, targetURL, nil)
	})
}
