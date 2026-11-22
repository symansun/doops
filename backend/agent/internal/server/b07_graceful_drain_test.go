package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestB07DrainRejectsNewOperationsAndMarksHealth503(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()

	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	// Healthy before drain.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/health before drain = %d, want 200", rec.Code)
	}

	u, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(u.ID, ScopeGrant{Cluster: "prod", Instance: "a1"}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	tok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: u.ID})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	auth, err := store.VerifyUserToken(tok.Plaintext)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}

	hub.BeginDrain()

	// 1. /health returns 503.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/health while draining = %d, want 503", rec.Code)
	}

	// 2. Agent registration returns 503 with Retry-After.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/agent/connect?cluster=prod&instance=a1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/v1/agent/connect while draining = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("/v1/agent/connect missing Retry-After header")
	}

	// 3. Git HTTP returns 503 with Retry-After.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/git/prod/a1/s1/info/refs?service=git-upload-pack", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/v1/git while draining = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("/v1/git missing Retry-After header")
	}

	// 4. Public proxy returns 503 with Retry-After.
	proxyReq := httptest.NewRequest(http.MethodGet, "http://demo.example.com/", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, proxyReq)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("public proxy while draining = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("public proxy missing Retry-After header")
	}

	// 5. Internal tool call fails fast with gateway_draining.
	if _, err := hub.RunInternalToolCall(context.Background(), "prod", "a1", "doops_node_info", nil); err == nil || !strings.Contains(err.Error(), "gateway_draining") {
		t.Fatalf("RunInternalToolCall while draining err = %v, want gateway_draining", err)
	}

	// 6. Client RPC tool call returns -32010 gateway_draining and writes no audit row.
	var rpcReply api.JSONRPCResponse
	err = hub.client.handleGatewayToolCall(
		context.Background(),
		auth,
		"prod",
		"a1",
		api.JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"doops_shell","arguments":{"command":"echo hi"}}`),
		},
		func([]byte) error { return nil },
		func(v interface{}) error {
			raw, _ := json.Marshal(v)
			return json.Unmarshal(raw, &rpcReply)
		},
	)
	if err != nil {
		t.Fatalf("handleGatewayToolCall: %v", err)
	}
	if rpcReply.Error == nil || rpcReply.Error.Code != -32010 || !strings.Contains(rpcReply.Error.Message, "gateway_draining") {
		t.Fatalf("RPC reply = %+v, want -32010 gateway_draining", rpcReply.Error)
	}
	events, err := store.ListAudit(10)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("draining rejected RPC must not create audit rows, got %d", len(events))
	}
}

func TestB07WaitIdleTracksMultipleOperations(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()

	// Empty hub returns immediately.
	if rem := hub.WaitIdle(context.Background()); rem != 0 {
		t.Fatalf("WaitIdle on empty hub = %d, want 0", rem)
	}

	op1 := hub.client.registerActiveOperation(GatewayActiveOperation{
		Cluster: "c", Instance: "a1", Action: ActionExec, Kind: "rpc",
	}, func() {})
	op2 := hub.client.registerActiveOperation(GatewayActiveOperation{
		Cluster: "c", Instance: "a2", Action: ActionExec, Kind: "rpc",
	}, func() {})

	// Timeout while still active returns remaining count.
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelShort()
	if rem := hub.WaitIdle(shortCtx); rem != 2 {
		t.Fatalf("WaitIdle on timeout = %d, want 2", rem)
	}

	waitDone := make(chan int, 1)
	go func() {
		waitDone <- hub.WaitIdle(context.Background())
	}()

	hub.client.finishActiveOperation(op1)
	select {
	case rem := <-waitDone:
		t.Fatalf("WaitIdle returned early (%d) while op2 still active", rem)
	case <-time.After(50 * time.Millisecond):
	}

	hub.client.finishActiveOperation(op2)
	select {
	case rem := <-waitDone:
		if rem != 0 {
			t.Fatalf("WaitIdle after all ops finished = %d, want 0", rem)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitIdle did not wake up after last op finished")
	}
}

func TestB07AdminOperationsEndpoint(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	defer hub.CloseTunnels()
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	user, err := store.CreateUser("bob")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID})
	if err != nil {
		t.Fatalf("user token: %v", err)
	}
	admin, err := store.CreateUser("admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := store.SetUserAdmin(admin.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	adminTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: admin.ID})
	if err != nil {
		t.Fatalf("admin token: %v", err)
	}

	// Unauthenticated -> 401.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}

	// Non-admin -> 403.
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil)
	req.Header.Set("Authorization", "Bearer "+userTok.Plaintext)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", rec.Code)
	}

	// Register one op and query as admin -> 200 with operation.
	opID := hub.client.registerActiveOperation(GatewayActiveOperation{
		UserID:   user.ID,
		Cluster:  "prod",
		Instance: "a1",
		Action:   ActionExec,
		Kind:     "rpc",
	}, func() {})
	defer hub.client.finishActiveOperation(opID)

	req = httptest.NewRequest(http.MethodGet, "/v1/admin/operations", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok.Plaintext)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Draining   bool                     `json:"draining"`
		Operations []GatewayActiveOperation `json:"operations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Draining {
		t.Fatal("draining = true, want false")
	}
	if len(payload.Operations) != 1 || payload.Operations[0].ID != opID {
		t.Fatalf("operations = %+v, want [%s]", payload.Operations, opID)
	}
}
