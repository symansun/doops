package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayAuditHTTPAllowsOwnHistoryAndFiltersEvents(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	admin, _ := store.CreateUser("admin")
	adminToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: admin.ID, Name: "admin"})
	if err := store.SetUserAdmin(admin.ID, true); err != nil {
		t.Fatalf("add admin grant: %v", err)
	}
	user, _ := store.CreateUser("alice")
	userToken, _ := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "alice"})

	id, err := store.StartAudit(AuditEvent{
		UserID:         user.ID,
		TokenID:        userToken.ID,
		Cluster:        "dev",
		Instance:       "local",
		Action:         ActionAsk,
		Session:        "deploy-1",
		CommandSummary: "deploy app",
		StartedAt:      time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("start audit: %v", err)
	}
	if err := store.FinishAudit(id, AuditFinish{Status: "success", Tail: "done", BytesOut: 4}); err != nil {
		t.Fatalf("finish audit: %v", err)
	}

	gw := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, AllowOpenAgentRegistration: true})
	mux := http.NewServeMux()
	gw.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/audit?session=deploy-1&limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+userToken.Plaintext)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get audit as user: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected own audit read to succeed, got %d", resp.StatusCode)
	}
	var own auditResponse
	if err := json.NewDecoder(resp.Body).Decode(&own); err != nil {
		t.Fatal(err)
	}
	if len(own.Events) != 1 || own.Events[0].UserID != user.ID {
		t.Fatalf("own audit boundary failed: %#v", own.Events)
	}
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/audit?session=deploy-1&limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken.Plaintext)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get audit as admin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin audit read to succeed, got %d", resp.StatusCode)
	}
	var body struct {
		Events []auditRecord `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(body.Events) != 1 || body.Events[0].Session != "deploy-1" || body.Events[0].Status != "success" {
		t.Fatalf("unexpected audit response: %#v", body.Events)
	}

	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/v1/audit?before="+time.Now().UTC().Add(time.Hour).Format(time.RFC3339), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken.Plaintext)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete audit: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin audit purge to succeed, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
