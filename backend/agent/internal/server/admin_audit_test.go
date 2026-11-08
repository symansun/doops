package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/symansun/doops/backend/agent/api"
)

const adminAuditCanary = "SYNTHETIC_ADMIN_PAYLOAD_CANARY_DO_NOT_LOG"

type adminAuditFixture struct {
	store       *GatewayStore
	hub         *GatewayHub
	mux         *http.ServeMux
	adminToken  string
	userToken   string
	adminAuth   TokenAuth
	user        GatewayUser
	route       ProxyRoute
	grantID     int64
	tokenID     string
	operationID string
	canceled    context.Context
	calls       atomic.Int32
}

func newAdminAuditFixture(t *testing.T) *adminAuditFixture {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("a", 32))
	store, token := newAdminStoreAndToken(t)
	t.Cleanup(func() { store.Close() })
	f := &adminAuditFixture{store: store, adminToken: token}
	var err error
	f.adminAuth, err = store.VerifyUserToken(token)
	if err != nil {
		t.Fatal(err)
	}
	f.user, err = store.CreateUserWithPassword(CreateUserRequest{Name: "audit-user", Password: "initial-canary"})
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: f.user.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.userToken, f.tokenID = userToken.Plaintext, userToken.ID
	if err := store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
		t.Fatal(err)
	}
	grants, err := store.ListGrants(f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.grantID = grants[0].ID
	f.route, err = store.UpsertProxyRoute(ProxyRouteInput{
		Host: "audit.example.test", Cluster: "dev", Instance: "node", Upstream: "http://localhost:8080", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.hub = NewGatewayHub(store, GatewayHubOptions{})
	f.mux = http.NewServeMux()
	f.hub.RegisterRoutes(f.mux)
	var cancel context.CancelFunc
	f.canceled, cancel = context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.operationID = f.hub.client.registerActiveOperation(GatewayActiveOperation{
		UserID: f.user.ID, Cluster: "dev", Instance: "node", Action: ActionExec,
	}, cancel)
	return f
}

func (f *adminAuditFixture) agent(t *testing.T, failed bool) {
	t.Helper()
	server := httptest.NewServer(f.mux)
	t.Cleanup(server.Close)
	conn := dialFakeGatewayAgent(t, server.URL, "dev", "node")
	t.Cleanup(func() { conn.Close() })
	go func() {
		for {
			var msg map[string]any
			if conn.ReadJSON(&msg) != nil {
				return
			}
			switch msg["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": gatewayTestInitializeResult("")})
			case "tools/call":
				f.calls.Add(1)
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"request_id": msg["id"], "data": adminAuditCanary}})
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0", "id": msg["id"],
					"result": map[string]any{"isError": failed, "content": []map[string]any{{"type": "text", "text": adminAuditCanary}}},
				})
			}
		}
	}()
	waitForGatewayAgent(t, f.hub, "dev", "node")
}

func (f *adminAuditFixture) state(t *testing.T) string {
	t.Helper()
	var snapshot bytes.Buffer
	for _, table := range []string{"users", "grants", "tokens", "proxy_routes"} {
		rows, err := f.store.db.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Write(data)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	fmt.Fprintf(&snapshot, "canceled=%v calls=%d", f.canceled.Err(), f.calls.Load())
	return snapshot.String()
}

func assertAdminAudit(t *testing.T, store *GatewayStore, auth TokenAuth, route, method, status string, code int) AuditEvent {
	t.Helper()
	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionAdmin, Limit: 1})
	if err != nil || len(events) != 1 {
		t.Fatalf("audit read: %v, %#v", err, events)
	}
	ev := events[0]
	if ev.UserID != auth.UserID || ev.TokenID != auth.TokenID || ev.Action != ActionAdmin || ev.Status != status || ev.EndedAt.IsZero() {
		t.Fatalf("wrong actor/result: %#v", ev)
	}
	var metadata adminAuditMetadata
	if err := json.Unmarshal([]byte(ev.CommandSummary), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Route != strings.Split(route, "?")[0] || metadata.Method != method || metadata.HTTPStatus != code {
		t.Fatalf("wrong metadata: %#v", metadata)
	}
	data, _ := json.Marshal(ev)
	if strings.Contains(string(data), adminAuditCanary) || ev.Tail != "" {
		t.Fatalf("admin payload entered audit: %s", data)
	}
	return ev
}

