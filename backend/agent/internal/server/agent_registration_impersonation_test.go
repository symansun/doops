package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// syncLogBuffer collects log lines written from several goroutines.
type syncLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// captureGatewayLogs captures the process log for the rest of the test.
func captureGatewayLogs(t *testing.T) *syncLogBuffer {
	t.Helper()
	logs := &syncLogBuffer{}
	old := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(old) })
	return logs
}

// impersonationFixture is a Gateway served over HTTP with a real Agent
// registration endpoint, so a test can prove that a refused Agent never gets a
// WebSocket upgrade.
type impersonationFixture struct {
	*executionLifecycleFixture
	server  *httptest.Server
	canary  string
	enroll  CreatedAgentEnrollment
	blocked []CreatedAgentEnrollment
}

func newImpersonationFixture(t *testing.T, allowOpen bool) *impersonationFixture {
	t.Helper()
	// Synthetic master key only; no real credential material is involved.
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("s", 32))
	f := &impersonationFixture{
		executionLifecycleFixture: newExecutionLifecycleFixture(t, GatewayHubOptions{AllowOpenAgentRegistration: allowOpen, RequireAgentEnrollment: !allowOpen}),
		canary:                    "canary-s01-" + randomHex(12),
	}
	mux := http.NewServeMux()
	f.hub.RegisterRoutes(mux)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	var err error
	f.enroll, err = f.store.CreateAgentEnrollment("dev", "local")
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	// A second target carries its own confirmed enrollment and a credential
	// bound to it, so an impersonated session on dev/local must never reach it.
	other, err := f.store.CreateAgentEnrollment("dev", "other")
	if err != nil {
		t.Fatalf("create second enrollment: %v", err)
	}
	if err := f.store.ConfirmAgentEnrollment(other.ID); err != nil {
		t.Fatalf("confirm second enrollment: %v", err)
	}
	f.blocked = []CreatedAgentEnrollment{other}
	return f
}

func (f *impersonationFixture) agentURL() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http") + "/v1/agent/connect?cluster=dev&instance=local"
}

func (f *impersonationFixture) otherURL() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http") + "/v1/agent/connect?cluster=dev&instance=other"
}

func (f *impersonationFixture) dial(t *testing.T, url string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return websocket.DefaultDialer.Dial(url, header)
}

func bearerHeader(token string) http.Header {
	return http.Header{"Authorization": []string{"Bearer " + token}}
}

func (f *impersonationFixture) connectEnrolled(t *testing.T) *executionLifecycleAgent {
	t.Helper()
	initialize := gatewayTestInitializeResult("")
	initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[executionLifecycleProtocol] = executionLifecycleCapability()
	conn, resp, err := f.dial(t, f.agentURL(), bearerHeader(f.enroll.Plaintext))
	if err != nil {
		t.Fatalf("dial enrolled agent: %v", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	return f.serveControlledAgent(t, conn, initialize)
}

// serveControlledAgent answers initialize and records tool calls, the same
// controlled Agent the execution lifecycle fixtures use.
func (f *impersonationFixture) serveControlledAgent(t *testing.T, conn *websocket.Conn, initialize map[string]interface{}) *executionLifecycleAgent {
	t.Helper()
	agent := &executionLifecycleAgent{
		conn:    conn,
		calls:   make(chan executionLifecycleCall, 64),
		cancels: make(chan int64, 16),
	}
	stop := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		_ = conn.Close()
	})
	go func() {
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			switch msg["method"] {
			case "initialize":
				if err := agent.write(map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"], "result": initialize}); err != nil {
					return
				}
			case "tools/call":
				id, _ := numericID(msg["id"])
				params, _ := msg["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				name, _ := params["name"].(string)
				session, _ := args["session_id"].(string)
				select {
				case agent.calls <- executionLifecycleCall{id: id, name: name, session: session}:
				case <-stop:
					return
				}
			}
		}
	}()
	requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("dev", "local") != nil })
	return agent
}

// assertNoCalls fails when the controlled Agent received any tool call.
func assertNoCalls(t *testing.T, agent *executionLifecycleAgent) {
	t.Helper()
	select {
	case call := <-agent.calls:
		t.Fatalf("credential payload reached an unenrolled session: %s/%s", call.name, call.session)
	case <-time.After(200 * time.Millisecond):
	}
}

