package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/symansun/doops/backend/agent/api"
)

func TestSubscribeDoagentSSEStructuredUI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		updates := []map[string]interface{}{
			{"sessionUpdate": "tool_call_update", "turnId": "ui-turn", "toolCallId": "tool-one", "toolName": "Read", "status": "completed", "rawInput": "PRIVATE_INPUT_CANARY"},
			{"sessionUpdate": "agent_message_chunk", "turnId": "ui-turn", "content": map[string]interface{}{"type": "text", "text": "Read complete. Bearer SECRET_"}},
			{"sessionUpdate": "agent_message_chunk", "turnId": "ui-turn", "content": map[string]interface{}{"type": "text", "text": "SPLIT_CANARY"}},
			{"sessionUpdate": "turn_finished", "turnId": "ui-turn", "status": "completed", "stopReason": "end_turn"},
		}
		for _, update := range updates {
			body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "method": "session/update", "params": map[string]interface{}{"sessionId": "ui-session", "update": update}})
			fmt.Fprintf(w, "data: %s\n\n", body)
		}
	}))
	defer ts.Close()
	turn := make(chan string, 1)
	turn <- "ui-turn"
	var kinds []string
	err := subscribeDoagentSSEWithCollectorReady(t.Context(), ts.URL, "ui-session", func(event notificationEvent) {
		if event.Kind != "acp_update" {
			t.Errorf("unexpected legacy event: %#v", event)
		}
		if strings.Contains(event.Data, "SECRET_SPLIT_CANARY") || strings.Contains(event.Data, "PRIVATE_INPUT_CANARY") {
			t.Errorf("private payload escaped: %s", event.Data)
		}
		var update map[string]interface{}
		if err := json.Unmarshal([]byte(event.Data), &update); err != nil {
			t.Error(err)
			return
		}
		if update["turnId"] != "ui-turn" {
			t.Errorf("turn ownership lost: %#v", update)
		}
		kind, _ := update["sessionUpdate"].(string)
		kinds = append(kinds, kind)
	}, nil, turn, false, true, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kinds, []string{"tool_call_update", "agent_message", "turn_finished"}) {
		t.Fatalf("structured events mismatch: %v", kinds)
	}
}

func decodeDoagentUIEvent(t *testing.T, update map[string]interface{}) map[string]interface{} {
	t.Helper()
	event := publicDoagentUINotification(update)
	if event.Kind != "acp_update" {
		t.Fatalf("unexpected notification: %#v", event)
	}
	decoder := json.NewDecoder(strings.NewReader(event.Data))
	decoder.UseNumber()
	var public map[string]interface{}
	if err := decoder.Decode(&public); err != nil {
		t.Fatalf("decode public update: %v", err)
	}
	return public
}

func TestAgentPromptParamsEventFormatJSON(t *testing.T) {
	var params api.AgentPromptParams
	if err := json.Unmarshal([]byte(`{"session_id":"ui-session","event_format":"acp","operation":"ask","attachments":[{"filename":"report.pdf","media_type":"application/pdf","base64_data":"cGRm"}]}`), &params); err != nil {
		t.Fatal(err)
	}
	if params.EventFormat != "acp" || params.SessionID != "ui-session" || params.Operation != "ask" {
		t.Fatalf("protocol fields did not survive decoding: %#v", params)
	}
	if len(params.Attachments) != 1 || params.Attachments[0].Filename != "report.pdf" || params.Attachments[0].Base64Data != "cGRm" {
		t.Fatalf("attachment fields did not survive decoding: %#v", params.Attachments)
	}
	encoded, err := json.Marshal(params)
	if err != nil || !strings.Contains(string(encoded), `"event_format":"acp"`) {
		t.Fatalf("protocol field did not survive encoding: %s, %v", encoded, err)
	}
	params.EventFormat = ""
	encoded, err = json.Marshal(params)
	if err != nil || strings.Contains(string(encoded), "event_format") {
		t.Fatalf("default event format must be omitted: %s, %v", encoded, err)
	}
}