func TestAdminMutationRoutesAuditAndFailClosed(t *testing.T) {
	type request struct {
		method, path string
		body         any
	}
	cases := []struct {
		name    string
		prepare func(*testing.T, *adminAuditFixture) request
	}{
		{"user-create", func(t *testing.T, f *adminAuditFixture) request {
			return request{"POST", "/v1/admin/users", map[string]any{"name": "new-user", "password": adminAuditCanary}}
		}},
		{"password", func(t *testing.T, f *adminAuditFixture) request {
			return request{"POST", "/v1/admin/users/password", map[string]any{"user_id": f.user.ID, "password": adminAuditCanary}}
		}},
		{"disable", func(t *testing.T, f *adminAuditFixture) request {
			return request{"POST", "/v1/admin/users/disable", map[string]any{"user_id": f.user.ID, "disabled": true, "payload": adminAuditCanary}}
		}},
		{"grant-create", func(t *testing.T, f *adminAuditFixture) request {
			return request{"POST", "/v1/admin/grants", map[string]any{"user_id": f.user.ID, "cluster": "dev", "instance": "node", "payload": adminAuditCanary}}
		}},
		{"grant-delete", func(t *testing.T, f *adminAuditFixture) request {
			return request{"DELETE", fmt.Sprintf("/v1/admin/grants?id=%d", f.grantID), nil}
		}},
		{"token-create", func(t *testing.T, f *adminAuditFixture) request {
			return request{"POST", "/v1/admin/tokens", map[string]any{"user": f.user.Name, "name": adminAuditCanary, "expires": "1h"}}
		}},
		{"token-revoke", func(t *testing.T, f *adminAuditFixture) request {
			return request{"DELETE", "/v1/admin/tokens?id=" + f.tokenID, nil}
		}},
		{"proxy-upsert", func(t *testing.T, f *adminAuditFixture) request {
			return request{"POST", "/v1/admin/proxy-routes", map[string]any{"id": f.route.ID, "host": f.route.Host, "cluster": "dev", "instance": "node", "upstream": "http://localhost:9090", "payload": adminAuditCanary}}
		}},
		{"proxy-delete", func(t *testing.T, f *adminAuditFixture) request {
			return request{"DELETE", "/v1/admin/proxy-routes?id=" + f.route.ID, nil}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminAuditFixture(t)
			req := tc.prepare(t, f)
			before := f.state(t)
			userAuth, err := f.store.VerifyUserToken(f.userToken)
			if err != nil {
				t.Fatal(err)
			}
			denied := credentialHTTPRequest(t, f.mux, f.userToken, req.method, req.path, req.body)
			if denied.Code != http.StatusForbidden {
				t.Fatalf("ordinary mutation status = %d", denied.Code)
			}
			assertAdminAudit(t, f.store, userAuth, req.path, req.method, "forbidden", http.StatusForbidden)
			for _, phase := range []string{"INSERT", "UPDATE"} {
				trigger := "CREATE TRIGGER deny_admin_audit BEFORE " + phase + " ON audit_events BEGIN SELECT RAISE(FAIL, '" + adminAuditCanary + "'); END"
				if _, err := f.store.db.Exec(trigger); err != nil {
					t.Fatal(err)
				}
				response := credentialHTTPRequest(t, f.mux, f.adminToken, req.method, req.path, req.body)
				if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), adminAuditCanary) {
					t.Fatalf("%s audit unavailable: HTTP %d: %s", phase, response.Code, response.Body.String())
				}
				if after := f.state(t); after != before {
					t.Fatalf("%s audit failure allowed mutation", phase)
				}
				if _, err := f.store.db.Exec("DROP TRIGGER deny_admin_audit"); err != nil {
					t.Fatal(err)
				}
			}
			var countBefore int
			if err := f.store.db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&countBefore); err != nil {
				t.Fatal(err)
			}
			response := credentialHTTPRequest(t, f.mux, f.adminToken, req.method, req.path, req.body)
			wantCode := http.StatusOK
			if response.Code != wantCode {
				t.Fatalf("admin success HTTP %d: %s", response.Code, response.Body.String())
			}
			assertAdminAudit(t, f.store, f.adminAuth, req.path, req.method, "success", wantCode)
			var countAfter int
			if err := f.store.db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&countAfter); err != nil {
				t.Fatal(err)
			}
			wantCount := countBefore + 1
			if countAfter != wantCount {
				t.Fatalf("duplicate/missing audit: before %d after %d", countBefore, countAfter)
			}
			badPath := strings.Split(req.path, "?")[0] + "?id=999999&cluster=missing&instance=missing"
			failed := credentialHTTPRequest(t, f.mux, f.adminToken, req.method, badPath, json.RawMessage(`{"password":"`+adminAuditCanary+`",`))
			if failed.Code < 400 {
				t.Fatalf("invalid mutation succeeded: %d", failed.Code)
			}
			assertAdminAudit(t, f.store, f.adminAuth, badPath, req.method, "error", failed.Code)
		})
	}
}

