package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentAdmissionStatusReadsRuntimeWithoutPrompt(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		reply map[string]interface{}
		error string
	}{
		{"busy", map[string]interface{}{"result": map[string]interface{}{
			"admission": map[string]interface{}{"current": map[string]interface{}{
				"limit": 2, "inUse": 2, "available": 0,
				"holders": []interface{}{map[string]interface{}{"sessionId": "active", "turnId": "t2"}},
				"queues":  []interface{}{map[string]interface{}{"sessionId": "waiting", "queued": 1, "oldestWaitSecs": 14}},
			}},
		}}, ""},
		{"old_runtime", map[string]interface{}{"result": map[string]interface{}{"admission": map[string]interface{}{"accepted": 30}}}, "doagent_admission_status_unsupported"},
		{"runtime_error", map[string]interface{}{"error": map[string]interface{}{"code": -32000, "message": "GC subsystem not initialized"}}, "doagent_admission_status_unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			calls := 0
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				calls++
				if request["method"] != "runtime/gcStats" {
					t.Errorf("diagnosis invoked %v", request["method"])
				}
				_ = json.NewEncoder(w).Encode(scenario.reply)
			}))
			defer runtime.Close()
			_, conn, cleanup := dialPromptAdmissionAgent(t, runtime.URL)
			defer cleanup()
			reply := callToolResult(t, conn, "doops_node_info", map[string]interface{}{
				"session_id": "diagnose-admission", "section": "admission",
			})
			if scenario.error != "" {
				_, message := promptWireError(t, reply)
				if message != scenario.error {
					t.Fatalf("error = %q", message)
				}
			} else {
				encoded, _ := json.Marshal(reply["result"])
				for _, marker := range []string{"active", "t2", "waiting", "oldestWaitSecs"} {
					if !strings.Contains(string(encoded), marker) {
						t.Fatalf("diagnosis lost %q: %s", marker, encoded)
					}
				}
			}
			if calls != 1 {
				t.Fatalf("diagnosis made %d RPC calls, want only gcStats", calls)
			}
		})
	}
}
