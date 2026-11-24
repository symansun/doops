package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSensitiveHTTPFixture(t *testing.T) (*GatewayStore, *http.ServeMux, string) {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("a", 32))
	store, token := newAdminStoreAndToken(t)
	t.Cleanup(func() { store.Close() })
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	return store, mux, token
}

func sensitiveRequest(t *testing.T, mux *http.ServeMux, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder
}

func TestSensitiveFilterAdminHTTP(t *testing.T) {
	store, mux, token := newSensitiveHTTPFixture(t)

	resp := sensitiveRequest(t, mux, token, http.MethodPost, "/v1/admin/sensitive-filter/rules", map[string]any{
		"rules": []map[string]any{
			{"text": "rm -rf /", "scope": "command"},
			{"text": "CANARY_PROMPT_TERM", "scope": "prompt"},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("add rules status=%d body=%s", resp.Code, resp.Body.String())
	}
	var added struct {
		Rules   []SensitiveRule `json:"rules"`
		Skipped []string        `json:"skipped"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &added); err != nil || len(added.Rules) != 2 {
		t.Fatalf("add rules body=%s err=%v", resp.Body.String(), err)
	}

	resp = sensitiveRequest(t, mux, token, http.MethodGet, "/v1/admin/sensitive-filter", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.Code, resp.Body.String())
	}
	var view SensitiveFilterView
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil || len(view.Rules) != 2 {
		t.Fatalf("list body=%s err=%v", resp.Body.String(), err)
	}
	if view.Enabled {
		t.Fatal("filter must default to disabled")
	}

	resp = sensitiveRequest(t, mux, token, http.MethodPatch, "/v1/admin/sensitive-filter", map[string]any{"enabled": true})
	if resp.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", resp.Code, resp.Body.String())
	}
	var toggle struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &toggle); err != nil || !toggle.Enabled {
		t.Fatalf("enable body=%s err=%v", resp.Body.String(), err)
	}
	resp = sensitiveRequest(t, mux, token, http.MethodGet, "/v1/admin/sensitive-filter", nil)
	if err := json.Unmarshal(resp.Body.Bytes(), &view); err != nil || !view.Enabled {
		t.Fatalf("enabled view body=%s err=%v", resp.Body.String(), err)
	}

	ruleID := added.Rules[0].ID
	resp = sensitiveRequest(t, mux, token, http.MethodPatch, "/v1/admin/sensitive-filter/rules", map[string]any{"id": ruleID, "enabled": false})
	if resp.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", resp.Code, resp.Body.String())
	}
	if _, blocked := store.MatchSensitiveText("usr_a", "dev", "node", "rm -rf /", sensitiveScopeCommand); blocked {
		t.Fatal("disabled rule must not match")
	}

	resp = sensitiveRequest(t, mux, token, http.MethodDelete, "/v1/admin/sensitive-filter/rules?id="+ruleID, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = sensitiveRequest(t, mux, token, http.MethodDelete, "/v1/admin/sensitive-filter/rules?id=sw_missing", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("missing delete status=%d body=%s", resp.Code, resp.Body.String())
	}

	resp = sensitiveRequest(t, mux, token, http.MethodPost, "/v1/admin/sensitive-filter/whitelist", map[string]any{"kind": "user", "user_id": "usr_a"})
	if resp.Code != http.StatusOK {
		t.Fatalf("add whitelist status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = sensitiveRequest(t, mux, token, http.MethodPost, "/v1/admin/sensitive-filter/whitelist", map[string]any{"kind": "user", "user_id": "usr_a"})
	if resp.Code != http.StatusConflict {
		t.Fatalf("duplicate whitelist status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = sensitiveRequest(t, mux, token, http.MethodDelete, "/v1/admin/sensitive-filter/whitelist?kind=user&user_id=usr_a", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete whitelist status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = sensitiveRequest(t, mux, token, http.MethodDelete, "/v1/admin/sensitive-filter/whitelist?kind=user&user_id=usr_a", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("missing whitelist status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestSensitiveFilterAdminHTTPRejectsInvalidAndNonAdmin(t *testing.T) {
	store, mux, token := newSensitiveHTTPFixture(t)

	resp := sensitiveRequest(t, mux, token, http.MethodPost, "/v1/admin/sensitive-filter/rules", map[string]any{"rules": []map[string]any{{"text": "", "scope": "all"}}})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("empty rule status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = sensitiveRequest(t, mux, token, http.MethodPatch, "/v1/admin/sensitive-filter", map[string]any{})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("toggle without enabled status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = sensitiveRequest(t, mux, token, http.MethodPatch, "/v1/admin/sensitive-filter/rules", map[string]any{"id": "sw_x"})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("patch without enabled status=%d body=%s", resp.Code, resp.Body.String())
	}
	var apiErr struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &apiErr); err != nil || apiErr.Code != "invalid_sensitive_filter_input" {
		t.Fatalf("patch error body=%s err=%v", resp.Body.String(), err)
	}

	malformed := httptest.NewRequest(http.MethodPost, "/v1/admin/sensitive-filter/rules", strings.NewReader("{"))
	malformed.Header.Set("Authorization", "Bearer "+token)
	malformed.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, malformed)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed body status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	apiErr = struct {
		Code string `json:"code"`
	}{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &apiErr); err != nil || apiErr.Code != "invalid_sensitive_filter_input" {
		t.Fatalf("malformed error body=%s err=%v", recorder.Body.String(), err)
	}

	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "plain-reader", Password: "plain-reader-password-123"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userToken, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID})
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	resp = sensitiveRequest(t, mux, userToken.Plaintext, http.MethodGet, "/v1/admin/sensitive-filter", nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d body=%s", resp.Code, resp.Body.String())
	}
}