func TestAdminAuditReadRoutesDoNotCreateMutationEvents(t *testing.T) {
	f := newAdminAuditFixture(t)
	for _, path := range []string{"users", "grants", "tokens", "instances", "proxy-routes"} {
		response := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/admin/"+path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %s", path, response.Code, response.Body.String())
		}
	}
	events, err := f.store.ListAudit(500)
	if err != nil || len(events) != 0 {
		t.Fatalf("read-only routes created audit mutations: %v, %#v", err, events)
	}
	response := credentialHTTPRequest(t, f.mux, "", http.MethodPost, "/v1/admin/users", map[string]any{"name": "unauthenticated"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mutation = %d", response.Code)
	}
}

func TestTargetAdminCannotMutateGlobalResources(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		body               func(*adminAuditFixture) any
	}{
		{"create-platform-admin", "POST", "/v1/admin/users", func(f *adminAuditFixture) any {
			return map[string]any{"name": "escalated-admin", "password": adminAuditCanary, "admin": true}
		}},
		{"reset-admin-password", "POST", "/v1/admin/users/password", func(f *adminAuditFixture) any {
			return map[string]any{"user_id": f.adminAuth.UserID, "password": adminAuditCanary}
		}},
		{"disable-admin", "POST", "/v1/admin/users/disable", func(f *adminAuditFixture) any {
			return map[string]any{"user_id": f.adminAuth.UserID, "disabled": true}
		}},
		{"grant-self-platform", "POST", "/v1/admin/grants", func(f *adminAuditFixture) any {
			return map[string]any{"user_id": f.user.ID, "cluster": "*", "instance": "*", "actions": []string{"admin"}}
		}},
		{"delete-grant", "DELETE", "/v1/admin/grants?id=%d", nil},
		{"issue-admin-token", "POST", "/v1/admin/tokens", func(f *adminAuditFixture) any {
			user, err := f.store.FindUserByID(f.adminAuth.UserID)
			if err != nil {
				t.Fatal(err)
			}
			return map[string]any{"user": user.Name, "name": adminAuditCanary}
		}},
		{"revoke-admin-token", "DELETE", "/v1/admin/tokens?id=%s", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdminAuditFixture(t)
			if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
				t.Fatal(err)
			}
			if !f.store.UserCanUseTarget(f.user.ID, "dev", "node") || f.store.UserIsPlatformAdmin(f.user.ID) {
				t.Fatal("fixture must have target access, not platform admin")
			}
			path := tc.path
			if strings.Contains(path, "%") {
				var id any
				switch {
				case strings.Contains(path, "/grants"):
					id = f.grantID
				case strings.Contains(path, "/tokens"):
					id = f.adminAuth.TokenID
				}
				path = fmt.Sprintf(path, id)
			}
			var body any
			if tc.body != nil {
				body = tc.body(f)
			}
			auth, err := f.store.VerifyUserToken(f.userToken)
			if err != nil {
				t.Fatal(err)
			}
			before := f.state(t)
			response := credentialHTTPRequest(t, f.mux, f.userToken, tc.method, path, body)
			if response.Code != http.StatusForbidden || f.state(t) != before {
				t.Fatalf("target admin mutated global state: HTTP %d: %s", response.Code, response.Body.String())
			}
			assertAdminAudit(t, f.store, auth, path, tc.method, "forbidden", http.StatusForbidden)
		})
	}
}

