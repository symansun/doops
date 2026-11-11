package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCOSPushUsesResolvedSessionForEveryRequest(t *testing.T) {
	for _, tc := range []struct {
		name, session, dest, expected string
	}{
		{name: "generated"},
		{name: "destination", dest: "/root/ws/destination-session", expected: "destination-session"},
		{name: "explicit", session: "explicit-session", dest: "/root/ws/ignored", expected: "explicit-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			t.Setenv("DOOPS_CLI_CALL_TIMEOUT", "5s")
			captured := make(chan []map[string]interface{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var calls []map[string]interface{}
				defer func() { captured <- calls }()
				for {
					var request struct {
						ID     interface{} `json:"id"`
						Method string      `json:"method"`
						Params struct {
							Arguments map[string]interface{} `json:"arguments"`
						} `json:"params"`
					}
					if conn.ReadJSON(&request) != nil {
						return
					}
					result := map[string]interface{}{"capabilities": map[string]interface{}{}}
					if request.Method == "tools/call" {
						calls = append(calls, request.Params.Arguments)
						result = map[string]interface{}{"structuredContent": map[string]interface{}{"storage": "cos", "workspace_status": "synced"}}
					}
					if conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
						return
					}
				}
			}))
			defer srv.Close()
			src := t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("session fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			commit, err := pushCOS(Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}, src, tc.dest, false, tc.session, "", "synthetic-token", nil)
			if err != nil {
				t.Fatal(err)
			}
			var calls []map[string]interface{}
			select {
			case calls = <-captured:
			case <-time.After(5 * time.Second):
				t.Fatal("COS transport did not close after push")
			}
			if len(calls) < 5 || calls[0]["operation"] != "probe" || calls[len(calls)-1]["operation"] != "sync" {
				t.Fatalf("incomplete push: %#v", calls)
			}
			expected := tc.expected
			if expected == "" {
				expected, _ = calls[0]["session_id"].(string)
				if !strings.HasPrefix(expected, "ses_") {
					t.Fatalf("generated session = %q", expected)
				}
			}
			for _, call := range calls {
				if call["session_id"] != expected {
					t.Fatalf("%v session = %v, want %q", call["operation"], call["session_id"], expected)
				}
			}
			if calls[len(calls)-1]["path"] != commit {
				t.Fatalf("sync commit differs from pushed commit %s", commit)
			}
		})
	}
}
