package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newFeishuEvent(openID, text, eventID string) feishuEvent {
	var event feishuEvent
	event.Schema = "2.0"
	event.Header.EventID = eventID
	event.Header.EventType = "im.message.receive_v1"
	event.Header.Token = "vt"
	event.Event.Sender.SenderType = "user"
	event.Event.Sender.SenderID.OpenID = openID
	event.Event.Message.MessageType = "text"
	event.Event.Message.ChatID = "oc_1"
	content, _ := json.Marshal(map[string]string{"text": text})
	event.Event.Message.Content = string(content)
	return event
}

func TestParseFeishuCommand(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		defaultC   string
		wantAction string
		wantTarget string
		wantArg    string
		wantErr    bool
	}{
		{name: "help", text: "help", wantAction: "help"},
		{name: "help slash", text: "/帮助", wantAction: "help"},
		{name: "targets", text: "targets", wantAction: "targets"},
		{name: "exec full", text: "exec prod/web-1 uname -a", wantAction: "exec", wantTarget: "prod/web-1", wantArg: "uname -a"},
		{name: "exec default cluster", text: "exec web-1 uptime", defaultC: "staging", wantAction: "exec", wantTarget: "staging/web-1", wantArg: "uptime"},
		{name: "ask", text: "ask prod/web-1 检查磁盘", wantAction: "ask", wantTarget: "prod/web-1", wantArg: "检查磁盘"},
		{name: "mention stripped", text: "@_user_1 exec prod/web-1 hostname", wantAction: "exec", wantTarget: "prod/web-1", wantArg: "hostname"},
		{name: "missing arg", text: "exec prod/web-1", wantErr: true},
		{name: "no default cluster", text: "exec web-1 uptime", wantErr: true},
		{name: "bad target", text: "exec /web-1 uptime", wantErr: true},
		{name: "unknown command", text: "rm -rf /", wantErr: true},
		{name: "empty", text: "  ", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := parseFeishuCommand(tc.text, tc.defaultC)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got command %#v", cmd)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd.Action != tc.wantAction {
				t.Fatalf("action=%q want %q", cmd.Action, tc.wantAction)
			}
			if tc.wantTarget != "" && cmd.Cluster+"/"+cmd.Instance != tc.wantTarget {
				t.Fatalf("target=%q want %q", cmd.Cluster+"/"+cmd.Instance, tc.wantTarget)
			}
			if cmd.Argument != tc.wantArg {
				t.Fatalf("arg=%q want %q", cmd.Argument, tc.wantArg)
			}
		})
	}
}

func TestFeishuSignature(t *testing.T) {
	body := []byte(`{"hello":"doops"}`)
	sum := sha256.Sum256([]byte("1700000000" + "nonce" + "key" + string(body)))
	header := http.Header{}
	header.Set("X-Lark-Request-Timestamp", "1700000000")
	header.Set("X-Lark-Request-Nonce", "nonce")
	header.Set("X-Lark-Signature", hexString(sum[:]))
	if !verifyFeishuSignature(header, body, "key") {
		t.Fatal("valid signature rejected")
	}
	header.Set("X-Lark-Signature", "deadbeef")
	if verifyFeishuSignature(header, body, "key") {
		t.Fatal("invalid signature accepted")
	}
	if verifyFeishuSignature(http.Header{}, body, "key") {
		t.Fatal("missing headers must fail")
	}
}

func hexString(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}

func encryptFeishuBody(t *testing.T, plain []byte, encryptKey string) []byte {
	t.Helper()
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	iv := bytes.Repeat([]byte{7}, aes.BlockSize)
	padded := append([]byte{}, plain...)
	pad := aes.BlockSize - len(padded)%aes.BlockSize
	padded = append(padded, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded)+aes.BlockSize)
	copy(out, iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[aes.BlockSize:], padded)
	envelope, _ := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(out)})
	return envelope
}

func TestDecryptFeishuPayload(t *testing.T) {
	plain := []byte(`{"type":"url_verification","challenge":"abc"}`)
	body := encryptFeishuBody(t, plain, "ek")
	got, err := decryptFeishuPayload(body, "ek")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !strings.Contains(string(got), "url_verification") {
		t.Fatalf("unexpected plaintext: %s", got)
	}
	if _, err := decryptFeishuPayload(body, "wrong"); err == nil {
		// 错误 key 解出的填充几乎必然非法，这里只要求不 panic；若恰好通过填充校验则说明实现有问题
		if strings.Contains(string(mustDecrypt(t, body, "wrong")), "url_verification") {
			t.Fatal("wrong key should not recover plaintext")
		}
	}
}

func mustDecrypt(t *testing.T, body []byte, key string) []byte {
	out, _ := decryptFeishuPayload(body, key)
	return out
}

func TestHandleFeishuEventURLVerification(t *testing.T) {
	store := openApprovalTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{Feishu: &FeishuConfig{VerificationToken: "vt"}})
	body, _ := json.Marshal(map[string]string{"type": "url_verification", "challenge": "ch-1", "token": "vt"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/integrations/feishu/event", bytes.NewReader(body))
	hub.HandleFeishuEvent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ch-1") {
		t.Fatalf("challenge not echoed: %s", rec.Body.String())
	}

	badBody, _ := json.Marshal(map[string]string{"type": "url_verification", "challenge": "ch-1", "token": "wrong"})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/integrations/feishu/event", bytes.NewReader(badBody))
	hub.HandleFeishuEvent(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token should be 401, got %d", rec.Code)
	}
}

