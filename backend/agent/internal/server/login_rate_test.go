package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordLoginRateLimitAndUnknownUserShareTheError(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.VerifyUserPassword("missing-user", "Ab123456"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown user error = %v", err)
	}
	if _, err := store.VerifyUserPassword("alice", "wrong-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password error = %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{LoginFailureLimit: 2, LoginFailureWindow: time.Minute})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	for i := 0; i < 2; i++ {
		status, code := postLogin(t, ts.URL, `{"username":"alice","password":"wrong-password"}`)
		if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
			t.Fatalf("attempt %d = %d %s", i, status, code)
		}
	}
	status, code := postLogin(t, ts.URL, `{"username":"nobody","password":"wrong-password"}`)
	if status != http.StatusTooManyRequests || code != "gateway_login_rate_limited" {
		t.Fatalf("limited login = %d %s", status, code)
	}
	status, code = postLogin(t, ts.URL, `{"username":"alice","password":"Ab123456"}`)
	if status != http.StatusTooManyRequests || code != "gateway_login_rate_limited" {
		t.Fatalf("correct password while limited = %d %s", status, code)
	}
}

func postLogin(t *testing.T, baseURL, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var parsed doopsErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp.StatusCode, parsed.Code
}
