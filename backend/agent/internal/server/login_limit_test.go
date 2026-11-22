package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testConsoleForwardSecret = "unit-test-console-forward-secret"

func signConsoleForward(secret []byte, ts int64, ip string) (ipValue, sigValue string) {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(fmt.Sprintf("%d\n%s", ts, ip)))
	return ip, fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestConsoleForwardedIP(t *testing.T) {
	secret := []byte(testConsoleForwardSecret)
	now := time.Unix(1_700_000_000, 0)
	validIP, validSig := signConsoleForward(secret, now.Unix(), "198.51.100.7")
	oldIP, oldSig := signConsoleForward(secret, now.Add(-consoleForwardMaxSkew-time.Second).Unix(), "198.51.100.7")
	newIP, newSig := signConsoleForward(secret, now.Add(consoleForwardMaxSkew+time.Second).Unix(), "198.51.100.7")
	futureIP, futureSig := signConsoleForward(secret, now.Add(consoleForwardMaxSkew).Unix(), "198.51.100.7")
	otherIP, otherSig := signConsoleForward([]byte("another-synthetic-secret"), now.Unix(), "198.51.100.7")
	badIP, badIPsig := signConsoleForward(secret, now.Unix(), "not-an-ip")
	hostIP, hostIPsig := signConsoleForward(secret, now.Unix(), "198.51.100.7:443")
	_, validSigOnly := signConsoleForward(secret, now.Unix(), "198.51.100.7")
	ipOnly, _ := signConsoleForward(secret, now.Unix(), "198.51.100.7")

	cases := []struct {
		name   string
		secret []byte
		ips    []string
		sigs   []string
		want   string
	}{
		{name: "valid", secret: secret, ips: []string{validIP}, sigs: []string{validSig}, want: "198.51.100.7"},
		{name: "valid at skew edge", secret: secret, ips: []string{futureIP}, sigs: []string{futureSig}, want: "198.51.100.7"},
		{name: "expired", secret: secret, ips: []string{oldIP}, sigs: []string{oldSig}},
		{name: "future beyond skew", secret: secret, ips: []string{newIP}, sigs: []string{newSig}},
		{name: "bad MAC", secret: secret, ips: []string{otherIP}, sigs: []string{otherSig}},
		{name: "signature for another IP", secret: secret, ips: []string{validIP}, sigs: []string{otherSig}},
		{name: "bad IP", secret: secret, ips: []string{badIP}, sigs: []string{badIPsig}},
		{name: "IP with port", secret: secret, ips: []string{hostIP}, sigs: []string{hostIPsig}},
		{name: "no secret", secret: nil, ips: []string{validIP}, sigs: []string{validSig}},
		{name: "empty secret", secret: []byte(""), ips: []string{validIP}, sigs: []string{validSig}},
		{name: "no header", secret: secret},
		{name: "IP without signature", secret: secret, ips: []string{ipOnly}},
		{name: "signature without IP", secret: secret, sigs: []string{validSigOnly}},
		{name: "duplicate IP header", secret: secret, ips: []string{validIP, validIP}, sigs: []string{validSig}},
		{name: "duplicate signature header", secret: secret, ips: []string{validIP}, sigs: []string{validSig, validSig}},
		{name: "missing t", secret: secret, ips: []string{validIP}, sigs: []string{"v1=" + validSig[len("t=1700000000,v1="):]}},
		{name: "missing v1", secret: secret, ips: []string{validIP}, sigs: []string{"t=" + fmt.Sprint(now.Unix())}},
		{name: "non-hex v1", secret: secret, ips: []string{validIP}, sigs: []string{fmt.Sprintf("t=%d,v1=zz", now.Unix())}},
		{name: "extra field", secret: secret, ips: []string{validIP}, sigs: []string{validSig + ",v2=00"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
			req.RemoteAddr = "10.1.2.3:4321"
			for _, v := range tc.ips {
				req.Header.Add(consoleClientIPHeader, v)
			}
			for _, v := range tc.sigs {
				req.Header.Add(consoleClientIPSigHeader, v)
			}
			if got := consoleForwardedIP(req, tc.secret, now); got != tc.want {
				t.Fatalf("consoleForwardedIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// newConsoleForwardTestServer starts a Gateway whose login limit is `limit`
// failures per (address, username) and whose console forwarding secret is
// secret (nil disables it).
func newConsoleForwardTestServer(t *testing.T, limit int, secret []byte) (*GatewayStore, *httptest.Server) {
	t.Helper()
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	hub := NewGatewayHub(store, GatewayHubOptions{
		LoginFailureLimit:    limit,
		LoginFailureWindow:   time.Minute,
		ConsoleForwardSecret: secret,
	})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return store, ts
}

// postConsoleLogin signs the browser address when signing is true; otherwise
// it sends only X-Real-IP, the way the gateway proxy does for CLI clients.
func postConsoleLogin(t *testing.T, baseURL, browserIP, username, password string, secret []byte, signing bool) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/auth/login",
		strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", browserIP)
	if signing {
		ip, sig := signConsoleForward(secret, time.Now().Unix(), browserIP)
		req.Header.Set(consoleClientIPHeader, ip)
		req.Header.Set(consoleClientIPSigHeader, sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var parsed doopsErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp.StatusCode, parsed.Code
}

func TestConsoleForwardingSeparatesLoginBuckets(t *testing.T) {
	secret := []byte(testConsoleForwardSecret)
	store, ts := newConsoleForwardTestServer(t, 2, secret)
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "correct-horse"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Browser A spends the account budget. The console would relay every one
	// of these from its own address; the signature makes them A's.
	for i := 0; i < 2; i++ {
		if status, code := postConsoleLogin(t, ts.URL, "198.51.100.7", "alice", "wrong-password", secret, true); status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
			t.Fatalf("attempt %d = %d %s", i, status, code)
		}
	}
	if status, code := postConsoleLogin(t, ts.URL, "198.51.100.7", "alice", "correct-horse", secret, true); status != http.StatusTooManyRequests || code != "gateway_login_rate_limited" {
		t.Fatalf("A while limited = %d %s", status, code)
	}
	// Browser B is a different bucket even though it shares the console host.
	if status, code := postConsoleLogin(t, ts.URL, "203.0.113.9", "alice", "correct-horse", secret, true); status != http.StatusOK {
		t.Fatalf("B must log in as alice: %d %s", status, code)
	}
}

func TestConsoleForwardingIgnoresUnsignedHeaders(t *testing.T) {
	// The address budget is limit*loginIPLimitMultiplier; keep both numbers
	// small so the spray below stays short.
	limit := loginIPLimitMultiplier
	if limit < 2 {
		limit = 2
	}
	secret := []byte(testConsoleForwardSecret)
	store, ts := newConsoleForwardTestServer(t, limit, secret)
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "correct-horse"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Spend the 198.51.100.7 address budget with unsigned requests that name
	// different accounts, so no account budget is reached first.
	for i := 0; i < limit*loginIPLimitMultiplier-1; i++ {
		if status, code := postConsoleLogin(t, ts.URL, "198.51.100.7", fmt.Sprintf("spray-%d", i), "wrong-password", secret, false); status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
			t.Fatalf("unsigned attempt %d = %d %s", i, status, code)
		}
	}
	// A signed header claiming 203.0.113.9 is ignored: the request still
	// counts for the proxy-reported 198.51.100.7 and fills its budget.
	status, code := postConsoleLogin(t, ts.URL, "198.51.100.7", "spray-last", "wrong-password", secret, true)
	println("DBG SPRAYLAST", status, code)
	if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" || status == http.StatusOK {
		t.Fatalf("unsigned request with a foreign signature = %d %s", status, code)
	}
	// Only the signed address B owns reaches B's bucket, which was never
	// touched by the spray.
	if status, code := postConsoleLogin(t, ts.URL, "203.0.113.9", "alice", "correct-horse", secret, true); status != http.StatusOK {
		t.Fatalf("203.0.113.9 must stay unaffected by unsigned headers: %d %s", status, code)
	}
	// The unsigned failures still count for 198.51.100.7: its own bucket
	// reaches the address budget once enough of them are recorded.
	for i := 0; ; i++ {
		if i == 10 {
			t.Fatal("198.51.100.7 address bucket never filled")
		}
		status, code := postConsoleLogin(t, ts.URL, "198.51.100.7", "spray-again", "wrong-password", secret, false)
		if status == http.StatusTooManyRequests && code == "gateway_login_rate_limited" {
			break
		}
		if status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
			t.Fatalf("unsigned attempt past the budget %d = %d %s", i, status, code)
		}
	}
}

func TestConsoleForwardingWithoutSecretKeepsProxyAddress(t *testing.T) {
	// Without the secret, X-Real-IP decides the bucket no matter what the
	// console headers claim: requests that arrived through the console proxy
	// from two different browsers share the proxy peer, so one browser's
	// failures spend the address budget for both.
	store, ts := newConsoleForwardTestServer(t, 2, nil)
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "correct-horse"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	secret := []byte(testConsoleForwardSecret)
	if status, code := postConsoleLogin(t, ts.URL, "127.0.0.1", "alice", "wrong-password", secret, true); status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
		t.Fatalf("first typo = %d %s", status, code)
	}
	if status, code := postConsoleLogin(t, ts.URL, "127.0.0.1", "alice", "wrong-password", secret, true); status != http.StatusUnauthorized || code != "gateway_login_invalid_credentials" {
		t.Fatalf("second typo = %d %s", status, code)
	}
	// The signed header claims another browser, which must not give B a fresh
	// bucket on the same proxy peer.
	if status, code := postConsoleLogin(t, ts.URL, "127.0.0.1", "alice", "correct-horse", secret, true); status != http.StatusTooManyRequests || code != "gateway_login_rate_limited" {
		t.Fatalf("the address the proxy reports must still be limited: %d %s", status, code)
	}
}

func TestConsoleForwardingSuccessResetsAccountFailures(t *testing.T) {
	secret := []byte(testConsoleForwardSecret)
	// The address budget is limit*loginIPLimitMultiplier. Two earlier
	// successful logins from the same browser also spend it, so keep room.
	limit := 2
	if limit*loginIPLimitMultiplier <= 2+2 {
		limit = (2 + 2) / loginIPLimitMultiplier
		if limit < 1 {
			limit = 1
		}
	}
	store, ts := newConsoleForwardTestServer(t, limit, secret)
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "correct-horse"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if status, _ := postConsoleLogin(t, ts.URL, "198.51.100.7", "alice", "wrong-password", secret, true); status != http.StatusUnauthorized {
		t.Fatalf("first typo = %d", status)
	}
	if status, _ := postConsoleLogin(t, ts.URL, "198.51.100.7", "alice", "correct-horse", secret, true); status != http.StatusOK {
		t.Fatalf("login after one typo = %d", status)
	}
	// The earlier typo was cleared, so one new typo is still under the account
	// limit and the retry with the right password works.
	if status, _ := postConsoleLogin(t, ts.URL, "198.51.100.7", "alice", "wrong-password", secret, true); status != http.StatusUnauthorized {
		t.Fatalf("typo after success = %d", status)
	}
	if status, code := postConsoleLogin(t, ts.URL, "198.51.100.7", "alice", "correct-horse", secret, true); status != http.StatusOK {
		t.Fatalf("login after one more typo = %d %s", status, code)
	}
}

func TestLoginFailureLimiterResetOnlyDropsTheAccountKey(t *testing.T) {
	l := newLoginFailureLimiter()
	now := time.Now()
	l.record("198.51.100.7", "alice", now.Add(-time.Second), time.Minute)
	l.record("198.51.100.7", "bob", now.Add(-time.Second), time.Minute)
	l.reset("198.51.100.7", "Alice", now)
	// The per-address budget (still 2) limits everything from this address.
	if got := len(l.byIPKey[loginAccountKey("198.51.100.7", "alice")]); got != 0 {
		t.Fatalf("reset address/username kept %d failures", got)
	}
	if l.limited("198.51.100.7", "alice", now, 2, time.Minute) {
		t.Fatal("reset address/username must not limit")
	}
	if !l.limited("198.51.100.7", "bob", now, 2, time.Minute) {
		t.Fatal("reset must not clear another account")
	}
	if got := len(l.byIP["198.51.100.7"]); got != 2 {
		t.Fatalf("per-address failures = %d, want 2", got)
	}
	// A failure recorded after the reset counts again.
	l.record("198.51.100.7", "alice", now.Add(time.Second), time.Minute)
	if !l.limited("198.51.100.7", "alice", now, 2, time.Minute) {
		t.Fatal("post-reset failure must count")
	}
}

func TestLoginFailureLimiterResetKeepsAccountBudgetBounded(t *testing.T) {
	l := newLoginFailureLimiter()
	start := time.Now()
	// One address, ten accounts: the address budget is only reached after far
	// more than ten failures, so each account stays under its own limit.
	for i := 0; i < 10; i++ {
		user := fmt.Sprintf("alice-%d", i)
		l.record("198.51.100.7", user, start.Add(time.Duration(i)*time.Second), time.Minute)
		l.reset("198.51.100.7", user, start.Add(time.Duration(i)*time.Second+time.Millisecond))
		l.record("198.51.100.7", user, start.Add(time.Duration(i)*time.Second+2*time.Millisecond), time.Minute)
		if l.limited("198.51.100.7", user, start.Add(20*time.Second), 2*loginIPLimitMultiplier, time.Minute) {
			t.Fatalf("%s must have at most one live failure", user)
		}
	}
	// The shared-address failures stayed, so a spray that keeps typing one
	// error and the right password across accounts still reaches the address
	// budget and limits.
	if got := len(pruneLoginTimes(l.byIP, "198.51.100.7", start.Add(20*time.Second), time.Minute)); got != 20 {
		t.Fatalf("per-address failures = %d, want 20 (successes must not reset them)", got)
	}
	if l.limited("198.51.100.7", "carol", start.Add(20*time.Second), 2*loginIPLimitMultiplier, time.Minute) {
		t.Fatal("per-address budget must not be reset by successes")
	}
}
