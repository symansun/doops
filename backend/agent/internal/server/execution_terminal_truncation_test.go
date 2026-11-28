package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// INT-87: a terminal payload larger than executionRecordTerminalLimit is stored
// as a prefix. The read-back must stay parseable and say so, instead of handing
// the caller half a JSON document.

func truncationPayload(size int, text bool) json.RawMessage {
	if text {
		// SSE-ish result: the whole result is one string, not an object.
		return json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"result":%q}`, strings.Repeat("data: x\\n\\n", size/10)))
	}
	return json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"result":{"stdout":%q}}`, strings.Repeat("a", size)))
}

func truncationColumns(t *testing.T, f *executionLifecycleFixture, id string) (truncated int, bytes int64) {
	t.Helper()
	if err := f.store.db.QueryRow(
		`SELECT terminal_truncated, terminal_bytes FROM execution_records WHERE execution_id = ?`, id,
	).Scan(&truncated, &bytes); err != nil {
		t.Fatalf("read truncation columns for %s: %v", id, err)
	}
	return truncated, bytes
}

func TestExecutionTerminalSmallResultIsUnchanged(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true})
	token := f.user(t, "truncation-small", ScopeGrant{Cluster: "dev", Instance: "local"})
	auth, err := f.store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}

	terminal := json.RawMessage(`{"jsonrpc":"2.0","id":3,"result":{"stdout":"hello"}}`)
	if _, created := f.hub.client.executions.begin("exec-small", "doops_shell", "dev", "local", auth.UserID); !created {
		t.Fatalf("begin did not create the record")
	}
	f.hub.client.executions.finish("exec-small", executionStatusSucceeded, "", terminal)

	if truncated, bytes := truncationColumns(t, f, "exec-small"); truncated != 0 {
		t.Fatalf("small result terminal_truncated = %d, want 0", truncated)
	} else if bytes != int64(len(terminal)) {
		t.Fatalf("small result terminal_bytes = %d, want %d", bytes, len(terminal))
	}

	status, body := executionResumeQuery(t, f, token, "exec-small", 0)
	if status != 200 {
		t.Fatalf("GET = %d %s", status, body)
	}
	var decoded struct {
		Execution struct {
			Result json.RawMessage `json:"result"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("read-back is not JSON: %v (%s)", err, body)
	}
	if strings.Contains(string(decoded.Execution.Result), "truncated") {
		t.Fatalf("an untruncated result must keep its shape, got %s", decoded.Execution.Result)
	}

	// The same bytes survive a restart: reopen the store and read again.
	f.open(t)
	status, after := executionResumeQuery(t, f, token, "exec-small", 0)
	if status != 200 {
		t.Fatalf("GET after restart = %d %s", status, after)
	}
	var second struct {
		Execution struct {
			Result json.RawMessage `json:"result"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(after, &second); err != nil {
		t.Fatalf("read-back after restart is not JSON: %v (%s)", err, after)
	}
	if string(second.Execution.Result) != string(decoded.Execution.Result) {
		t.Fatalf("result changed across restart: %s -> %s", decoded.Execution.Result, second.Execution.Result)
	}
}

func TestExecutionTerminalTruncationIsParseable(t *testing.T) {
	for _, text := range []bool{false, true} {
		name := "json_result"
		if text {
			name = "text_result"
		}
		t.Run(name, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{RequireAgentEnrollment: true})
			token := f.user(t, "truncation-large", ScopeGrant{Cluster: "dev", Instance: "local"})
			auth, err := f.store.VerifyUserToken(token.Plaintext)
			if err != nil {
				t.Fatalf("verify token: %v", err)
			}

			terminal := truncationPayload(executionRecordTerminalLimit+64*1024, text)
			if len(terminal) <= executionRecordTerminalLimit {
				t.Fatalf("fixture terminal is only %d bytes", len(terminal))
			}
			id := "exec-large-" + name
			if _, created := f.hub.client.executions.begin(id, "doops_shell", "dev", "local", auth.UserID); !created {
				t.Fatalf("begin did not create the record")
			}
			f.hub.client.executions.finish(id, executionStatusSucceeded, "", terminal)

			if truncated, _ := truncationColumns(t, f, id); truncated != 1 {
				t.Fatalf("terminal_truncated = %d, want 1", truncated)
			}

			// Force the persisted row to be read back: only then does the stored
			// prefix reach the caller.
			f.open(t)
			status, body := executionResumeQuery(t, f, token, id, 0)
			if status != 200 {
				t.Fatalf("GET = %d %s", status, body)
			}
			if !json.Valid(body) {
				t.Fatalf("read-back is not valid JSON: %s", body[:min(len(body), 400)])
			}
			var decoded struct {
				Execution struct {
					Result struct {
						Truncated bool    `json:"truncated"`
						Bytes     int64   `json:"bytes"`
						Terminal  *string `json:"terminal"`
					} `json:"result"`
				} `json:"execution"`
			}
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("wrapper did not unmarshal: %v", err)
			}
			if !decoded.Execution.Result.Truncated {
				t.Fatalf("wrapper truncated = false, want true: %s", body[:min(len(body), 400)])
			}
			if decoded.Execution.Result.Bytes != int64(len(terminal)) {
				t.Fatalf("wrapper bytes = %d, want the original %d", decoded.Execution.Result.Bytes, len(terminal))
			}
			if decoded.Execution.Result.Terminal == nil || len(*decoded.Execution.Result.Terminal) == 0 {
				t.Fatalf("wrapper terminal is empty")
			}
			if !strings.HasPrefix(string(terminal), *decoded.Execution.Result.Terminal) {
				t.Fatalf("wrapper terminal (%d bytes) is not a prefix of the original payload (%d bytes)",
					len(*decoded.Execution.Result.Terminal), len(terminal))
			}
			if len(*decoded.Execution.Result.Terminal) > executionRecordTerminalLimit {
				t.Fatalf("wrapper terminal is %d bytes, want at most %d", len(*decoded.Execution.Result.Terminal), executionRecordTerminalLimit)
			}
		})
	}
}

