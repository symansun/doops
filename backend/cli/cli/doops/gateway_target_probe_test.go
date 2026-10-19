package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProbeGatewayTargetDistinguishesOfflineOnlineAndWrongScope(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		wantOnline  bool
		wantErrText string
	}{
		{name: "allowed offline", mode: "offline"},
		{name: "online", mode: "online", wantOnline: true},
		{name: "wrong scope", mode: "wrong-scope", wantErrText: "http_403"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway, _ := newGatewayTargetProbeServer(t, test.mode, "probe-session")
			defer gateway.Close()

			online, err := probeGatewayTarget(Server{
				Name:     "probe",
				Gateway:  gateway.URL,
				Cluster:  "dev",
				Instance: "node",
			}, "synthetic-token", "probe-session")
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("probe error = %v, want %q", err, test.wantErrText)
				}
				if online {
					t.Fatal("wrong-scope probe was classified as online")
				}
				return
			}
			if err != nil {
				t.Fatalf("probe target: %v", err)
			}
			if online != test.wantOnline {
				t.Fatalf("online = %t, want %t", online, test.wantOnline)
			}
		})
	}
}

func TestCLIRemoveUsesSessionAndPreservesConfigWhenNotOffline(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	for _, test := range []struct {
		name       string
		mode       string
		session    string
		wantRemove bool
		wantOutput string
	}{
		{name: "offline", mode: "offline", session: "retire-offline", wantRemove: true, wantOutput: "Removed offline Gateway target"},
		{name: "online", mode: "online", session: "retire-online", wantOutput: "is online; refusing"},
		{name: "wrong scope", mode: "wrong-scope", session: "retire-denied", wantOutput: "http_403"},
		{name: "info not granted", mode: "info-forbidden", session: "retire-no-info", wantOutput: "forbidden: info on dev/node"},
		{name: "bare 401", mode: "bare-401", session: "retire-401", wantOutput: "http_401"},
		{name: "structured 401", mode: "structured-401", session: "retire-auth", wantOutput: "gateway_auth_failed (auth_domain=gateway)"},
		{name: "gateway unavailable", mode: "unavailable", session: "retire-503", wantOutput: "http_503"},
		{name: "wrong error code", mode: "wrong-code", session: "retire-code", wantOutput: "cannot verify target is offline"},
		{name: "fractional error code", mode: "fractional-code", session: "retire-invalid-code", wantOutput: "cannot verify target is offline"},
		{name: "different target offline", mode: "wrong-target", session: "retire-target", wantOutput: "cannot verify target is offline"},
		{name: "tool error mimics offline", mode: "tool-error", session: "retire-tool-error", wantOutput: "cannot verify target is offline"},
		{name: "connection lost", mode: "disconnect", session: "retire-disconnect", wantOutput: "cannot verify target is offline"},
		{name: "missing session is generated", mode: "offline", wantRemove: true, wantOutput: "[session] -session not given"},
		{name: "blank session is generated", mode: "offline", session: " ", wantRemove: true, wantOutput: "[session] -session not given"},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway, requests := newGatewayTargetProbeServer(t, test.mode, test.session)
			defer gateway.Close()

			configPath := filepath.Join(t.TempDir(), "config.json")
			original := RootConfig{
				Servers: []Server{
					{Name: "probe", Aliases: []string{"old"}, Gateway: gateway.URL, Cluster: "dev", Instance: "node"},
					{Name: "keep", Gateway: gateway.URL, Cluster: "dev", Instance: "other"},
				},
				LLM: LLMConfig{Model: "synthetic-model", BaseURL: "https://example.invalid"},
			}
			before, err := json.MarshalIndent(original, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, before, 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			t.Setenv("DOOPS_CONFIG", configPath)
			auth := &AuthStore{}
			auth.SetGateway(gateway.URL, "synthetic-token")
			if err := auth.Save(); err != nil {
				t.Fatalf("write auth: %v", err)
			}
			NewSessionStore().Set("probe", test.session, "cached-session-must-not-replace-explicit-session")

			args := []string{"remove", "--target", "old"}
			if test.session != "" {
				args = append([]string{"-session", test.session}, args...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("CLI timed out: %s", output)
			}
			after, readErr := os.ReadFile(configPath)
			if readErr != nil {
				t.Fatalf("read config: %v", readErr)
			}
			if !strings.Contains(string(output), test.wantOutput) {
				t.Fatalf("output = %s, want %q", output, test.wantOutput)
			}
			if requests.Load() != 1 {
				t.Fatalf("HTTP requests = %d, want 1", requests.Load())
			}
			if test.mode == "bare-401" && (strings.Contains(string(output), "gateway_auth_failed") ||
				strings.Contains(string(output), "auth_domain=") || strings.Contains(string(output), "doops login")) {
				t.Fatalf("bare 401 invented an authentication diagnosis: %s", output)
			}

			if test.wantRemove {
				if err != nil {
					t.Fatalf("offline remove failed: %v\n%s", err, output)
				}
				var config RootConfig
				if err := json.Unmarshal(after, &config); err != nil {
					t.Fatalf("decode removed config: %v", err)
				}
				if !reflect.DeepEqual(config.Servers, original.Servers[1:]) || config.LLM != original.LLM {
					t.Fatalf("remove did not preserve unrelated config: %#v", config)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s remove unexpectedly succeeded: %s", test.name, output)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("%s changed config:\nbefore=%s\nafter=%s", test.name, before, after)
			}
		})
	}
}

func newGatewayTargetProbeServer(t *testing.T, mode, wantSession string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer synthetic-token" {
			t.Errorf("authorization = %q, want synthetic token", got)
		}
		if r.URL.Path != "/v1/rpc" || r.URL.Query().Get("cluster") != "dev" ||
			r.URL.Query().Get("instance") != "node" {
			t.Errorf("unexpected probe route: %s", r.URL)
			http.Error(w, "unexpected route", http.StatusBadRequest)
			return
		}
		switch mode {
		case "wrong-scope":
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		case "bare-401":
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		case "structured-401":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"gateway_auth_failed","auth_domain":"gateway","message":"token rejected","next_action":"Run doops login"}`))
			return
		case "unavailable":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade probe websocket: %v", err)
			return
		}
		defer conn.Close()

		for {
			var request map[string]interface{}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			switch request["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]interface{}{"protocolVersion": "2024-11-05"},
				})
			case "tools/call":
				params, _ := request["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				if params["name"] != "doops_node_info" {
					t.Errorf("probe tool = %v, want doops_node_info", params["name"])
				}
				got, _ := args["session_id"].(string)
				if strings.TrimSpace(wantSession) == "" {
					// An omitted session is generated by the CLI.
					if !regexp.MustCompile(`^ses_[0-9a-f]{16}$`).MatchString(got) {
						t.Errorf("probe session_id = %q, want a generated ses_<16 hex>", got)
					}
				} else if got != wantSession {
					t.Errorf("probe session_id = %q, want %q", got, wantSession)
				}
				if mode == "disconnect" {
					return
				}
				code, message := float64(-32004), "target offline: dev/node"
				switch mode {
				case "info-forbidden":
					code, message = -32003, "forbidden: info on dev/node"
				case "wrong-code":
					code = -32603
				case "fractional-code":
					code = -32004.5
				case "wrong-target":
					message = "target offline: dev/other"
				}
				if mode != "online" && mode != "tool-error" {
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      request["id"],
						"error":   map[string]interface{}{"code": code, "message": message},
					})
					continue
				}
				text := "synthetic node info"
				if mode == "tool-error" {
					text = "remote error: target offline: dev/node"
				}
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]interface{}{
						"isError": mode == "tool-error",
						"content": []interface{}{map[string]interface{}{"type": "text", "text": text}},
					},
				})
			default:
				t.Errorf("unexpected probe method %v", fmt.Sprint(request["method"]))
				return
			}
		}
	}))
	return gateway, requests
}