// unenrolledOpaqueCredential creates an active opaque credential with a
// synthetic payload and an active grant on dev/local.
func unenrolledOpaqueCredential(t *testing.T, f *impersonationFixture) (CredentialResource, CredentialVersion, CredentialGrant) {
	t.Helper()
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := f.store.CreateCredential(CredentialCreateRequest{
		Name: "s01-canary", Scope: CredentialScopePlatform, Type: CredentialTypeOpaque, CreatedBy: auth.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: resource.ID, CreatedBy: auth.UserID, Activate: true,
		Payload: json.RawMessage(`{"data":{"dsn":"` + f.canary + `"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: resource.ID, CreatedBy: auth.UserID, Scope: revocationScope("api"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return resource, version, grant
}

func TestAgentRegistrationRefusesTokensThatAreNotAnEnrollmentForTheTarget(t *testing.T) {
	for _, allowOpen := range []bool{false, true} {
		t.Run(fmt.Sprintf("allowOpen=%v", allowOpen), func(t *testing.T) {
			logs := captureGatewayLogs(t)
			f := newImpersonationFixture(t, allowOpen)

			// Given a target without a confirmed enrollment and an attacker that
			// presents an invalid token, When it connects, Then it is refused
			// with 401 and never receives a WebSocket upgrade.
			resp := assertUnauthorized(t, func() (*websocket.Conn, *http.Response, error) {
				return websocket.DefaultDialer.Dial(f.agentURL(), bearerHeader("canary-s01-forged-"+randomHex(8)))
			})
			if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("Upgrade") != "" {
				t.Fatalf("invalid token: status=%d upgrade=%q", resp.StatusCode, resp.Header.Get("Upgrade"))
			}
			if current := f.hub.getAgent("dev", "local"); current != nil {
				t.Fatal("an invalid token registered dev/local")
			}

			// And a real enrollment token for a different target is not an
			// enrollment for this target.
			assertUnauthorized(t, func() (*websocket.Conn, *http.Response, error) {
				return websocket.DefaultDialer.Dial(f.agentURL(), bearerHeader(f.blocked[0].Plaintext))
			})
			if current := f.hub.getAgent("dev", "local"); current != nil {
				t.Fatal("a token for dev/other registered dev/local")
			}

			// Then the refusal carries the stable enrollment category and no secret.
			targets := f.hub.ListTargets()
			if len(targets) != 0 {
				t.Fatalf("refused connections published targets: %#v", targets)
			}
			if strings.Contains(logs.String(), f.blocked[0].Plaintext) || strings.Contains(logs.String(), f.enroll.Plaintext) {
				t.Fatal("an enrollment token reached the log")
			}
		})
	}
}

func assertUnauthorized(t *testing.T, dial func() (*websocket.Conn, *http.Response, error)) *http.Response {
	t.Helper()
	conn, resp, err := dial()
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("expected the registration to be refused")
	}
	if resp == nil {
		t.Fatalf("expected an HTTP refusal, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	var refusal doopsErrorResponse
	if err := json.Unmarshal(body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode refusal %q: %v", body.String(), err)
	}
	if refusal.Code != "agent_enrollment_auth_failed" || refusal.AuthDomain != "agent_enrollment" {
		t.Fatalf("refusal = %#v, want agent_enrollment_auth_failed/agent_enrollment", refusal)
	}
	return resp
}

func TestAgentRegistrationWithoutTokenIsClosedUnlessOpenRegistrationIsEnabled(t *testing.T) {
	t.Run("flag off", func(t *testing.T) {
		f := newImpersonationFixture(t, false)
		assertUnauthorized(t, func() (*websocket.Conn, *http.Response, error) {
			return websocket.DefaultDialer.Dial(f.agentURL(), nil)
		})
		if current := f.hub.getAgent("dev", "local"); current != nil {
			t.Fatal("a token-less connection registered while open registration is closed")
		}
		// The same unenrolled session cannot slip in through the registry.
		registry := f.hub.registry
		if err := registry.Register(newGatewayAgentSession("dev", "local", "", "198.51.100.9:1", nil, nil)); !errors.Is(err, ErrAgentEnrollmentRequired) {
			t.Fatalf("registry registration = %v, want ErrAgentEnrollmentRequired", err)
		}
	})

	t.Run("flag on", func(t *testing.T) {
		f := newImpersonationFixture(t, true)
		agent := func() *executionLifecycleAgent {
			initialize := gatewayTestInitializeResult("")
			initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[executionLifecycleProtocol] = executionLifecycleCapability()
			conn, _, err := websocket.DefaultDialer.Dial(f.agentURL(), nil)
			if err != nil {
				t.Fatalf("open registration rejected a token-less agent: %v", err)
			}
			return f.serveControlledAgent(t, conn, initialize)
		}()
		_ = agent
		targets := f.hub.ListTargets()
		if len(targets) != 1 || targets[0].Key != "dev/local" {
			t.Fatalf("targets = %#v", targets)
		}
		if targets[0].Enrolled {
			t.Fatal("a token-less session must be reported as not enrolled")
		}
	})

	t.Run("valid token works regardless of enrollment state", func(t *testing.T) {
		// First connect flow: the token itself registers and confirms the
		// target, before any enrollment is confirmed.
		f := newImpersonationFixture(t, false)
		conn, resp, err := websocket.DefaultDialer.Dial(f.agentURL(), bearerHeader(f.enroll.Plaintext))
		if err != nil {
			t.Fatalf("valid enrollment token refused while unconfirmed: %v (resp=%v)", err, resp)
		}
		_ = conn.Close()
	})
}

func TestCredentialDeliverySkipsUnenrolledTargets(t *testing.T) {
	logs := captureGatewayLogs(t)
	f := newImpersonationFixture(t, true)
	resource, version, grant := unenrolledOpaqueCredential(t, f)

	// A token-less session on dev/local must not receive model or registry
	// credentials, and must not receive any materialization either.
	var delivered sync.WaitGroup
	reached := make(chan *GatewayAgent, 1)
	f.hub.registration.onRegistered = func(agent *GatewayAgent) {
		delivered.Add(1)
		select {
		case reached <- agent:
		default:
		}
		f.hub.deliverAgentCredentials(agent)
	}
	initialize := gatewayTestInitializeResult("")
	initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[executionLifecycleProtocol] = executionLifecycleCapability()
	conn, _, err := websocket.DefaultDialer.Dial(f.agentURL(), nil)
	if err != nil {
		t.Fatalf("open registration: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	agent := f.serveControlledAgent(t, conn, initialize)
	var registered *GatewayAgent
	requireEventually(t, 3*time.Second, func() bool {
		select {
		case registered = <-reached:
			return true
		default:
			return false
		}
	})
	if registered.TokenID != "" {
		t.Fatal("a token-less session carries an enrollment ID")
	}
	requireEventually(t, 2*time.Second, func() bool {
		return strings.Contains(logs.String(), "credential delivery skipped for dev/local: target is not enrolled")
	})
	assertNoCalls(t, agent)

	// When a credential is used explicitly for that target, Then it fails with
	// the stable category before any payload leaves the Gateway.
	_, _, err = f.hub.materializeCredentialVersion(
		context.Background(), "dev", "local", "s01-session", resource, version, "", grant.ID,
		CredentialPlanReference{Name: resource.Name, Scope: revocationScope("api")},
	)
	if !errors.Is(err, ErrCredentialTargetUnenrolled) {
		t.Fatalf("materialization = %v, want ErrCredentialTargetUnenrolled", err)
	}
	if got := credentialOperationErrorCategory(err); got != "credential_target_unenrolled" {
		t.Fatalf("category = %s", got)
	}
	if status := credentialHTTPStatus(err); status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if got := credentialErrorCategory(err); got != "credential_target_unenrolled" {
		t.Fatalf("HTTP category = %s", got)
	}
	assertNoCalls(t, agent)
	rows, err := f.store.CredentialMaterializations(resource.ID, "", grant.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("a refused use recorded a materialization: %v %#v", err, rows)
	}

	// The direct dispatch gate refuses too, even when the caller checked
	// earlier (the session can be replaced between check and send).
	_, err = f.hub.RunInternalToolCall(context.Background(), "dev", "local", "doops_credential_materialize", map[string]interface{}{
		"session_id": "s01-race", "credential_id": resource.ID, "version_id": version.ID,
		"grant_id": grant.ID, "scope": revocationScope("api"), "required_keys": []string{"dsn"},
		"payload": map[string]interface{}{"data": map[string]string{"dsn": f.canary}},
	})
	if !errors.Is(err, ErrCredentialTargetUnenrolled) {
		t.Fatalf("dispatch = %v, want ErrCredentialTargetUnenrolled", err)
	}
	assertNoCalls(t, agent)

	// An offline target keeps its own category.
	_ = agent.conn.Close()
	requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
	_, _, err = f.hub.materializeCredentialVersion(
		context.Background(), "dev", "local", "s01-session", resource, version, "", grant.ID,
		CredentialPlanReference{Name: resource.Name, Scope: revocationScope("api")},
	)
	if err == nil || !strings.Contains(err.Error(), "target offline") {
		t.Fatalf("offline materialization = %v, want target offline", err)
	}
	if got := credentialOperationErrorCategory(err); got != "target_offline" {
		t.Fatalf("offline category = %s, want target_offline", got)
	}

	if strings.Contains(logs.String(), f.canary) {
		t.Fatalf("payload canary reached the log: %s", logs.String())
	}
}

func TestEnrolledTargetStillReceivesCredentialDelivery(t *testing.T) {
	logs := captureGatewayLogs(t)
	f := newImpersonationFixture(t, false)
	if err := f.store.ConfirmAgentEnrollment(f.enroll.ID); err != nil {
		t.Fatal(err)
	}
	resource, _, grant := unenrolledOpaqueCredential(t, f)
	agent := f.connectEnrolled(t)

	targets := f.hub.ListTargets()
	if len(targets) != 1 || !targets[0].Enrolled || targets[0].TokenID != f.enroll.ID {
		t.Fatalf("enrolled session = %#v", targets)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := f.hub.materializeCredentialVersion(
			context.Background(), "dev", "local", "s01-enrolled", resource, f.activeVersion(t, resource.ID), "", grant.ID,
			CredentialPlanReference{Name: resource.Name, Scope: revocationScope("api")},
		)
		done <- err
	}()
	call := agent.call(t, "doops_credential_materialize", "s01-enrolled")
	tokenLifecycleReplyText(t, agent, call, `{"status":"failed","error":"fixture stop"}`, true)
	select {
	case err := <-done:
		if errors.Is(err, ErrCredentialTargetUnenrolled) {
			t.Fatalf("an enrolled target was refused: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("materialization did not finish")
	}
	if strings.Contains(logs.String(), f.canary) {
		t.Fatal("payload canary reached the log")
	}
}

func (f *impersonationFixture) activeVersion(t *testing.T, credentialID string) CredentialVersion {
	t.Helper()
	version, err := f.store.CredentialVersionByState(credentialID, CredentialVersionActive)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestTargetsListExposesEnrollmentAsABooleanOnly(t *testing.T) {
	logs := captureGatewayLogs(t)
	f := newImpersonationFixture(t, false)
	if err := f.store.ConfirmAgentEnrollment(f.enroll.ID); err != nil {
		t.Fatal(err)
	}
	initialize := gatewayTestInitializeResult("")
	initialize["capabilities"].(map[string]interface{})["protocols"].(map[string]interface{})[executionLifecycleProtocol] = executionLifecycleCapability()
	conn, _, err := websocket.DefaultDialer.Dial(f.agentURL(), bearerHeader(f.enroll.Plaintext))
	if err != nil {
		t.Fatalf("dial enrolled agent: %v", err)
	}
	f.serveControlledAgent(t, conn, initialize)

	req, _ := http.NewRequest(http.MethodGet, f.server.URL+"/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer "+f.admin.Plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("targets status = %d: %s", resp.StatusCode, body.String())
	}
	var decoded struct {
		Targets []map[string]interface{} `json:"targets"`
	}
	if err := json.Unmarshal(body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Targets) != 1 {
		t.Fatalf("targets = %#v", decoded.Targets)
	}
	target := decoded.Targets[0]
	if enrolled, ok := target["enrolled"].(bool); !ok || !enrolled {
		t.Fatalf("enrolled = %#v, want true", target["enrolled"])
	}
	for key, value := range target {
		if key == "enrolled" {
			continue
		}
		if text, ok := value.(string); ok && strings.Contains(text, f.enroll.Plaintext) {
			t.Fatalf("targets exposed the enrollment token in %s", key)
		}
	}
	if strings.Contains(body.String(), f.enroll.Plaintext) {
		t.Fatalf("targets exposed enrollment material: %s", body.String())
	}
	if strings.Contains(logs.String(), f.enroll.Plaintext) {
		t.Fatal("enrollment token reached the log")
	}
}