// The MySQL backend stores the marker in its own columns; the read-back must not
// depend on SQLite.
func TestExecutionTerminalTruncationOnMySQL(t *testing.T) {
	if strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT")) == "" {
		t.Skip("set DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to run the MySQL round-trip")
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	store := openGatewayMySQLCredentialE2EStore(t)
	registry := newExecutionRegistry(time.Minute)
	if err := registry.restore(store, time.Now()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	terminal := truncationPayload(executionRecordTerminalLimit+64*1024, false)
	id := "exec-mysql-trunc-" + suffix
	registry.begin(id, "doops_shell", "dev", "local", "user-mysql-trunc")
	registry.finish(id, executionStatusSucceeded, "", terminal)

	reloaded := newExecutionRegistry(time.Minute)
	if err := reloaded.restore(store, time.Now()); err != nil {
		t.Fatalf("restore: %v", err)
	}
	record, ok := reloaded.get(id)
	if !ok {
		t.Fatalf("record did not come back from MySQL")
	}
	if !record.terminalTruncated {
		t.Fatalf("restored terminalTruncated = false, want true")
	}
	if record.terminalBytes != int64(len(terminal)) {
		t.Fatalf("restored terminalBytes = %d, want %d", record.terminalBytes, len(terminal))
	}
	view := record.view()
	result, ok := view["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("view result = %T, want the wrapper map", view["result"])
	}
	if result["truncated"] != true || result["bytes"] != int64(len(terminal)) {
		t.Fatalf("wrapper = %v, want truncated/bytes", result)
	}
	if _, err := json.Marshal(view); err != nil {
		t.Fatalf("wrapper view is not JSON-serializable: %v", err)
	}
}