func TestValidateAgentPromptAttachments(t *testing.T) {
	valid := api.AgentPromptAttachment{
		Filename:   "report.pdf",
		MediaType:  "application/pdf",
		Base64Data: base64.StdEncoding.EncodeToString([]byte("pdf")),
	}
	if err := validateAgentPromptAttachments([]api.AgentPromptAttachment{valid}); err != nil {
		t.Fatalf("valid attachment rejected: %v", err)
	}
	for name, attachment := range map[string]api.AgentPromptAttachment{
		"path traversal": {Filename: "../report.pdf", MediaType: "application/pdf", Base64Data: valid.Base64Data},
		"invalid base64": {Filename: "report.pdf", MediaType: "application/pdf", Base64Data: "%%%"},
		"empty payload":  {Filename: "report.pdf", MediaType: "application/pdf", Base64Data: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateAgentPromptAttachments([]api.AgentPromptAttachment{attachment}); err == nil {
				t.Fatal("invalid attachment was accepted")
			}
		})
	}
}

func TestPublicDoagentUINotificationPreservesIdentityAndExcludesAuthority(t *testing.T) {
	turnID := "turn-" + strings.Repeat("identity-", 180)
	update := map[string]interface{}{
		"sessionUpdate": "tool_call_update", "turnId": turnID,
		"toolCallId": "call-original", "toolName": "mcp_doops_helm",
		"status": "failed", "title": "Verify Deployment", "replay": true,
		"content": []interface{}{map[string]interface{}{
			"type": "content", "token": "nested-token-canary",
			"content": map[string]interface{}{"type": "text", "text": "Deployment verification failed.", "password": "content-password-canary"},
		}},
	}
	privateFields := []string{
		"input", "args", "rawOutput", "outputRef", "attestation", "attestationSchemaVersion",
		"mutationAuthorizationToken", "mutationAuthorizationId", "mutationAuthorizationIssuedAt",
		"mutationAuthorityDigest", "mutationAuthorized", "inputDigest", "resultDigest",
		"publicResultDigest", "canonicalScope", "credentialPayload", "access_token", "apiKey",
		"sessionId", "request_id", "unexpected",
	}
	for _, key := range privateFields {
		update[key] = map[string]interface{}{"secret": key + "-canary"}
	}
	before, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	public := decodeDoagentUIEvent(t, update)
	for _, key := range []string{"sessionUpdate", "turnId", "toolCallId", "toolName", "status", "title", "replay"} {
		if !reflect.DeepEqual(public[key], update[key]) {
			t.Fatalf("display identity %s changed: %#v", key, public[key])
		}
	}
	encoded, _ := json.Marshal(public)
	for _, key := range privateFields {
		if _, exists := public[key]; exists {
			t.Fatalf("private field %s was forwarded", key)
		}
	}
	if strings.Contains(string(encoded), "canary") {
		t.Fatalf("credential/evidence data escaped projection: %s", encoded)
	}
	expected := []interface{}{map[string]interface{}{
		"type": "content", "content": map[string]interface{}{"type": "text", "text": "Deployment verification failed."},
	}}
	if !reflect.DeepEqual(public["content"], expected) {
		t.Fatalf("tool text changed: %#v", public["content"])
	}
	after, _ := json.Marshal(update)
	if string(before) != string(after) {
		t.Fatal("public projection changed the raw collector input")
	}
}