func TestHandleFeishuEventRejectsBadSignature(t *testing.T) {
	store := openApprovalTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{Feishu: &FeishuConfig{VerificationToken: "vt", EncryptKey: "ek"}})
	body, _ := json.Marshal(map[string]string{"type": "url_verification", "challenge": "c", "token": "vt"})
	req := httptest.NewRequest(http.MethodPost, "/v1/integrations/feishu/event", bytes.NewReader(body))
	req.Header.Set("X-Lark-Request-Timestamp", "1")
	req.Header.Set("X-Lark-Request-Nonce", "n")
	req.Header.Set("X-Lark-Signature", "bad")
	rec := httptest.NewRecorder()
	hub.HandleFeishuEvent(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature should be 401, got %d", rec.Code)
	}
}

func TestHandleFeishuEventDisabled(t *testing.T) {
	store := openApprovalTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/integrations/feishu/event", strings.NewReader(`{}`))
	hub.HandleFeishuEvent(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled integration should be 404, got %d", rec.Code)
	}
}

// webhookCapture 启动一个接收回复的幺蛾子服务器，返回收到的最后一条文本。
func webhookCapture(t *testing.T) (string, *string) {
	t.Helper()
	received := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received = payload.Content.Text
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &received
}

func TestFeishuMessageUnknownUser(t *testing.T) {
	store := openApprovalTestStore(t)
	url, received := webhookCapture(t)
	cfg := &FeishuConfig{VerificationToken: "vt", ReplyWebhook: url, Users: map[string]string{}}
	hub := NewGatewayHub(store, GatewayHubOptions{Feishu: cfg})

	hub.handleFeishuMessage(context.Background(), cfg, newFeishuEvent("ou_unknown", "help", "ev_1"))
	if !strings.Contains(*received, "未绑定") {
		t.Fatalf("unexpected reply: %q", *received)
	}
}

func TestFeishuMessageHelp(t *testing.T) {
	store := openApprovalTestStore(t)
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_ = user
	url, received := webhookCapture(t)
	cfg := &FeishuConfig{VerificationToken: "vt", ReplyWebhook: url, Users: map[string]string{"ou_alice": "alice"}}
	hub := NewGatewayHub(store, GatewayHubOptions{Feishu: cfg})

	hub.handleFeishuMessage(context.Background(), cfg, newFeishuEvent("ou_alice", "/help", "ev_2"))
	if !strings.Contains(*received, "DoOps 飞书机器人命令") {
		t.Fatalf("unexpected reply: %q", *received)
	}
}

func TestFeishuMessageUnknownCommand(t *testing.T) {
	store := openApprovalTestStore(t)
	if _, err := store.CreateUser("alice"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	url, received := webhookCapture(t)
	cfg := &FeishuConfig{ReplyWebhook: url, Users: map[string]string{"ou_alice": "alice"}}
	hub := NewGatewayHub(store, GatewayHubOptions{Feishu: cfg})

	hub.handleFeishuMessage(context.Background(), cfg, newFeishuEvent("ou_alice", "reboot now", "ev_3"))
	if !strings.Contains(*received, "未识别命令") {
		t.Fatalf("natural language must be rejected safely: %q", *received)
	}
}

func TestFeishuSendText(t *testing.T) {
	var authHeader, receiveID, msgType string
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSONHTTP(w, map[string]interface{}{"code": 0, "tenant_access_token": "t-app", "expire": 3600})
	})
	mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		receiveID = r.URL.Query().Get("receive_id_type")
		var body struct {
			ReceiveID string `json:"receive_id"`
			MsgType   string `json:"msg_type"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgType = body.MsgType
		if body.ReceiveID != "ou_x" {
			t.Errorf("unexpected receive_id %q", body.ReceiveID)
		}
		writeJSONHTTP(w, map[string]interface{}{"code": 0, "msg": "ok"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &FeishuConfig{AppID: "cli_1", AppSecret: "sec", APIBase: srv.URL}
	if err := feishuSendText(context.Background(), cfg, "ou_x", "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if authHeader != "Bearer t-app" {
		t.Fatalf("auth header=%q", authHeader)
	}
	if receiveID != "open_id" || msgType != "text" {
		t.Fatalf("receive_id_type=%q msg_type=%q", receiveID, msgType)
	}
}

func TestFeishuDuplicate(t *testing.T) {
	store := openApprovalTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	if hub.feishuDuplicate("ev_1") {
		t.Fatal("first event must not be duplicate")
	}
	if !hub.feishuDuplicate("ev_1") {
		t.Fatal("second event must be duplicate")
	}
	if hub.feishuDuplicate("") {
		t.Fatal("empty event id must not dedupe")
	}
	hub.feishuMu.Lock()
	hub.feishuSeen["ev_old"] = time.Now().Add(-10 * time.Minute)
	hub.feishuMu.Unlock()
	if hub.feishuDuplicate("ev_old") {
		t.Fatal("expired event should be pruned and treated as new")
	}
}

func TestParseFeishuTextContent(t *testing.T) {
	text, err := parseFeishuTextContent(`{"text":"@_user_1 /exec prod/web-1 hostname"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if text != "/exec prod/web-1 hostname" {
		t.Fatalf("unexpected text %q", text)
	}
	if _, err := parseFeishuTextContent("not-json"); err == nil {
		t.Fatal("invalid content must error")
	}
}
