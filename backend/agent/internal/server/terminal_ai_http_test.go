package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupTerminalAIFixture(t *testing.T) (*GatewayHub, *http.ServeMux, string, string) {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "test-terminal-ai-master-key-32-bytes!!")
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	admin, err := store.CreateUser("admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := store.SetUserAdmin(admin.ID, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}
	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	other, err := store.CreateUser("bob")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if err := store.GrantUser(alice.ID, ScopeGrant{Cluster: "prod", Instance: "node-1"}); err != nil {
		t.Fatalf("grant alice: %v", err)
	}
	aliceTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: alice.ID, Name: "alice-tok"})
	if err != nil {
		t.Fatalf("create alice token: %v", err)
	}
	bobTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: other.ID, Name: "bob-tok"})
	if err != nil {
		t.Fatalf("create bob token: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	return hub, mux, aliceTok.Plaintext, bobTok.Plaintext
}

func bindActiveModelProvider(t *testing.T, store *GatewayStore, name, cluster, instance, payloadJSON string) {
	t.Helper()
	users, err := store.ListUsers()
	if err != nil || len(users) == 0 {
		t.Fatalf("list users: %v", err)
	}
	actor := ""
	for _, u := range users {
		if u.Name == "admin" {
			actor = u.ID
			break
		}
	}
	if actor == "" {
		t.Fatalf("admin user not found")
	}
	res, err := store.CreateCredential(CredentialCreateRequest{
		Name:      name,
		Scope:     CredentialScopePlatform,
		OwnerID:   actor,
		Type:      CredentialTypeModelProvider,
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatalf("create modelProvider credential: %v", err)
	}
	ver, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: res.ID,
		Payload:      json.RawMessage(payloadJSON),
		CreatedBy:    actor,
	})
	if err != nil {
		t.Fatalf("put credential version: %v", err)
	}
	grant, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: res.ID,
		Scope: CredentialGrantScope{
			Cluster:   cluster,
			Instance:  instance,
			Namespace: "default",
			Use:       CredentialUseModelProvider,
		},
		CreatedBy: actor,
	})
	if err != nil {
		t.Fatalf("grant credential: %v", err)
	}
	if err := store.RecordCredentialVerification(CredentialVerification{
		CredentialID: res.ID,
		VersionID:    ver.ID,
		GrantID:      grant.ID,
		Use:          CredentialUseModelProvider,
		Status:       "verified",
		Evidence:     CredentialMaterialization{Status: "verified"},
	}); err != nil {
		t.Fatalf("record verification: %v", err)
	}
	if err := store.PromoteCredentialVersion(res.ID, ver.ID, actor); err != nil {
		t.Fatalf("promote credential: %v", err)
	}
}