func TestPublicDoagentUINotificationPreservesPlanAndUsage(t *testing.T) {
	for _, key := range []string{"entries", "todos"} {
		t.Run(key, func(t *testing.T) {
			entry := map[string]interface{}{
				"id": "step-1", "content": "Inspect cluster", "status": "in_progress",
				"priority": "high", "activeForm": "Inspecting cluster",
				"credential": "plan-credential-canary", "proof": "plan-proof-canary",
			}
			public := decodeDoagentUIEvent(t, map[string]interface{}{
				"sessionUpdate": "plan", "turnId": "turn-plan", key: []interface{}{entry},
				"usage": map[string]interface{}{
					"inputTokens": json.Number("9007199254740993"), "outputTokens": 0,
					"totalTokens": json.Number("18446744073709551615"), "accessToken": "usage-token-canary",
				},
			})
			projected := public[key].([]interface{})[0].(map[string]interface{})
			for _, field := range []string{"id", "content", "status", "priority", "activeForm"} {
				if projected[field] != entry[field] {
					t.Fatalf("plan field %s changed: %#v", field, projected[field])
				}
			}
			usage := public["usage"].(map[string]interface{})
			if usage["inputTokens"] != json.Number("9007199254740993") ||
				usage["outputTokens"] != json.Number("0") || usage["totalTokens"] != json.Number("18446744073709551615") {
				t.Fatalf("usage counters lost precision or zero: %#v", usage)
			}
			encoded, _ := json.Marshal(public)
			if strings.Contains(string(encoded), "canary") || strings.Contains(string(encoded), "accessToken") {
				t.Fatalf("plan/usage forwarded private data: %s", encoded)
			}
		})
	}
	public := decodeDoagentUIEvent(t, map[string]interface{}{
		"sessionUpdate": "turn_finished", "turnId": "turn-terminal", "status": "cancelled",
		"stopReason": "cancelled", "inputTokens": json.Number("17"), "outputTokens": 0, "replay": false,
	})
	if public["turnId"] != "turn-terminal" || public["status"] != "cancelled" || public["stopReason"] != "cancelled" ||
		public["inputTokens"] != json.Number("17") || public["outputTokens"] != json.Number("0") || public["replay"] != false {
		t.Fatalf("terminal data changed: %#v", public)
	}
}

func TestPublicDoagentUINotificationRedactsBufferedTextWithoutTruncation(t *testing.T) {
	prefix := strings.Repeat("beginning and middle stay visible\n", 100)
	suffix := strings.Repeat("\u4e2d\u6587 end stays visible\n", 100)
	buffered := strings.Join([]string{prefix, "https://storage.example.test/object?X-Amz-Signature=", "split-secret-canary", "\n", suffix}, "")
	for _, kind := range []string{"agent_message", "agent_message_chunk"} {
		t.Run(kind, func(t *testing.T) {
			public := decodeDoagentUIEvent(t, map[string]interface{}{
				"sessionUpdate": kind, "turnId": "turn-buffered", "content": map[string]interface{}{"type": "text", "text": buffered},
			})
			text := public["content"].(map[string]interface{})["text"].(string)
			expected := prefix + "https://storage.example.test/object?X-Amz-Signature=...REDACTED\n" + suffix
			if text != expected {
				t.Fatalf("buffered text was leaked, truncated, or damaged: bytes=%d expected=%d", len(text), len(expected))
			}
		})
	}
	public := decodeDoagentUIEvent(t, map[string]interface{}{"sessionUpdate": "agent_message", "content": prefix + suffix})
	if public["content"] != prefix+suffix {
		t.Fatal("plain root scalar text was truncated or changed")
	}
}

