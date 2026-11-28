package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayAuthMeReturnsOnlyTheAuthenticatedTokenMetadata(t *testing.T) {
	for _, finite := range []bool{false, true} {
		name := "no-expiry"
		if finite {
			name = "with-expiry"
		}
		t.Run(name, func(t *testing.T) {
			store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			user, err := store.CreateUser("non-admin-without-grants")
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Time{}
			if finite {
				expires = time.Now().UTC().Add(time.Hour)
			}
			token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "current-token", ExpiresAt: expires})
			if err != nil {
				t.Fatal(err)
			}
			otherUser, err := store.CreateUser("other-user")
			if err != nil {
				t.Fatal(err)
			}
			other, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: otherUser.ID, Name: "other-token"})
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(mux)
			req := httptest.NewRequest(http.MethodGet, "/v1/auth/me?token_id="+other.ID, nil)
			req.Header.Set("Authorization", "Bearer "+token.Plaintext)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("metadata response HTTP %d: %s", response.Code, response.Body.String())
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			var info gatewayCurrentTokenResponse
			if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if info.TokenID != token.ID || info.UserID != user.ID || info.Username != user.Name || info.IsAdmin || info.Name != token.Name ||
				info.IssuedAt.IsZero() || info.IssuedAt.Before(token.CreatedAt.Add(-time.Second)) || info.IssuedAt.After(token.CreatedAt.Add(time.Second)) {
				t.Fatalf("current token metadata mismatch: %#v", info)
			}
			_, hasExpiry := body["expires_at"]
			if finite != hasExpiry || finite && (info.ExpiresAt == nil || info.ExpiresAt.Sub(expires).Abs() > time.Second) {
				t.Fatalf("expiry metadata mismatch: %s", response.Body.String())
			}
			if _, leaked := body["token"]; leaked || strings.Contains(response.Body.String(), token.Plaintext) ||
				strings.Contains(response.Body.String(), other.Plaintext) || strings.Contains(response.Body.String(), other.ID) ||
				strings.Contains(response.Body.String(), otherUser.Name) || strings.Contains(response.Body.String(), "0001-") {
				t.Fatalf("metadata endpoint exposed secret or unrelated identity: %s", response.Body.String())
			}
			if err := store.SetUserAdmin(user.ID, true); err != nil {
				t.Fatal(err)
			}
			response = httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || !info.IsAdmin || info.UserID != user.ID || info.TokenID != token.ID {
				t.Fatalf("current role not reflected in /me: HTTP %d %#v", response.Code, info)
			}
		})
	}
}