func postTerminalAI(mux *http.ServeMux, token string, body any) *httptest.ResponseRecorder {
	var raw []byte
	switch v := body.(type) {
	case string:
		raw = []byte(v)
	default:
		raw, _ = json.Marshal(v)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/terminal/ai", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestHandleTerminalAI_OpenAIAndAnthropicSingleShotSuccessAndAudit(t *testing.T) {
	prevAllow := terminalAIAllowLoopbackForTest
	terminalAIAllowLoopbackForTest = true
	t.Cleanup(func() { terminalAIAllowLoopbackForTest = prevAllow })

	const openaiCanary = "sk-synthetic-openai-canary-998877"
	var seenAuth string
	var seenPath string
	openaiUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"explanation\":\"查看 CPU 核心数与实时占用。\",\"commands\":[{\"command\":\"$ lscpu\",\"description\":\"查看 CPU 架构与核心数\"},{\"command\":\"top -bn1 | head -n 12\",\"description\":\"查看实时占用\"}]}"}}],
			"usage":{"prompt_tokens":120,"completion_tokens":48,"total_tokens":168}
		}`))
	}))
	defer openaiUpstream.Close()

	hub, mux, adminTok, bobTok := setupTerminalAIFixture(t)

	// 1. Unauthenticated -> 401
	if rr := postTerminalAI(mux, "", map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s1", "question": "我想查一下系统的cpu",
	}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rr.Code)
	}

	// 2. Unauthorized user (no target grant) -> 403
	if rr := postTerminalAI(mux, bobTok, map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s1", "question": "我想查一下系统的cpu",
	}); rr.Code != http.StatusForbidden {
		t.Fatalf("ungranted user status = %d, want 403", rr.Code)
	}

	// 3. No active modelProvider bound -> 412
	if rr := postTerminalAI(mux, adminTok, map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s1", "question": "我想查一下系统的cpu",
	}); rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("missing modelProvider status = %d, want 412: %s", rr.Code, rr.Body.String())
	}

	// 4. Bind OpenAI-compatible modelProvider (with https placeholder patched via custom transport? No, loopback allowed for test only via direct payload insert)
	// decodeModelProviderPayload requires https:// when validCredentialEndpoint runs, so we use https://model.test and route via terminalAIHTTPTransport.
	prevTransport := terminalAIHTTPTransport
	terminalAIHTTPTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(openaiUpstream.URL, "http://")
		return http.DefaultTransport.RoundTrip(clone)
	})
	t.Cleanup(func() { terminalAIHTTPTransport = prevTransport })

	bindActiveModelProvider(t, hub.store, "mp-deepseek", "prod", "node-1",
		`{"model":"deepseek/deepseek-flash","provider":{"deepseek":{"options":{"apiKey":"`+openaiCanary+`","baseURL":"https://api.deepseek.com","kind":"openai"}}}}`)

	rr := postTerminalAI(mux, adminTok, map[string]any{
		"cluster":        "prod",
		"instance":       "node-1",
		"session_id":     "console-s1",
		"question":       "我想查一下系统的cpu",
		"action":         "ask",
		"cwd":            "/workspace",
		"context_prompt": "[终端上下文] cwd=/workspace",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), openaiCanary) {
		t.Fatalf("response leaked modelProvider apiKey canary")
	}
	if seenAuth != "Bearer "+openaiCanary {
		t.Fatalf("upstream Authorization = %q, want Bearer canary", seenAuth)
	}
	if seenPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %q, want /v1/chat/completions", seenPath)
	}

	var parsed terminalAIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !parsed.OK || parsed.Model != "deepseek/deepseek-flash" || parsed.Provider != "deepseek" {
		t.Fatalf("unexpected meta: %+v", parsed)
	}
	if len(parsed.Commands) != 2 || parsed.Commands[0].Command != "lscpu" {
		t.Fatalf("unexpected commands (expected stripped '$ '): %+v", parsed.Commands)
	}
	if parsed.Usage == nil || parsed.Usage.TotalTokens != 168 {
		t.Fatalf("unexpected usage: %+v", parsed.Usage)
	}

	// Verify audit event recorded with model & tokens and zero canary leakage.
	events, err := hub.store.ListAuditFiltered(AuditFilter{Cluster: "prod", Instance: "node-1", Limit: 20})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	found := false
	for _, ev := range events {
		if string(ev.Action) == "terminal:ai" {
			found = true
			if ev.Status != "success" || ev.Model != "deepseek/deepseek-flash" || ev.TotalTokens != 168 {
				t.Fatalf("unexpected audit row: %+v", ev)
			}
			if strings.Contains(ev.CommandSummary+ev.Error+ev.Tail, openaiCanary) {
				t.Fatalf("audit row leaked apiKey canary: %+v", ev)
			}
		}
	}
	if !found {
		t.Fatalf("expected terminal:ai audit event")
	}
}

func TestHandleTerminalAI_AnthropicFallbackAndUpstreamSanitization(t *testing.T) {
	const anthCanary = "sk-ant-synthetic-canary-445566"
	var seenKey string
	var seenPath string
	mode := "ok"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("x-api-key")
		seenPath = r.URL.Path
		switch mode {
		case "redirect":
			http.Redirect(w, r, "https://evil.invalid/steal", http.StatusFound)
			return
		case "auth_error":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("invalid api key: " + anthCanary))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content":[{"type":"text","text":"可以通过下面命令查看磁盘占用：\n` + "```bash\\ndf -h\\n```" + `"}],
			"usage":{"input_tokens":50,"output_tokens":30}
		}`))
	}))
	defer upstream.Close()

	prevTransport := terminalAIHTTPTransport
	terminalAIHTTPTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(upstream.URL, "http://")
		return http.DefaultTransport.RoundTrip(clone)
	})
	t.Cleanup(func() { terminalAIHTTPTransport = prevTransport })

	hub, mux, adminTok, _ := setupTerminalAIFixture(t)
	bindActiveModelProvider(t, hub.store, "mp-anth", "prod", "node-1",
		`{"model":"minimax/MiniMax-M3","provider":{"minimax":{"options":{"apiKey":"`+anthCanary+`","baseURL":"https://api.minimaxi.com/anthropic","kind":"anthropic"}}}}`)

	// Markdown fallback success
	rr := postTerminalAI(mux, adminTok, map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s2", "question": "查磁盘空间",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("anthropic fallback status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if seenKey != anthCanary || seenPath != "/anthropic/v1/messages" {
		t.Fatalf("anthropic request mismatch: keyMatch=%v path=%q", seenKey == anthCanary, seenPath)
	}
	var parsed terminalAIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parsed.Commands) != 1 || parsed.Commands[0].Command != "df -h" {
		t.Fatalf("markdown fallback commands = %+v", parsed.Commands)
	}

	// Upstream 401 must NOT echo upstream raw body / canary
	mode = "auth_error"
	rrAuth := postTerminalAI(mux, adminTok, map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s2", "question": "查磁盘空间",
	})
	if rrAuth.Code != http.StatusBadGateway {
		t.Fatalf("upstream 401 status = %d, want 502", rrAuth.Code)
	}
	if strings.Contains(rrAuth.Body.String(), anthCanary) {
		t.Fatalf("error response leaked upstream canary: %s", rrAuth.Body.String())
	}

	// Redirect must be blocked
	mode = "redirect"
	rrRedir := postTerminalAI(mux, adminTok, map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s2", "question": "查磁盘空间",
	})
	if rrRedir.Code != http.StatusBadGateway || !strings.Contains(rrRedir.Body.String(), "terminal_ai_redirect_blocked") {
		t.Fatalf("redirect status=%d body=%s", rrRedir.Code, rrRedir.Body.String())
	}
}

