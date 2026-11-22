package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBearerTokenRejectsRetiredXDoopsKeyHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/targets", nil)
	req.Header.Set("X-Doops-Key", "retired-token")
	if got := bearerToken(req); got != "" {
		t.Fatalf("retired X-Doops-Key header must be ignored, got %q", got)
	}
}

func TestGatewayPasswordLoginIssuesUserToken(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"})
	if err != nil {
		t.Fatalf("create password user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "*", Instance: "*"}); err != nil {
		t.Fatalf("grant targets list: %v", err)
	}
	oldToken, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "expired-before-login", ExpiresAt: time.Now().UTC().Add(-time.Minute)})
	if err != nil {
		t.Fatalf("create expired prior token: %v", err)
	}
	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, LoginTokenTTL: 2 * time.Second, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"wrong"}`))
	if err != nil {
		t.Fatalf("post bad login: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password must be 401, got %d", resp.StatusCode)
	}
	var loginError doopsErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginError); err != nil {
		t.Fatalf("decode bad login error: %v", err)
	}
	if loginError.Code != "gateway_login_invalid_credentials" || loginError.AuthDomain != "gateway" {
		t.Fatalf("unexpected bad login error: %#v", loginError)
	}
	resp.Body.Close()

	resp, err = http.Post(ts.URL+"/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"Ab123456","name":"test"}`))
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", resp.StatusCode, body)
	}
	var parsed gatewayLoginResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse login response: %v", err)
	}
	if parsed.Token == "" || parsed.TokenID == "" || parsed.Username != "alice" || parsed.TokenType != string(TokenKindUser) ||
		parsed.IssuedAt == nil || parsed.CreatedAt == nil || parsed.ExpiresAt == nil {
		t.Fatalf("unexpected login response: %#v", parsed)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer "+parsed.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get targets with login token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login token should authorize targets:list, got %d", resp.StatusCode)
	}
	oldReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/targets", nil)
	oldReq.Header.Set("Authorization", "Bearer "+oldToken.Plaintext)
	oldResp, err := http.DefaultClient.Do(oldReq)
	if err != nil {
		t.Fatalf("get targets with expired prior token: %v", err)
	}
	defer oldResp.Body.Close()
	if oldResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired prior token status = %d", oldResp.StatusCode)
	}
	var oldError doopsErrorResponse
	if err := json.NewDecoder(oldResp.Body).Decode(&oldError); err != nil {
		t.Fatalf("decode expired prior token error: %v", err)
	}
	if oldError.Code != "gateway_auth_expired" {
		t.Fatalf("expired prior token error = %#v", oldError)
	}
	tokens, err := store.ListTokens(TokenKindUser)
	if err != nil {
		t.Fatalf("list tokens after login: %v", err)
	}
	var foundOld bool
	for _, item := range tokens {
		if item.ID == oldToken.ID {
			foundOld = item.ExpiresAt != nil
		}
	}
	if !foundOld {
		t.Fatalf("expired prior token was deleted or lost expiry metadata")
	}
	time.Sleep(2100 * time.Millisecond)
	if _, err := store.VerifyUserToken(parsed.Token); err == nil {
		t.Fatal("password-login token must expire according to gateway LoginTokenTTL")
	}
}

func TestGatewayHTTPReportsTokenLifecycleReason(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUser("auth-http-reasons")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "*", Instance: "*"}); err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "http-reasons"})
	if err != nil {
		t.Fatal(err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	check := func(want string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/targets", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token.Plaintext)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		var body doopsErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Code != want || body.AuthDomain != "gateway" || body.Message == "" {
			t.Fatalf("auth response = %#v, want code %s", body, want)
		}
	}

	if _, err := store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", formatTime(time.Now().Add(-time.Minute)), token.ID); err != nil {
		t.Fatal(err)
	}
	check("gateway_auth_expired")
	if _, err := store.db.Exec("UPDATE tokens SET expires_at = '', revoked_at = ? WHERE id = ?", formatTime(time.Now()), token.ID); err != nil {
		t.Fatal(err)
	}
	check("gateway_auth_revoked")
	if _, err := store.db.Exec("UPDATE tokens SET revoked_at = '', expires_at = '' WHERE id = ?", token.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	check("gateway_auth_user_disabled")
}

func TestGatewayAdminTokenCreateRequiresAdminAndIssuesUserToken(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	admin, _ := store.CreateUser("admin")
	adminToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: admin.ID, Name: "admin"})
	if err := store.SetUserAdmin(admin.ID, true); err != nil {
		t.Fatalf("grant admin: %v", err)
	}
	alice, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: alice.ID, Name: "alice"})

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	reqBody := strings.NewReader(`{"user":"alice","name":"cli-issued","expires":"1h"}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/tokens", reqBody)
	req.Header.Set("Authorization", "Bearer "+userToken.Plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create token as user: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected non-admin token create to be forbidden, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	reqBody = strings.NewReader(`{"user":"alice","name":"cli-issued","expires":"1h"}`)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/tokens", reqBody)
	req.Header.Set("Authorization", "Bearer "+adminToken.Plaintext)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create token as admin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected admin token create to succeed, got %d: %s", resp.StatusCode, body)
	}
	var body struct {
		Token     string     `json:"token"`
		TokenID   string     `json:"token_id"`
		TokenType string     `json:"token_type"`
		Username  string     `json:"username"`
		IssuedAt  *time.Time `json:"issued_at"`
		CreatedAt *time.Time `json:"created_at"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if body.Token == "" || body.TokenID == "" || body.TokenType != string(TokenKindUser) || body.Username != "alice" ||
		body.IssuedAt == nil || body.CreatedAt == nil || body.ExpiresAt == nil {
		t.Fatalf("unexpected token response: %#v", body)
	}
	auth, err := store.VerifyUserToken(body.Token)
	if err != nil {
		t.Fatalf("created token should verify: %v", err)
	}
	if auth.UserID != alice.ID {
		t.Fatalf("created token user mismatch: want %s got %s", alice.ID, auth.UserID)
	}
	events, err := store.ListAuditFiltered(AuditFilter{Action: ActionAdmin, Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 2 || events[0].Status != "success" || events[0].Session != "admin-token-create" ||
		events[1].Status != "forbidden" || events[1].UserID != alice.ID {
		t.Fatalf("expected one audit per successful/denied token mutation, got %#v", events)
	}
}
