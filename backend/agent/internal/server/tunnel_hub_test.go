package server

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayHTTPListsTargetsForGrantedUserToken(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userToken, err := store.CreateToken(CreateTokenRequest{
		Kind:   TokenKindUser,
		UserID: user.ID,
		Name:   "alice",
	})
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
		t.Fatalf("grant targets list: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, LoginTokenTTL: 20 * time.Millisecond, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	agentWS := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/agent/connect?cluster=dev&instance=local"
	// Open registration admits a token-less Agent on an unconfirmed target ...
	agentConn, resp, err := websocket.DefaultDialer.Dial(agentWS, nil)
	if err != nil {
		t.Fatalf("open registration must accept a token-less agent before an enrollment is confirmed: %v", err)
	}
	_ = agentConn.Close()
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	// ... but a presented token that is not an enrollment for this target is
	// refused even in open mode, never treated as an open registration.
	_, resp, err = websocket.DefaultDialer.Dial(agentWS, http.Header{"Authorization": []string{"Bearer stale-registration-token"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token must be refused with 401, got err=%v resp=%v", err, resp)
	}
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/v1/targets")
	if err != nil {
		t.Fatalf("get targets without token: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
	var authError doopsErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&authError); err != nil {
		t.Fatalf("decode unauthenticated error: %v", err)
	}
	if authError.Code != "gateway_auth_missing" || authError.AuthDomain != "gateway" {
		t.Fatalf("unexpected unauthenticated error: %#v", authError)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer stale-registration-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get targets with stale registration token: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale registration token must not authorize user calls, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer "+userToken.Plaintext)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get targets with default user token: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for default user token, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