func TestAdminTokenDirectAndMuxEachAuditOnce(t *testing.T) {
	for _, direct := range []bool{true, false} {
		t.Run(fmt.Sprintf("direct=%t", direct), func(t *testing.T) {
			f := newAdminAuditFixture(t)
			var handler http.Handler = f.mux
			if direct {
				handler = http.HandlerFunc(f.hub.HandleAdminTokens)
			}
			response := credentialHTTPRequest(t, handler, f.adminToken, http.MethodPost, "/v1/admin/tokens",
				map[string]any{"user": f.user.Name, "name": adminAuditCanary})
			if response.Code != http.StatusOK {
				t.Fatalf("token create: HTTP %d: %s", response.Code, response.Body.String())
			}
			events, err := f.store.ListAudit(100)
			if err != nil || len(events) != 1 || events[0].Session != "admin-token-create" {
				t.Fatalf("direct/mux lost or duplicated token audit: %v, %#v", err, events)
			}
			assertAdminAudit(t, f.store, f.adminAuth, "/v1/admin/tokens", http.MethodPost, "success", http.StatusOK)
			var result gatewayLoginResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(events[0].CommandSummary, result.Token) {
				t.Fatal("issued token entered audit")
			}
			if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
				t.Fatal(err)
			}
			before := f.state(t)
			denied := credentialHTTPRequest(t, handler, f.userToken, http.MethodPost, "/v1/admin/tokens", map[string]any{"user": f.user.Name})
			if denied.Code != http.StatusForbidden || f.state(t) != before {
				t.Fatalf("direct/mux token signing bypassed platform authority: HTTP %d", denied.Code)
			}
			events, err = f.store.ListAudit(100)
			if err != nil || len(events) != 2 || events[0].UserID != f.user.ID || events[0].Status != "forbidden" {
				t.Fatalf("direct/mux token denial lost or duplicated audit: %v, %#v", err, events)
			}
			if _, err := f.store.db.Exec(`CREATE TRIGGER deny_token_audit BEFORE INSERT ON audit_events
				BEGIN SELECT RAISE(FAIL, 'synthetic audit unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			before = f.state(t)
			response = credentialHTTPRequest(t, handler, f.adminToken, http.MethodPost, "/v1/admin/tokens", map[string]any{"user": f.user.Name})
			if response.Code != http.StatusServiceUnavailable || f.state(t) != before {
				t.Fatalf("token create bypassed audit gate: HTTP %d", response.Code)
			}
		})
	}
}

// A target grant never reaches the admin control plane; every admin route is
// platform-admin only and still records the refused attempt.
func TestTargetGrantCannotUseAdminRoutes(t *testing.T) {
	f := newAdminAuditFixture(t)
	f.agent(t, false)
	if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
		t.Fatal(err)
	}
	auth, err := f.store.VerifyUserToken(f.userToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/admin/proxy-routes", map[string]any{"id": f.route.ID, "cluster": "dev", "instance": "node", "host": f.route.Host, "upstream": "http://localhost:9090"}},
		{"DELETE", "/v1/admin/proxy-routes?id=" + f.route.ID, nil},
	} {
		before := f.state(t)
		response := credentialHTTPRequest(t, f.mux, f.userToken, tc.method, tc.path, tc.body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("target grant reached admin route: %s HTTP %d: %s", tc.path, response.Code, response.Body.String())
		}
		if f.state(t) != before {
			t.Fatalf("refused admin route mutated state: %s", tc.path)
		}
		assertAdminAudit(t, f.store, auth, tc.path, tc.method, "forbidden", http.StatusForbidden)
	}
}

func TestAdminAuditRejectsOtherTargets(t *testing.T) {
	f := newAdminAuditFixture(t)
	f.agent(t, true)
	limited, err := f.store.CreateUser("limited-admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.GrantUser(limited.ID, ScopeGrant{Cluster: "elsewhere", Instance: "other"}); err != nil {
		t.Fatal(err)
	}
	token, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: limited.ID})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := f.store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"DELETE", "/v1/admin/proxy-routes?id=" + f.route.ID, nil},
		{"POST", "/v1/admin/proxy-routes", map[string]any{"cluster": "dev", "instance": "node", "host": "denied.test", "upstream": "http://localhost:80"}},
		{"POST", "/v1/admin/proxy-routes", map[string]any{"id": f.route.ID, "cluster": "elsewhere", "instance": "other", "host": f.route.Host, "upstream": "http://localhost:80"}},
		{"POST", "/v1/admin/proxy-routes", map[string]any{"id": " " + f.route.ID + " ", "cluster": "elsewhere", "instance": "other", "host": f.route.Host, "upstream": "http://localhost:80"}},
	} {
		before := f.state(t)
		response := credentialHTTPRequest(t, f.mux, token.Plaintext, tc.method, tc.path, tc.body)
		if response.Code != http.StatusForbidden || f.state(t) != before {
			t.Fatalf("%s crossed target authority: HTTP %d", tc.path, response.Code)
		}
		assertAdminAudit(t, f.store, auth, tc.path, tc.method, "forbidden", http.StatusForbidden)
	}
}

func TestAdminAuditResponseStatusAndCompletionFailure(t *testing.T) {
	f := newAdminAuditFixture(t)
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"ok":false,"message":"` + adminAuditCanary + `"}`, http.StatusBadGateway},
		{`{"isError":true,"error":"` + adminAuditCanary + `"}`, http.StatusBadGateway},
		{adminAuditCanary, http.StatusConflict},
	} {
		handler := f.hub.auditAdminMutation("/v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
			if tc.code == http.StatusConflict {
				w.WriteHeader(http.StatusConflict)
			}
			_, _ = w.Write([]byte(tc.body))
		})
		response := credentialHTTPRequest(t, handler, f.adminToken, http.MethodPost, "/v1/admin/users", nil)
		if response.Code != tc.code {
			t.Fatalf("real error status = %d, want %d", response.Code, tc.code)
		}
		assertAdminAudit(t, f.store, f.adminAuth, "/v1/admin/users", http.MethodPost, "error", tc.code)
	}
	called := false
	handler := f.hub.auditAdminMutation("/v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, err := f.store.db.Exec(`CREATE TRIGGER late_audit_failure BEFORE UPDATE ON audit_events
			BEGIN SELECT RAISE(FAIL, 'synthetic late audit failure'); END`); err != nil {
			t.Fatal(err)
		}
		writeJSONHTTP(w, map[string]any{"ok": true, "message": adminAuditCanary})
	})
	response := credentialHTTPRequest(t, handler, f.adminToken, http.MethodPost, "/v1/admin/users", nil)
	if !called || response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "mutation outcome requires verification") ||
		strings.Contains(response.Body.String(), adminAuditCanary) {
		t.Fatalf("completion failure claimed success: HTTP %d: %s", response.Code, response.Body.String())
	}
}

func TestAuditHTTPPersonalHistoryAndCredentialScope(t *testing.T) {
	f := newAdminAuditFixture(t)
	other, err := f.store.CreateUser("other-audit-user")
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []AuditEvent{
		{UserID: f.user.ID, Action: ActionExec, Cluster: "dev", Instance: "node", CommandSummary: "my-exec"},
		{UserID: f.user.ID, Action: ActionAsk, Cluster: "dev", Instance: "node", CommandSummary: "my-ask"},
		{UserID: other.ID, Action: ActionExec, Cluster: "dev", Instance: "node", CommandSummary: "other-exec"},
		{UserID: f.user.ID, Action: ActionCredentialRotate, CommandSummary: "my-unscoped-credential"},
		{UserID: other.ID, Action: ActionCredentialUse, Cluster: "dev", Instance: "node", CommandSummary: "allowed-credential"},
		{UserID: f.user.ID, Action: ActionCredentialUse, Cluster: "prod", Instance: "other", CommandSummary: "my-other-target-credential"},
		{UserID: other.ID, Action: ActionCredentialUse, Cluster: "prod", Instance: "other", CommandSummary: "forbidden-credential"},
		{UserID: other.ID, Action: ActionCredentialRotate, CommandSummary: "other-unscoped-credential"},
	} {
		if _, err := f.store.StartAudit(ev); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/audit", "/v1/audit?user_id=" + f.user.ID, "/v1/audit?user_id=self"} {
		response := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, path, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "my-exec") ||
			!strings.Contains(response.Body.String(), "my-unscoped-credential") ||
			!strings.Contains(response.Body.String(), "my-other-target-credential") ||
			strings.Contains(response.Body.String(), "other-exec") ||
			strings.Contains(response.Body.String(), "allowed-credential") ||
			strings.Contains(response.Body.String(), "forbidden-credential") ||
			strings.Contains(response.Body.String(), "other-unscoped-credential") {
			t.Fatalf("personal audit leaked/omitted events: HTTP %d: %s", response.Code, response.Body.String())
		}
	}
	crossUser := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit?user_id="+other.ID, nil)
	if crossUser.Code != http.StatusForbidden {
		t.Fatalf("cross-user audit accepted: %d", crossUser.Code)
	}
	purge := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodDelete, "/v1/audit?before=2099-01-01T00:00:00Z", nil)
	if purge.Code != http.StatusForbidden {
		t.Fatalf("ordinary user purged audit: %d", purge.Code)
	}
	admin := credentialHTTPRequest(t, f.mux, f.adminToken, http.MethodGet, "/v1/audit?user_id="+other.ID, nil)
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), "other-exec") {
		t.Fatalf("platform admin lost full history: %d: %s", admin.Code, admin.Body.String())
	}
	// A target grant does not reveal other users' events on that target.
	if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
		t.Fatal(err)
	}
	granted := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit", nil)
	if granted.Code != http.StatusOK || !strings.Contains(granted.Body.String(), "my-exec") ||
		strings.Contains(granted.Body.String(), "allowed-credential") ||
		strings.Contains(granted.Body.String(), "other-exec") {
		t.Fatalf("target grant changed audit visibility: %d: %s", granted.Code, granted.Body.String())
	}
	// Owning a credential reveals other users' events that reference it.
	owned, err := f.store.CreateCredential(CredentialCreateRequest{
		Name: "audit-owned", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
		OwnerID: f.user.ID, CreatedBy: f.user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.StartAudit(AuditEvent{UserID: other.ID, Action: ActionCredentialUse, Cluster: "prod", Instance: "other",
		CredentialID: owned.ID, CommandSummary: "owned-credential-use"}); err != nil {
		t.Fatal(err)
	}
	ownerView := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit", nil)
	if ownerView.Code != http.StatusOK || !strings.Contains(ownerView.Body.String(), "owned-credential-use") ||
		strings.Contains(ownerView.Body.String(), "forbidden-credential") ||
		strings.Contains(ownerView.Body.String(), "other-unscoped-credential") {
		t.Fatalf("credential owner audit boundary failed: %d: %s", ownerView.Code, ownerView.Body.String())
	}
	crossCredential := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit?user_id="+other.ID, nil)
	if crossCredential.Code != http.StatusForbidden {
		t.Fatalf("explicit cross-user audit query accepted: %d", crossCredential.Code)
	}
	if _, err := f.store.db.Exec(`DROP TABLE audit_events`); err != nil {
		t.Fatal(err)
	}
	unavailable := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit", nil)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable audit became empty success: %d", unavailable.Code)
	}
}

func TestAuditHTTPExplicitSelfAndGlobalViews(t *testing.T) {
	f := newAdminAuditFixture(t)
	for _, event := range []AuditEvent{
		{UserID: f.user.ID, Action: ActionExec, CommandSummary: "own-operation"},
		{UserID: f.user.ID, Action: ActionCredentialUse, Cluster: "prod", Instance: "other", CommandSummary: "own-credential"},
		{UserID: f.adminAuth.UserID, Action: ActionExec, CommandSummary: "other-operation"},
		{UserID: f.adminAuth.UserID, Action: ActionCredentialUse, Cluster: "dev", Instance: "node", CommandSummary: "other-credential"},
	} {
		if _, err := f.store.StartAudit(event); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"no-grant", "target-grant", "wildcard-grant", "platform-admin"} {
		t.Run(role, func(t *testing.T) {
			t.Cleanup(func() {
				if _, err := f.store.db.Exec("DELETE FROM grants WHERE user_id = ?", f.user.ID); err != nil {
					t.Error(err)
				}
				if err := f.store.SetUserAdmin(f.user.ID, false); err != nil {
					t.Error(err)
				}
			})
			switch role {
			case "target-grant":
				if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
					t.Fatal(err)
				}
			case "wildcard-grant":
				if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "*", Instance: "*"}); err != nil {
					t.Fatal(err)
				}
			case "platform-admin":
				if err := f.store.SetUserAdmin(f.user.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			for _, query := range []string{"user_id=self", "user_id=self&action=credential:use&limit=1"} {
				response := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit?"+query, nil)
				var result auditResponse
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Events) == 0 {
					t.Fatalf("self query failed: HTTP %d: %s", response.Code, response.Body.String())
				}
				for _, event := range result.Events {
					if event.UserID != f.user.ID {
						t.Fatalf("self query returned another user: %#v", event)
					}
				}
				wantCount := 2
				if strings.Contains(query, "action=") {
					wantCount = 1
				}
				if len(result.Events) != wantCount || !strings.Contains(response.Body.String(), "own-credential") {
					t.Fatalf("self query lost own credentials/filters: %s", response.Body.String())
				}
			}
			// A wildcard target grant is not platform admin: global audit stays denied.
			platform := role == "platform-admin"
			for _, query := range []string{"view=global", "view=global&user_id=self"} {
				response := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit?"+query, nil)
				if !platform {
					if response.Code != http.StatusForbidden {
						t.Fatalf("non-platform global query accepted: HTTP %d", response.Code)
					}
					continue
				}
				var result auditResponse
				wantCount := 4
				if strings.Contains(query, "self") {
					wantCount = 2
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Events) != wantCount {
					t.Fatalf("platform global query failed: HTTP %d: %s", response.Code, response.Body.String())
				}
			}
		})
	}
	if _, err := f.store.db.Exec("DROP TABLE audit_events"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ token, query string }{
		{f.userToken, "user_id=self"},
		{f.adminToken, "view=global"},
	} {
		response := credentialHTTPRequest(t, f.mux, tc.token, http.MethodGet, "/v1/audit?"+tc.query, nil)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("unavailable explicit view became success: HTTP %d", response.Code)
		}
	}
}

func TestAuditHTTPFiltersVisibilityBeforeLimit(t *testing.T) {
	f := newAdminAuditFixture(t)
	if err := f.store.GrantUser(f.user.ID, ScopeGrant{Cluster: "dev", Instance: "node"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.StartAudit(AuditEvent{UserID: f.user.ID, Action: ActionExec, CommandSummary: "older-own-event"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 501; i++ {
		if _, err := f.store.StartAudit(AuditEvent{UserID: f.adminAuth.UserID, Action: ActionExec, CommandSummary: "hidden-event"}); err != nil {
			t.Fatal(err)
		}
	}
	response := credentialHTTPRequest(t, f.mux, f.userToken, http.MethodGet, "/v1/audit?limit=1", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "older-own-event") || strings.Contains(response.Body.String(), "hidden-event") {
		t.Fatalf("visible event lost after filtering: %d: %s", response.Code, response.Body.String())
	}
}

func TestConfigWriteUsesOnePayloadFreeRPCAudit(t *testing.T) {
	for _, mode := range []string{"success", "tool-error", "forbidden", "insert-unavailable", "update-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f := newAdminAuditFixture(t)
			f.agent(t, mode == "tool-error")
			auth := f.adminAuth
			if mode == "forbidden" {
				var err error
				auth, err = f.store.VerifyUserToken(f.userToken)
				if err != nil {
					t.Fatal(err)
				}
				// Without a grant on the target the write must not reach the Agent.
				if _, err := f.store.db.Exec("DELETE FROM grants WHERE user_id = ?", f.user.ID); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(mode, "-unavailable") {
				phase := "INSERT"
				if mode == "update-unavailable" {
					phase = "UPDATE"
				}
				if _, err := f.store.db.Exec("CREATE TRIGGER deny_config_audit BEFORE " + phase + " ON audit_events BEGIN SELECT RAISE(FAIL, '" + adminAuditCanary + "'); END"); err != nil {
					t.Fatal(err)
				}
			}
			params, err := json.Marshal(api.ToolCallParams{
				Name:      "doops_file_write",
				Arguments: json.RawMessage(`{"session_id":"config-audit","path":"/root/.agent/settings.json","content":"` + adminAuditCanary + `"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			var replies []map[string]any
			writeRaw := func(raw []byte) error {
				var reply map[string]any
				if err := json.Unmarshal(raw, &reply); err != nil {
					return err
				}
				replies = append(replies, reply)
				return nil
			}
			writeJSON := func(value any) error {
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return writeRaw(raw)
			}
			var logs bytes.Buffer
			oldLog := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(oldLog)
			if err := f.hub.client.handleGatewayToolCall(context.Background(), auth, "dev", "node",
				api.JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params}, writeRaw, writeJSON); err != nil {
				t.Fatal(err)
			}
			if len(replies) == 0 {
				t.Fatal("missing RPC result")
			}
			events, err := f.store.ListAudit(100)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), adminAuditCanary) {
				t.Fatalf("config payload logged: %s", logs.String())
			}
			for _, event := range events {
				data, _ := json.Marshal(event)
				if strings.Contains(string(data), adminAuditCanary) || event.Tail != "" {
					t.Fatalf("config payload audited: %s", data)
				}
			}
			if strings.HasSuffix(mode, "-unavailable") {
				if f.calls.Load() != 0 || replies[len(replies)-1]["error"] == nil {
					t.Fatal("config dispatched without writable audit")
				}
				return
			}
			wantStatus := "success"
			if mode == "forbidden" {
				wantStatus = "forbidden"
				if f.calls.Load() != 0 || replies[len(replies)-1]["error"] == nil {
					t.Fatal("config bypassed target write permission")
				}
			} else if mode == "tool-error" {
				wantStatus = "error"
			}
			if len(events) != 1 || events[0].UserID != auth.UserID || events[0].TokenID != auth.TokenID ||
				events[0].Action != ActionWrite || events[0].Status != wantStatus {
				t.Fatalf("wrong/duplicate config audit: %#v", events)
			}
		})
	}
}

func TestRemovedGatewayAdminOperationRoutesReturnNotFound(t *testing.T) {
	f := newAdminAuditFixture(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/operations?cluster=dev&instance=node"},
		{http.MethodDelete, "/v1/admin/operations?cluster=dev&id=" + f.operationID + "&instance=node"},
		{http.MethodPost, "/v1/targets/unlock?cluster=dev&instance=node"},
		{http.MethodPost, "/v1/admin/audit/optimize"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			before := f.state(t)
			var countBefore int
			if err := f.store.db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&countBefore); err != nil {
				t.Fatal(err)
			}
			response := credentialHTTPRequest(t, f.mux, f.adminToken, tc.method, tc.path, nil)
			if response.Code != http.StatusNotFound {
				t.Fatalf("removed route answered HTTP %d: %s", response.Code, response.Body.String())
			}
			var countAfter int
			if err := f.store.db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&countAfter); err != nil {
				t.Fatal(err)
			}
			if f.state(t) != before || countAfter != countBefore {
				t.Fatal("removed route changed state or wrote an audit event")
			}
		})
	}
	if f.canceled.Err() != nil {
		t.Fatal("removed cancel route still canceled the operation")
	}
}
