package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckWebSocketOriginRejectsPlainHTTPOnDirectTLS(t *testing.T) {
	t.Setenv("DOOPS_ALLOW_ALL_WS_ORIGINS", "")
	t.Setenv("DOOPS_ALLOWED_WS_ORIGINS", "")

	tlsReq := func(origin string) *http.Request {
		r := &http.Request{
			Host:   "gw.example.com:8443",
			TLS:    &tls.ConnectionState{},
			Header: http.Header{"Origin": []string{origin}},
		}
		return r
	}
	if checkWebSocketOrigin(tlsReq("http://gw.example.com:8443")) {
		t.Fatal("direct TLS listener must reject plain http:// origin")
	}
	if !checkWebSocketOrigin(tlsReq("https://gw.example.com:8443")) {
		t.Fatal("direct TLS listener should accept matching https:// origin")
	}
}

func TestCredentialImportRefusesUnenrolledTarget(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("u", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{
		AllowOpenAgentRegistration:        true,
		AllowUnenrolledCredentialDelivery: false,
		OperationTimeout:                  10 * time.Second,
	})
	// Connect a real Agent WITHOUT an enrollment token (open registration on,
	// unenrolled credential delivery off).
	agentConn := dialFakeGatewayAgent(t, f.server.URL, "dev", "local")
	agent := &Gateway{
		tasks:      make(map[string]*bgTask),
		sessionMap: make(map[string]*sessionEntry),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.ServeGatewayWebSocketConnWithReady(agentConn, "sec02-unenrolled-agent", nil)
	}()
	t.Cleanup(func() {
		_ = agentConn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("unenrolled test agent did not stop")
		}
	})
	waitForGatewayAgent(t, f.hub, "dev", "local")

	createReq := httptest.NewRequest(http.MethodPost, "/v1/credentials",
		strings.NewReader(`{"name":"unenrolled-import-test","scope":"personal","type":"opaque"}`))
	createReq.Header.Set("Authorization", "Bearer "+f.admin.Plaintext)
	createResp := httptest.NewRecorder()
	f.hub.HandleCredentials(createResp, createReq)
	if createResp.Code != http.StatusOK {
		t.Fatalf("create credential: status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	var created CredentialResource
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode credential: %v", err)
	}

	importBody := `{"session_id":"sec02-unenrolled","cluster":"dev","instance":"local","namespace":"apps","secret_name":"source-db","keys":["DB_PASSWORD"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/credentials/"+created.ID+"/import-secret", strings.NewReader(importBody))
	req.Header.Set("Authorization", "Bearer "+f.admin.Plaintext)
	resp := httptest.NewRecorder()
	f.hub.HandleCredential(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("want 409 Conflict for unenrolled import target, got %d body=%q", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "credential_target_unenrolled") {
		t.Fatalf("want credential_target_unenrolled category, got %q", resp.Body.String())
	}
	// Confirm Gateway recorded blocked audit for internal doops_credential_import
	// and never persisted a credential version.
	versions, err := f.store.CredentialVersions(created.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("unenrolled import must not persist any credential version, got %d", len(versions))
	}
}

func TestDoagentSSEBoundsMultiLineEventsAndBlocksRedirects(t *testing.T) {
	t.Run("does not follow HTTP redirect", func(t *testing.T) {
		var redirectTargetHits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectTargetHits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer target.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/events", http.StatusFound)
		}))
		defer redirector.Close()

		err := subscribeDoagentSSE(context.Background(), redirector.URL, "ses-redirect", func(notificationEvent) {})
		if err == nil || !strings.Contains(err.Error(), "302") {
			t.Fatalf("want HTTP 302 refusal error, got %v", err)
		}
		if redirectTargetHits.Load() != 0 {
			t.Fatalf("SSE client followed redirect: target hits=%d", redirectTargetHits.Load())
		}
	})

	t.Run("caps cumulative multi-line data buffer", func(t *testing.T) {
		chunk := strings.Repeat("a", 128*1024)
		sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Emit > maxDoagentSSELineBytes across multiple data: lines without a blank separator.
			lines := (maxDoagentSSELineBytes / len(chunk)) + 2
			for i := 0; i < lines; i++ {
				_, _ = w.Write([]byte("data: " + chunk + "\n"))
			}
			_, _ = w.Write([]byte("\n"))
		}))
		defer sse.Close()

		err := subscribeDoagentSSE(context.Background(), sse.URL, "ses-oversize", func(notificationEvent) {})
		if err == nil || !strings.Contains(err.Error(), "doagent_sse_event_too_large") {
			t.Fatalf("want doagent_sse_event_too_large, got %v", err)
		}
	})
}

func TestMCPURLValidationAndProbeDoesNotFollowRedirects(t *testing.T) {
	for _, bad := range []string{
		"http://",
		"https://",
		"https://user:secret@mcp.example.com/rpc",
		"https://mcp.example.com/rpc#frag",
		"ftp://mcp.example.com/rpc",
	} {
		e := agentMCPServerEntry{Name: "test-mcp", Transport: "http", URL: bad}
		if err := validateMCPServerEntry(&e); err == nil {
			t.Fatalf("validateMCPServerEntry should reject %q", bad)
		}
	}
	good := agentMCPServerEntry{Name: "test-mcp", Transport: "http", URL: "https://mcp.example.com/rpc"}
	if err := validateMCPServerEntry(&good); err != nil {
		t.Fatalf("validateMCPServerEntry should accept valid URL: %v", err)
	}

	var leaked atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, _, err := probeHTTPMCP(context.Background(), agentMCPServerEntry{
		Name:      "redir-mcp",
		Transport: "http",
		URL:       redirector.URL + "/mcp",
		Headers:   map[string]string{"X-Api-Key": "synthetic-mcp-canary-key"},
	})
	if err == nil || !strings.Contains(err.Error(), "http 307") {
		t.Fatalf("want http 307 refusal, got %v", err)
	}
	if leaked.Load() != 0 {
		t.Fatalf("MCP HTTP probe followed redirect and risked header leakage: sink hits=%d", leaked.Load())
	}
}

func TestSkillMutationsRecordAuditEvents(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: 10 * time.Second})
	// Non-admin user
	nonAdmin, err := f.store.CreateUser("skill-user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userTok, err := f.store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: nonAdmin.ID, Name: "u"})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	postSkill := func(tok string, body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/skills", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		f.hub.HandlePlatformSkills(rec, req)
		return rec
	}

	// 1) Non-admin attempting to create a system skill -> 403 + error audit
	denied := postSkill(userTok.Plaintext, map[string]any{
		"action":   "upsert",
		"slug":     "unauth-sys-skill",
		"scope":    "system",
		"skill_md": "---\nname: test\n---\nbody\n",
	})
	if denied.Code != http.StatusForbidden {
		t.Fatalf("want 403 for non-admin system skill, got %d %s", denied.Code, denied.Body.String())
	}

	// 2) Non-admin creates, toggles, and deletes own custom skill -> 200 + success audits
	if rec := postSkill(userTok.Plaintext, map[string]any{
		"action":   "upsert",
		"slug":     "user-custom-skill",
		"skill_md": "---\nname: custom\n---\nbody\n",
	}); rec.Code != http.StatusOK {
		t.Fatalf("upsert custom skill: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postSkill(userTok.Plaintext, map[string]any{
		"action":  "toggle",
		"slug":    "user-custom-skill",
		"enabled": false,
	}); rec.Code != http.StatusOK {
		t.Fatalf("toggle custom skill: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postSkill(userTok.Plaintext, map[string]any{
		"action": "delete",
		"slug":   "user-custom-skill",
	}); rec.Code != http.StatusOK {
		t.Fatalf("delete custom skill: %d %s", rec.Code, rec.Body.String())
	}

	events, err := f.store.ListAudit(50)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	seen := map[string]string{}
	for _, ev := range events {
		if ev.UserID == nonAdmin.ID {
			seen[string(ev.Action)+":"+ev.Status+":"+ev.Error] = ev.CommandSummary
		}
	}
	for _, want := range []string{
		"skill:upsert:error:system_skill_admin_required",
		"skill:upsert:success:",
		"skill:toggle:success:",
		"skill:delete:success:",
	} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing expected skill audit record %q; got %#v", want, seen)
		}
	}
}
