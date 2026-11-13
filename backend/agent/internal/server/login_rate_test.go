package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newLoginTestServer(t *testing.T, limit int) (*GatewayStore, *httptest.Server) {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	hub := NewGatewayHub(store, GatewayHubOptions{LoginFailureLimit: limit, LoginFailureWindow: time.Minute})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return store, ts
}

func TestPasswordLoginRateLimitAndUnknownUserShareTheError(t *testing.T) {
	store, ts := newLoginTestServer(t, 2)
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.VerifyUserPassword("missing-user", "Ab123456"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown user error = %v", err)
	}
	if _, err := store.VerifyUserPassword("alice", "wrong-password"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password error = %v", err)
	}

	for i := 0; i < 2; i++ {
		status, code := postLoginFrom(t, ts.URL, "198.51.100.7", `{"username":"alice","password":"wrong-password"}`)
		if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
			t.Fatalf("attempt %d = %d %s", i, status, code)
		}
	}
	status, code := postLoginFrom(t, ts.URL, "198.51.100.7", `{"username":"alice","password":"Ab123456"}`)
	if status != http.StatusTooManyRequests || code != "gateway_login_rate_limited" {
		t.Fatalf("correct password while limited = %d %s", status, code)
	}
	// The same account from another client is not locked out.
	status, _ = postLoginFrom(t, ts.URL, "203.0.113.9", `{"username":"alice","password":"Ab123456"}`)
	if status != http.StatusOK {
		t.Fatalf("another client must still log in as alice: %d", status)
	}
	// Another account from the limited client is still allowed until the
	// per-address budget is spent.
	status, code = postLoginFrom(t, ts.URL, "198.51.100.7", `{"username":"nobody","password":"wrong-password"}`)
	if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
		t.Fatalf("other account from same client = %d %s", status, code)
	}
}

func TestPasswordLoginPerAddressLimitIsAMultipleOfTheAccountLimit(t *testing.T) {
	_, ts := newLoginTestServer(t, 2)
	budget := 2 * loginIPLimitMultiplier
	for i := 0; i < budget; i++ {
		status, code := postLoginFrom(t, ts.URL, "198.51.100.7", fmt.Sprintf(`{"username":"spray-%d","password":"wrong-password"}`, i))
		if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
			t.Fatalf("attempt %d = %d %s", i, status, code)
		}
	}
	status, code := postLoginFrom(t, ts.URL, "198.51.100.7", `{"username":"spray-last","password":"wrong-password"}`)
	if status != http.StatusTooManyRequests || code != "gateway_login_rate_limited" {
		t.Fatalf("address over budget = %d %s", status, code)
	}
	status, code = postLoginFrom(t, ts.URL, "203.0.113.9", `{"username":"spray-last","password":"wrong-password"}`)
	if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
		t.Fatalf("another client behind the same proxy must not be limited: %d %s", status, code)
	}
}

func TestLoginClientIPIgnoresForwardedHeadersFromPublicPeers(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.RemoteAddr = "192.0.2.10:4321"
	req.Header.Set("X-Real-IP", "198.51.100.7")
	req.Header.Set("X-Forwarded-For", "198.51.100.8")
	if got := loginClientIP(req); got != "192.0.2.10" {
		t.Fatalf("public peer chose its own bucket: %q", got)
	}
	req.RemoteAddr = "127.0.0.1:4321"
	if got := loginClientIP(req); got != "198.51.100.7" {
		t.Fatalf("loopback proxy client = %q", got)
	}
	req.RemoteAddr = "10.1.2.3:4321"
	req.Header.Del("X-Real-IP")
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 198.51.100.8")
	if got := loginClientIP(req); got != "198.51.100.8" {
		t.Fatalf("forwarded-for client = %q", got)
	}
	req.Header.Del("X-Forwarded-For")
	if got := loginClientIP(req); got != "10.1.2.3" {
		t.Fatalf("private peer without proxy headers = %q", got)
	}
}

func TestLoginFailureLimiterEvictsExpiredAndCapsTable(t *testing.T) {
	l := newLoginFailureLimiter()
	l.maxKeys = 64
	start := time.Now()
	for i := 0; i < 1000; i++ {
		l.record(fmt.Sprintf("192.0.2.%d", i%250), fmt.Sprintf("user-%d", i), start, time.Minute)
		if got := l.size(); got > l.maxKeys {
			t.Fatalf("limiter table exceeded cap: %d", got)
		}
	}
	l.record("198.51.100.1", "late", start.Add(2*time.Minute), time.Minute)
	if got := l.size(); got != 2 {
		t.Fatalf("expired limiter keys kept: %d", got)
	}
	if l.limited("192.0.2.1", "user-1", start.Add(2*time.Minute), 1, time.Minute) {
		t.Fatal("expired failures must not limit")
	}
}

func postLoginFrom(t *testing.T, baseURL, client, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/auth/login", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", client)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var parsed doopsErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp.StatusCode, parsed.Code
}
