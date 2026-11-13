package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database/sql"
)

func TestPasswordLoginTokenRenewsOnAuthenticatedUse(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "*", Instance: "*"}); err != nil {
		t.Fatal(err)
	}
	ttl := 4 * time.Second
	hub := NewGatewayHub(store, GatewayHubOptions{LoginTokenTTL: ttl, LoginTokenMaxLifetime: 30 * time.Second})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"Ab123456","name":"doops login"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", resp.StatusCode, body)
	}
	var parsed gatewayLoginResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.ExpiresAt == nil {
		t.Fatal("password login must set expires_at")
	}
	originalExpiry := parsed.ExpiresAt.UTC()
	issuedVia, err := store.tokenIssuedVia(parsed.TokenID)
	if err != nil || issuedVia != tokenIssuedViaPasswordLogin {
		t.Fatalf("issued_via=%q err=%v", issuedVia, err)
	}

	// Call /me after the renewal lead but before the original expiry, based on
	// the token's own issued/expiry timestamps so slow bcrypt under -race cannot
	// eat the whole TTL before the sleep starts.
	renewAfter := time.Now().UTC()
	if parsed.IssuedAt != nil {
		renewAfter = parsed.IssuedAt.UTC().Add(loginTokenRenewLead(ttl) + 50*time.Millisecond)
	} else {
		renewAfter = originalExpiry.Add(-loginTokenRenewLead(ttl) + 50*time.Millisecond)
	}
	if wait := time.Until(renewAfter); wait > 0 {
		time.Sleep(wait)
	}
	if !time.Now().Before(originalExpiry) {
		t.Fatal("token already expired before the renewal request; increase LoginTokenTTL")
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+parsed.Token)
	me, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me status=%d", me.StatusCode)
	}
	var info gatewayCurrentTokenResponse
	if err := json.NewDecoder(me.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.ExpiresAt == nil || !info.ExpiresAt.After(originalExpiry) {
		t.Fatalf("active use must extend expires_at: original=%s renewed=%v", originalExpiry, info.ExpiresAt)
	}

	wait := time.Until(originalExpiry) + 150*time.Millisecond
	if wait > 0 {
		time.Sleep(wait)
	}
	if _, err := store.VerifyUserToken(parsed.Token); err != nil {
		t.Fatalf("token should still be valid after original expiry when renewed: %v", err)
	}
}

func TestPasswordLoginTokenExpiresWhenIdlePastTTL(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"}); err != nil {
		t.Fatal(err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{LoginTokenTTL: 200 * time.Millisecond, LoginTokenMaxLifetime: time.Second})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/auth/login", "application/json", strings.NewReader(`{"username":"alice","password":"Ab123456"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var parsed gatewayLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := store.VerifyUserToken(parsed.Token); err == nil {
		t.Fatal("idle token must expire after TTL")
	} else if code := authFailureCode(err); code != "gateway_auth_expired" {
		t.Fatalf("idle expiry code=%s err=%v", code, err)
	}
}