func TestPublicDoagentUINotificationRedactsCredentialAndProofCanaries(t *testing.T) {
	samples := []string{
		"authorization=Bearer bearer-canary cookie=session-canary dbPassword=database-canary githubToken=token-canary " +
			"https://user:url-canary@example.test/repo?access_token=query-canary Basic YmFzaWMtY2FuYXJ5 " +
			"https://storage.example.test/object?X-Goog-Signature=signature-canary&X-Amz-Security-Token=security-token-canary " +
			"ghp_" + strings.Repeat("a", 40) + " github_pat_" + strings.Repeat("b", 82) + " AKIA" + strings.Repeat("A", 16),
		"-----BEGIN PRIVATE KEY-----\npem-canary\n-----END PRIVATE KEY-----", // gitleaks:allow -- synthetic test canary
		"-----BEGIN PRIVATE KEY-----\nunfinished-pem-canary",
		`"refresh_token": "quoted-token-canary" password='quoted password-canary' mutationAuthorizationToken=authority-canary inputDigest=proof-canary`,
		`{"visible":"keep","inputTokens":17,"token":"json-token-canary","data":{"tls.key":"json-payload-canary"},"nested":[{"resultDigest":"json-proof-canary","stringData":{"password":"json-password-canary"},"status":"failed"}]}`,
		`{"visible":"keep","nested":"{\"data\":{\"key\":\"encoded-payload-canary\"}}"}`,
		"Tool output:\n```json\n{\"kind\":\"Secret\",\"data\":{\"tls.crt\":\"fenced-payload-canary\"},\"string_data\":{\"opaque\":\"fenced-other-canary\"}}\n```",
		`Runtime details: attestation={"nested":{"proof":"fenced-proof-canary"}}. Visible result: failed.`,
	}
	for index, text := range samples {
		public := decodeDoagentUIEvent(t, map[string]interface{}{"sessionUpdate": "agent_message", "content": text})
		encoded, _ := json.Marshal(public)
		for _, secret := range []string{"canary", "YmFzaWMtY2FuYXJ5", "ghp_", "github_pat_", "AKIA"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("sample %d retained %q: %s", index, secret, encoded)
			}
		}
	}
	public := decodeDoagentUIEvent(t, map[string]interface{}{"sessionUpdate": "agent_message", "content": samples[4]})
	var textJSON map[string]interface{}
	if err := json.Unmarshal([]byte(public["content"].(string)), &textJSON); err != nil {
		t.Fatalf("sanitized structured text is not JSON: %v", err)
	}
	if textJSON["visible"] != "keep" || textJSON["inputTokens"] != float64(17) {
		t.Fatalf("safe structured text changed: %#v", textJSON)
	}
	public = decodeDoagentUIEvent(t, map[string]interface{}{"sessionUpdate": "tool_call_update", "content": samples[7]})
	if public["content"] != `Runtime details: attestation="...REDACTED". Visible result: failed.` {
		t.Fatalf("surrounding prose changed during proof redaction: %#v", public["content"])
	}
}

func TestPublicDoagentUINotificationRejectsMalformedDisplayData(t *testing.T) {
	cases := []map[string]interface{}{
		nil,
		{"sessionUpdate": ""},
		{"sessionUpdate": map[string]interface{}{"token": "invalid-canary"}},
		{"sessionUpdate": "tool_call_update", "toolCallId": map[string]interface{}{"secret": "invalid-canary"}},
		{"sessionUpdate": "agent_message", "content": map[string]interface{}{"type": "image", "data": "invalid-canary"}},
		{"sessionUpdate": "plan", "entries": map[string]interface{}{"token": "invalid-canary"}},
		{"sessionUpdate": "todos_updated", "todos": []interface{}{"invalid-canary"}},
		{"sessionUpdate": "plan", "entries": []interface{}{map[string]interface{}{"content": map[string]interface{}{"token": "invalid-canary"}}}},
		{"sessionUpdate": "usage_update", "usage": "invalid-canary"},
		{"sessionUpdate": "usage_update", "usage": map[string]interface{}{"inputTokens": "invalid-canary"}},
		{"sessionUpdate": "usage_update", "inputTokens": -1},
		{"sessionUpdate": "usage_update", "inputTokens": json.Number("1.5")},
		{"sessionUpdate": "turn_finished", "replay": "invalid-canary"},
		{"sessionUpdate": "agent_message", "content": `Credential payload: data={"opaque":"invalid-canary"`},
	}
	for index, update := range cases {
		event := publicDoagentUINotification(update)
		if event.Kind != "error" || !strings.HasPrefix(event.Data, "doagent_acp_update_invalid:") {
			t.Fatalf("case %d did not fail closed: %#v", index, event)
		}
		if strings.Contains(event.Data, "canary") {
			t.Fatalf("case %d leaked rejected data through error: %#v", index, event)
		}
	}
}