func TestGatewayHTTPAuthFailureCategoriesAndStoreUnavailable(t *testing.T) {
	for _, scenario := range []struct {
		state string
		code  string
	}{
		{"missing", "gateway_auth_missing"},
		{"invalid", "gateway_auth_invalid"},
		{"expired", "gateway_auth_expired"},
		{"revoked", "gateway_auth_revoked"},
		{"disabled", "gateway_auth_user_disabled"},
		{"invalid-expiry", "gateway_auth_invalid_expiry"},
		{"store-unavailable", "gateway_auth_unavailable"},
	} {
		t.Run(scenario.state, func(t *testing.T) {
			store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			user, err := store.CreateUser("http-auth")
			if err != nil {
				t.Fatal(err)
			}
			token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID})
			if err != nil {
				t.Fatal(err)
			}
			bearer := token.Plaintext
			switch scenario.state {
			case "missing":
				bearer = ""
			case "invalid":
				bearer += "invalid-secret"
			case "expired":
				_, err = store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", formatTime(time.Now().Add(-time.Minute)), token.ID)
			case "revoked":
				err = store.RevokeToken(token.ID)
			case "disabled":
				err = store.SetUserDisabled(user.ID, true)
			case "invalid-expiry":
				_, err = store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", "invalid-expiry", token.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(mux)
			if scenario.state == "store-unavailable" {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// Every auth gate must forward its original error, without re-verifying
			// and turning an unavailable store into a rejected token.
			for _, path := range []string{
				"/v1/auth/me", "/v1/targets", "/v1/rpc?cluster=dev&instance=node", "/v1/audit",
				"/v1/credentials", "/v1/credentials/cred_synthetic", "/v1/credential-bundles",
				"/v1/admin/users", "/v1/admin/users/disable", "/v1/admin/tokens", "/v1/admin/proxy-routes",
				"/v1/git/dev/node/auth.git/info/refs?service=git-upload-pack",
			} {
				t.Run(path, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, path, nil)
					if bearer != "" {
						req.Header.Set("Authorization", "Bearer "+bearer)
					}
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, req)
					wantStatus := http.StatusUnauthorized
					if scenario.state == "store-unavailable" {
						wantStatus = http.StatusServiceUnavailable
					}
					if response.Code != wantStatus {
						t.Fatalf("HTTP %d, want %d: %s", response.Code, wantStatus, response.Body.String())
					}
					var failure doopsErrorResponse
					if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
						t.Fatal(err)
					}
					if failure.Code != scenario.code || failure.AuthDomain != "gateway" || failure.NextAction == "" {
						t.Fatalf("auth category = %#v", failure)
					}
					challenge := response.Header().Get("WWW-Authenticate")
					if wantStatus == http.StatusUnauthorized && challenge == "" || wantStatus == http.StatusServiceUnavailable && challenge != "" {
						t.Fatalf("unexpected challenge %q for HTTP %d", challenge, wantStatus)
					}
					if (scenario.state == "disabled" || scenario.state == "invalid-expiry") && strings.Contains(failure.NextAction, "Run doops login") {
						t.Fatalf("non-login failure suggests logging in: %#v", failure)
					}
					if strings.Contains(response.Body.String(), token.Plaintext) || strings.Contains(response.Body.String(), "sql:") {
						t.Fatalf("auth response exposes payload or storage internals: %s", response.Body.String())
					}
				})
			}
		})
	}
}

func TestGatewayAuthMeRejectsOtherMethods(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mux := http.NewServeMux()
	NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/me", strings.NewReader("{}"))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
}

func TestGatewayPasswordLoginDistinguishesRejectedCredentialsFromUnavailableAuthentication(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		wantStatus int
		wantCode   string
	}{
		{"unknown-user", http.StatusUnauthorized, "gateway_login_invalid_credentials"},
		{"wrong-password", http.StatusUnauthorized, "gateway_login_invalid_credentials"},
		{"empty-password-hash", http.StatusUnauthorized, "gateway_login_invalid_credentials"},
		{"closed-store", http.StatusServiceUnavailable, "gateway_auth_unavailable"},
		{"failed-user-query", http.StatusServiceUnavailable, "gateway_auth_unavailable"},
		{"invalid-password-hash", http.StatusServiceUnavailable, "gateway_auth_unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "login-user", Password: "synthetic-password"})
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			NewGatewayHub(store, GatewayHubOptions{}).RegisterRoutes(mux)
			username, password := user.Name, "synthetic-password"
			switch scenario.name {
			case "unknown-user":
				username = "unknown-user"
			case "wrong-password":
				password = "synthetic-wrong-password"
			case "empty-password-hash":
				_, err = store.db.Exec("UPDATE users SET password_hash = '' WHERE id = ?", user.ID)
			case "closed-store":
				err = store.Close()
			case "failed-user-query":
				_, err = store.db.Exec("DROP TABLE users")
			case "invalid-password-hash":
				_, err = store.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", "synthetic-invalid-hash", user.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]string{"username": username, "password": password})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(string(body))))
			var failure doopsErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if response.Code != scenario.wantStatus || failure.Code != scenario.wantCode || failure.AuthDomain != "gateway" {
				t.Fatalf("HTTP %d: %#v", response.Code, failure)
			}
			if scenario.wantStatus == http.StatusServiceUnavailable &&
				(response.Header().Get("WWW-Authenticate") != "" || strings.Contains(failure.NextAction, "Run doops login")) {
				t.Fatalf("unavailable authentication asks for another credential: %#v", failure)
			}
			for _, private := range []string{password, "sql:", "database is closed", "no such table", "synthetic-invalid-hash"} {
				if strings.Contains(response.Body.String(), private) {
					t.Fatalf("authentication response exposes private payload or storage internals: %s", response.Body.String())
				}
			}
		})
	}
}