func TestPasswordLoginRenewalNeverExceedsMaxLifetime(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-89 * 24 * time.Hour)
	token, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "doops login",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		IssuedVia: tokenIssuedViaPasswordLogin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE tokens SET created_at = ? WHERE id = ?`, formatTime(createdAt), token.ID); err != nil {
		t.Fatal(err)
	}
	auth, err := store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.MaybeRenewPasswordLoginToken(auth, DefaultLoginTokenTTL, DefaultLoginTokenMaxLifetime, now); err != nil {
		t.Fatal(err)
	}
	info, _, err := store.CurrentUserTokenInfo(auth)
	if err != nil {
		t.Fatal(err)
	}
	if info.ExpiresAt == nil {
		t.Fatal("renewed token must keep an expiry")
	}
	deadline := createdAt.Add(DefaultLoginTokenMaxLifetime)
	if info.ExpiresAt.After(deadline.Add(time.Second)) {
		t.Fatalf("expires_at %s exceeds created_at+90d %s", info.ExpiresAt, deadline)
	}
}

func TestAdminTokensAreNotRenewed(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"})
	if err != nil {
		t.Fatal(err)
	}
	permanent, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "automation"})
	if err != nil {
		t.Fatal(err)
	}
	expiring, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "ci", ExpiresAt: time.Now().UTC().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []CreatedToken{permanent, expiring} {
		auth, err := store.VerifyUserToken(tok.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		later := time.Now().UTC().Add(90 * time.Minute)
		if err := store.MaybeRenewPasswordLoginToken(auth, 4*time.Hour, 24*time.Hour, later); err != nil {
			t.Fatal(err)
		}
		info, _, err := store.CurrentUserTokenInfo(auth)
		if err != nil {
			t.Fatal(err)
		}
		if tok.ID == permanent.ID && info.ExpiresAt != nil {
			t.Fatalf("permanent admin token gained expiry: %v", info.ExpiresAt)
		}
		if tok.ID == expiring.ID {
			if info.ExpiresAt == nil || info.ExpiresAt.UTC().Sub(tok.ExpiresAt.UTC()) > time.Second || tok.ExpiresAt.UTC().Sub(info.ExpiresAt.UTC()) > time.Second {
				t.Fatalf("expiring admin token was renewed: before=%s after=%v", tok.ExpiresAt, info.ExpiresAt)
			}
		}
		via, err := store.tokenIssuedVia(tok.ID)
		if err != nil || via != "" {
			t.Fatalf("admin token issued_via=%q err=%v", via, err)
		}
	}
}

func TestPasswordLoginTokenCapRevokesOldest(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < passwordLoginTokenCap; i++ {
		tok, err := store.CreateToken(CreateTokenRequest{
			Kind: TokenKindUser, UserID: user.ID, Name: "doops login",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
			IssuedVia: tokenIssuedViaPasswordLogin,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tok.ID)
		time.Sleep(2 * time.Millisecond)
	}
	newest, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "doops login",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		IssuedVia: tokenIssuedViaPasswordLogin,
	})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := store.RevokeExcessPasswordLoginTokens(user.ID, newest.ID, passwordLoginTokenCap, time.Now().UTC())
	if err != nil || revoked != 1 {
		t.Fatalf("revoked=%d err=%v", revoked, err)
	}
	if _, _, err := store.CurrentUserTokenInfo(TokenAuth{TokenID: ids[0], UserID: user.ID, Kind: TokenKindUser}); err == nil {
		t.Fatal("oldest password-login token should be revoked")
	}
	if _, _, err := store.CurrentUserTokenInfo(TokenAuth{TokenID: newest.ID, UserID: user.ID, Kind: TokenKindUser}); err != nil {
		t.Fatalf("newest token should remain: %v", err)
	}
}

func TestBackfillMarksIdentifiablePasswordLoginTokens(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "doops-console",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	custom, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "custom-label",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if via, err := store.tokenIssuedVia(legacy.ID); err != nil || via != "" {
		t.Fatalf("pre-backfill issued_via=%q err=%v", via, err)
	}
	if err := store.backfillPasswordLoginTokens(); err != nil {
		t.Fatal(err)
	}
	if via, err := store.tokenIssuedVia(legacy.ID); err != nil || via != tokenIssuedViaPasswordLogin {
		t.Fatalf("legacy console token issued_via=%q err=%v", via, err)
	}
	if via, err := store.tokenIssuedVia(custom.ID); err != nil || via != "" {
		t.Fatalf("custom token should stay unmarked: issued_via=%q err=%v", via, err)
	}
}

func TestIssuedViaMigrationFromLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL DEFAULT '', disabled INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL)`,
		`CREATE TABLE tokens (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, user_id TEXT, name TEXT NOT NULL,
			prefix TEXT NOT NULL, hash TEXT NOT NULL, cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '', expires_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
		`INSERT INTO users (id, name, password_hash, disabled, created_at) VALUES ('usr_legacy', 'alice', '', 0, '2026-01-01T00:00:00Z')`,
		`INSERT INTO tokens (id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, revoked_at, created_at)
			VALUES ('tok_legacy', 'user', 'usr_legacy', 'doops login', 'dgw_user_ab12', 'not-a-hash', '', '', '2026-12-01T00:00:00Z', '', '2026-11-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	via, err := store.tokenIssuedVia("tok_legacy")
	if err != nil || via != tokenIssuedViaPasswordLogin {
		t.Fatalf("migrated issued_via=%q err=%v", via, err)
	}
}

func TestDefaultLoginTokenLifetimes(t *testing.T) {
	ttl, max := normalizeLoginTokenLifetimes(0, 0)
	if ttl != DefaultLoginTokenTTL || max != DefaultLoginTokenMaxLifetime {
		t.Fatalf("ttl=%s max=%s", ttl, max)
	}
	ttl, max = normalizeLoginTokenLifetimes(48*time.Hour, time.Hour)
	if ttl != 48*time.Hour || max != 48*time.Hour {
		t.Fatalf("max shorter than ttl should bump: ttl=%s max=%s", ttl, max)
	}
}
