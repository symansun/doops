package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestPrintFinalResult(t *testing.T) {
	cases := []struct {
		tool                      string
		streamedAny, streamedText bool
		want                      bool
	}{
		{"doops_agent_prompt", false, false, true},
		{"doops_agent_prompt", true, false, true}, // only tool markers streamed
		{"doops_agent_prompt", true, true, false}, // answer already streamed
		{"doops_shell", false, false, true},
		{"doops_shell", true, false, false},
		{"doops_bg", true, false, true},
		{"doops_distill", true, true, true},
	}
	for _, tc := range cases {
		if got := printFinalResult(tc.tool, tc.streamedAny, tc.streamedText); got != tc.want {
			t.Errorf("printFinalResult(%s, any=%t, answer=%t) = %t, want %t",
				tc.tool, tc.streamedAny, tc.streamedText, got, tc.want)
		}
	}
}

// ask must print its answer exactly once and keep tool markers off the answer line.
func TestAskPrintsStreamedAnswerOnce(t *testing.T) {
	const answer = "## 总结\nkz-ops 只有一个常驻 Pod。"
	cases := []struct {
		name          string
		notifications []map[string]interface{}
		final         string
		wantAnswers   int
		wantToolLine  bool
	}{
		{
			name: "streamed answer is not repeated by the final result",
			notifications: []map[string]interface{}{
				{"kind": "tool", "data": "[tool:mcp_doops_k8s_read]"},
				{"kind": "assistant_delta", "data": answer},
			},
			final: answer, wantAnswers: 1, wantToolLine: true,
		},
		{
			name: "final result is printed when only tool markers streamed",
			notifications: []map[string]interface{}{
				{"kind": "tool", "data": "[tool:mcp_doops_k8s_read]"},
			},
			final: answer, wantAnswers: 1, wantToolLine: true,
		},
		{
			name:  "final result is printed when nothing streamed",
			final: answer, wantAnswers: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					var req map[string]interface{}
					if err := conn.ReadJSON(&req); err != nil {
						return
					}
					if req["method"] == "initialize" {
						_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"],
							"result": map[string]interface{}{"capabilities": map[string]interface{}{}}})
						continue
					}
					for _, note := range tc.notifications {
						params := map[string]interface{}{"request_id": req["id"]}
						for k, v := range note {
							params[k] = v
						}
						_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0",
							"method": "notifications/message", "params": params})
					}
					_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"],
						"result": map[string]interface{}{"content": []map[string]string{{"type": "text", "text": tc.final}}}})
				}
			}))
			defer gateway.Close()
			client := newGatewayTestClient(t, gateway.URL, "ask-output")
			defer client.Close()

			output := captureStdout(t, func() {
				if err := client.Call("doops_agent_prompt", map[string]interface{}{"instruction": "x"}); err != nil {
					t.Fatalf("ask: %v", err)
				}
			})
			if got := strings.Count(output, "kz-ops 只有一个常驻 Pod。"); got != tc.wantAnswers {
				t.Fatalf("answer printed %d times, want %d:\n%s", got, tc.wantAnswers, output)
			}
			if tc.wantToolLine && !strings.Contains(output, "[tool:mcp_doops_k8s_read]\n") {
				t.Fatalf("tool marker is not on its own line:\n%s", output)
			}
			if strings.Contains(output, "][tool:") || strings.Contains(output, "]##") {
				t.Fatalf("tool marker ran into other output:\n%s", output)
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		done <- buf.String()
	}()
	defer func() { os.Stdout = original }()
	fn()
	_ = writer.Close()
	os.Stdout = original
	return <-done
}
