package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayAdminUserAndGrantRequests(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer admin-token" {
			t.Fatalf("authorization = %q", got)
		}
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/users":
			_ = json.NewEncoder(w).Encode(gatewayAdminUsersResponse{
				Users: []gatewayAdminUserSummary{{ID: "usr_1", Name: "alice", HasPassword: true}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/users":
			var request gatewayAdminUserCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode create user: %v", err)
			}
			if request.Name != "alice" || request.Password != "secret" || request.Admin {
				t.Fatalf("create user request = %#v", request)
			}
			_ = json.NewEncoder(w).Encode(gatewayAdminUserCreateResponse{ID: "usr_1", Name: "alice"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/grants":
			if got := r.URL.Query().Get("user"); got != "usr_1" {
				t.Fatalf("grant user query = %q", got)
			}
			_ = json.NewEncoder(w).Encode(gatewayAdminGrantsResponse{
				Grants: []gatewayAdminGrant{{ID: 7, UserID: "usr_1", Cluster: "*", Instance: "*"}},
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/admin/grants":
			if got := r.URL.Query().Get("id"); got != "7" {
				t.Fatalf("grant id query = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "deleted": true})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/grants":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode grant: %v", err)
			}
			if _, hasActions := request["actions"]; hasActions || request["user_id"] != "usr_1" ||
				request["cluster"] != "cluster-a" || request["instance"] != "node-a" {
				t.Fatalf("grant request must be target-only: %#v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user_id": "usr_1", "granted": true})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer server.Close()

	users, err := GatewayAdminUsersList(server.URL, "admin-token")
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 || users[0].Name != "alice" {
		t.Fatalf("users = %#v", users)
	}

	created, err := GatewayAdminUserCreate(server.URL, "admin-token", gatewayAdminUserCreateRequest{
		Name: "alice", Password: "secret",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created.ID != "usr_1" {
		t.Fatalf("created user = %#v", created)
	}

	grants, err := GatewayAdminGrantsList(server.URL, "admin-token", "usr_1")
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 || grants[0].ID != 7 {
		t.Fatalf("grants = %#v", grants)
	}
	if err := GatewayAdminGrantDelete(server.URL, "admin-token", 7); err != nil {
		t.Fatalf("delete grant: %v", err)
	}
	if err := GatewayAdminGrantCreate(server.URL, "admin-token", gatewayAdminGrantRequest{
		UserID: "usr_1", Cluster: "cluster-a", Instance: "node-a",
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}

	if len(paths) != 5 {
		t.Fatalf("request paths = %#v", paths)
	}
}

func TestGatewayAdminUserPreservesStructuredGatewayAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","code":"gateway_auth_failed","auth_domain":"gateway","message":"Gateway user token is missing or invalid","next_action":"Run doops login"}`))
	}))
	defer server.Close()

	_, err := GatewayAdminUsersList(server.URL, "stale-token")
	if err == nil {
		t.Fatal("expected unauthorized error")
	}
	message := err.Error()
	if !strings.Contains(message, "gateway_auth_failed") ||
		!strings.Contains(message, "auth_domain=gateway") ||
		!strings.Contains(message, "Run doops login") {
		t.Fatalf("unexpected error guidance: %v", err)
	}
}

func TestReadInitialPasswordGenerate(t *testing.T) {
	password, generated, err := readInitialPassword(false, true)
	if err != nil {
		t.Fatalf("generate password: %v", err)
	}
	if !generated || len(password) != 32 {
		t.Fatalf("generated password = %q, generated=%t", password, generated)
	}
}