func TestHandleTerminalAI_AuditUnavailableFailsClosedWithoutCallingUpstream(t *testing.T) {
	called := false
	prevTransport := terminalAIHTTPTransport
	terminalAIHTTPTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, http.ErrHandlerTimeout
	})
	t.Cleanup(func() { terminalAIHTTPTransport = prevTransport })

	hub, mux, adminTok, _ := setupTerminalAIFixture(t)
	bindActiveModelProvider(t, hub.store, "mp-ok", "prod", "node-1",
		`{"model":"deepseek/deepseek-flash","provider":{"deepseek":{"options":{"apiKey":"sk-canary-no-call","baseURL":"https://api.deepseek.com","kind":"openai"}}}}`)

	// Break audit_events table to simulate unavailable audit persistence.
	if _, err := hub.store.db.Exec(`DROP TABLE audit_events`); err != nil {
		t.Fatalf("drop audit_events: %v", err)
	}

	rr := postTerminalAI(mux, adminTok, map[string]any{
		"cluster": "prod", "instance": "node-1", "session_id": "s3", "question": "查看内存",
	})
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "credential_audit_unavailable") {
		t.Fatalf("expected 500 credential_audit_unavailable, got %d: %s", rr.Code, rr.Body.String())
	}
	if called {
		t.Fatalf("upstream LLM must NOT be called when audit persistence is unavailable")
	}
}

func TestBuildTerminalAIUpstreamURL(t *testing.T) {
	cases := []struct {
		kind, provider, base, want string
	}{
		{"openai", "deepseek", "https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions"},
		{"openai", "ark", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{"openai", "dashscope", "https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		{"openai", "custom", "https://llm.example.com/v1/chat/completions", "https://llm.example.com/v1/chat/completions"},
		{"anthropic", "anthropic", "https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		{"anthropic", "minimax", "https://api.minimaxi.com/anthropic", "https://api.minimaxi.com/anthropic/v1/messages"},
		{"anthropic", "anthropic", "https://proxy.example.com/v1", "https://proxy.example.com/v1/messages"},
	}
	for _, tc := range cases {
		got, err := buildTerminalAIUpstreamURL(tc.kind, tc.provider, tc.base)
		if err != nil {
			t.Fatalf("buildTerminalAIUpstreamURL(%q,%q,%q) err=%v", tc.kind, tc.provider, tc.base, err)
		}
		if got != tc.want {
			t.Fatalf("buildTerminalAIUpstreamURL(%q,%q,%q) = %q, want %q", tc.kind, tc.provider, tc.base, got, tc.want)
		}
	}
	if _, err := buildTerminalAIUpstreamURL("openai", "openai", "http://plain.example.com"); err == nil {
		t.Fatalf("expected plain http baseURL to be rejected")
	}
}

func TestHandleTerminalAI_ParsingSummaryAndTrailingJSON(t *testing.T) {
	// 1. String-array commands JSON format compatibility.
	exp, cmds := parseTerminalAIOutput(`{"explanation":"查看主机状态","commands":["$ lscpu","nproc","lscpu"]}`)
	if exp != "查看主机状态" || len(cmds) != 2 || cmds[0].Command != "lscpu" || cmds[1].Command != "nproc" {
		t.Fatalf("string-array parse failed: exp=%q cmds=%+v", exp, cmds)
	}

	// 2. Audit summary normalizes whitespace and bounds length without rewriting user text.
	summary := summarizeTerminalAIQuestion("ask", "  查看\t  nginx\n为什么重启  ")
	if summary != "terminal_ai[ask]: 查看 nginx 为什么重启" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	longSummary := summarizeTerminalAIQuestion("ask", strings.Repeat("查", 150))
	if !strings.HasSuffix(longSummary, "…") || len([]rune(longSummary)) != len([]rune("terminal_ai[ask]: "))+121 {
		t.Fatalf("unexpected truncated summary: %q", longSummary)
	}

	// 3. Trailing JSON payload after valid object must be rejected with 400.
	_, mux, aliceTok, _ := setupTerminalAIFixture(t)
	rr := postTerminalAI(mux, aliceTok, `{"cluster":"prod","instance":"node-1","session_id":"s1","question":"查cpu"}{"extra":1}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
