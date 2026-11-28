package server

// INT-82a: 执行记录持久化 + 按执行 ID 取消。
//
// ① 同进程内 reopen store（新的 GatewayStore + 新的 GatewayHub，同一个 SQLite
// 文件）后，GET /v1/executions/<id> 仍然能读回终态；重启时仍在 running 的记录
// 恢复为明确的失败，而不是永远 running。
// ③ POST /v1/executions/<id>/cancel 把取消经隧道投给正在跑该执行的 Agent；
// 已终态幂等返回当前状态、不改历史；不存在 404；Agent 离线 409 + retryable。
// 最后一条用例走真两端（真 Agent 运行时 + 真 Gateway + 真 store），客户端断线后
// 取消并要求终态 canceled。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// executionFixtureUserID resolves the user behind a fixture token: CreatedToken
// names the token, not the user, and owner checks run on the user ID.
func executionFixtureUserID(t *testing.T, f *executionLifecycleFixture, token CreatedToken) string {
	t.Helper()
	auth, err := f.store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("verify fixture token %s: %v", token.Name, err)
	}
	return auth.UserID
}

func executionCancelRequest(t *testing.T, f *executionLifecycleFixture, token CreatedToken, executionID string) (int, []byte) {
	t.Helper()
	path := "/v1/executions/" + url.PathEscape(executionID) + "/cancel"
	req, err := http.NewRequest(http.MethodPost, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Plaintext)
	client := &http.Client{Timeout: f.httpClientTimeout()}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("POST %s: read body: %v", path, err)
	}
	return resp.StatusCode, data
}

func executionSucceededTerminal(id int, text string) json.RawMessage {
	raw, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"content": []interface{}{map[string]interface{}{"type": "text", "text": text}},
		},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

type executionCancelBody struct {
	Canceled  bool   `json:"canceled"`
	Error     string `json:"error"`
	Retryable bool   `json:"retryable"`
	Execution struct {
		ExecutionID string          `json:"execution_id"`
		Status      string          `json:"status"`
		Error       string          `json:"error"`
		Result      json.RawMessage `json:"result"`
	} `json:"execution"`
}

func decodeExecutionCancelBody(t *testing.T, body []byte) executionCancelBody {
	t.Helper()
	var out executionCancelBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode cancel body: %v; %s", err, body)
	}
	return out
}

// TestExecutionRecordSurvivesGatewayRestart is the ① acceptance: the same process
// closes its store and opens a new one at the same path, builds a new hub, and the
// execution is still addressable by ID.
func TestExecutionRecordSurvivesGatewayRestart(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	operator := f.user(t, "restart-op", ScopeGrant{Cluster: executionResumeCluster, Instance: executionResumeInstance})
	registry := f.hub.client.executions

	if _, created := registry.begin("exec-restart-done", "doops_shell", executionResumeCluster, executionResumeInstance, executionFixtureUserID(t, f, operator)); !created {
		t.Fatalf("the fixture already knows exec-restart-done")
	}
	registry.finish("exec-restart-done", executionStatusSucceeded, "", executionSucceededTerminal(2, "Operation complete."))
	if _, created := registry.begin("exec-restart-running", "doops_shell", executionResumeCluster, executionResumeInstance, executionFixtureUserID(t, f, operator)); !created {
		t.Fatalf("the fixture already knows exec-restart-running")
	}

	before := decodeExecutionResumeRecord(t, func() []byte {
		status, body := executionResumeQuery(t, f, operator, "exec-restart-done", 0)
		if status != http.StatusOK {
			t.Fatalf("before restart: HTTP %d: %s", status, body)
		}
		return body
	}())
	if before.Execution.Status != executionStatusSucceeded {
		t.Fatalf("before restart: status = %q, want succeeded: %v", before.Execution.Status, before)
	}

	// Same process, same file, new store and new hub: the record has to come back
	// from execution_records, not from the old registry's map.
	f.open(t)

	status, body := executionResumeQuery(t, f, operator, "exec-restart-done", 0)
	if status != http.StatusOK {
		t.Fatalf("after restart: HTTP %d, want 200: %s", status, body)
	}
	after := decodeExecutionResumeRecord(t, body)
	if after.Execution.Status != executionStatusSucceeded {
		t.Fatalf("after restart: status = %q, want succeeded: %s", after.Execution.Status, body)
	}
	if len(after.Execution.Result) == 0 {
		t.Fatalf("after restart: the terminal payload is gone: %s", body)
	}
	if string(after.Execution.Result) != string(before.Execution.Result) {
		t.Fatalf("after restart: the terminal payload changed: before %s, after %s", before.Execution.Result, after.Execution.Result)
	}

	status, body = executionResumeQuery(t, f, operator, "exec-restart-running", 0)
	if status != http.StatusOK {
		t.Fatalf("running record after restart: HTTP %d, want 200: %s", status, body)
	}
	running := decodeExecutionResumeRecord(t, body)
	if running.Execution.Status != executionStatusFailed {
		t.Fatalf("a record that was running at restart must be a definite failure, got %q: %s", running.Execution.Status, body)
	}
	if !strings.Contains(running.Execution.Error, errExecutionResultUnavailable.Error()) {
		t.Fatalf("running record after restart: error = %q, want the %s category: %s", running.Execution.Error, errExecutionResultUnavailable, body)
	}
}

// TestExecutionCancelContracts covers ③ without a tunnel: unknown ID, a foreign
// grant, an offline Agent while the record is inside its grace, and the
// idempotency rule that a terminal state is never rewritten.
func TestExecutionCancelContracts(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	operator := f.user(t, "cancel-op", ScopeGrant{Cluster: executionResumeCluster, Instance: executionResumeInstance})
	stranger := f.user(t, "cancel-stranger", ScopeGrant{Cluster: "other", Instance: "other"})
	registry := f.hub.client.executions

	status, unknownBody := executionCancelRequest(t, f, operator, "exec-cancel-unknown")
	if status != http.StatusNotFound || !strings.Contains(string(unknownBody), errExecutionNotFound.Error()) {
		t.Fatalf("unknown execution: HTTP %d %s, want 404 %s", status, unknownBody, errExecutionNotFound)
	}

	if _, created := registry.begin("exec-cancel-offline", "doops_shell", executionResumeCluster, executionResumeInstance, executionFixtureUserID(t, f, operator)); !created {
		t.Fatalf("the fixture already knows exec-cancel-offline")
	}
	// INT-100 S1: a non-owner must not learn that the id exists. Both the
	// no-grant stranger and a user granted the same target get the exact bytes
	// a missing execution returns; a 403 would be an existence oracle.
	granted := f.user(t, "cancel-granted", ScopeGrant{Cluster: executionResumeCluster, Instance: executionResumeInstance})
	for name, token := range map[string]CreatedToken{"stranger": stranger, "granted non-owner": granted} {
		code, got := executionCancelRequest(t, f, token, "exec-cancel-offline")
		if code != http.StatusNotFound || !bytes.Equal(got, unknownBody) {
			t.Fatalf("%s cancel: HTTP %d %s, want 404 %s (byte-identical to an unknown id)", name, code, got, unknownBody)
		}
	}
	status, body := executionCancelRequest(t, f, operator, "exec-cancel-offline")
	if status != http.StatusConflict {
		t.Fatalf("cancelling with no Agent must be 409, got HTTP %d: %s", status, body)
	}
	offline := decodeExecutionCancelBody(t, body)
	if offline.Error != errExecutionAgentOffline.Error() || !offline.Retryable {
		t.Fatalf("offline cancel must stay retryable: %s", body)
	}
	if snapshot, ok := registry.get("exec-cancel-offline"); !ok || snapshot.status != executionStatusRunning {
		t.Fatalf("a failed cancel must not change the record: %+v", snapshot)
	}

	registry.finish("exec-cancel-offline", executionStatusSucceeded, "", executionSucceededTerminal(3, "done"))
	status, body = executionCancelRequest(t, f, operator, "exec-cancel-offline")
	if status != http.StatusOK {
		t.Fatalf("cancel after the terminal state: HTTP %d, want 200: %s", status, body)
	}
	terminal := decodeExecutionCancelBody(t, body)
	if terminal.Canceled || terminal.Execution.Status != executionStatusSucceeded {
		t.Fatalf("a cancel after success must report succeeded and keep the history: %s", body)
	}
	again := decodeExecutionCancelBody(t, executionCancelRequestSecond(t, f, operator, "exec-cancel-offline"))
	if again.Execution.Status != executionStatusSucceeded || string(again.Execution.Result) != string(terminal.Execution.Result) {
		t.Fatalf("the second cancel rewrote the terminal state: first %s, second %+v", body, again)
	}

	// A canceled record answers the same way every time.
	if _, created := registry.begin("exec-cancel-twice", "doops_shell", executionResumeCluster, executionResumeInstance, executionFixtureUserID(t, f, operator)); !created {
		t.Fatalf("the fixture already knows exec-cancel-twice")
	}
	registry.finish("exec-cancel-twice", executionStatusCanceled, "execution canceled by request", nil)
	first := decodeExecutionCancelBody(t, executionCancelRequestSecond(t, f, operator, "exec-cancel-twice"))
	second := decodeExecutionCancelBody(t, executionCancelRequestSecond(t, f, operator, "exec-cancel-twice"))
	if !first.Canceled || !second.Canceled || first.Execution.Status != executionStatusCanceled || second.Execution.Status != executionStatusCanceled {
		t.Fatalf("cancel is not idempotent: first %+v, second %+v", first, second)
	}
}

func executionCancelRequestSecond(t *testing.T, f *executionLifecycleFixture, token CreatedToken, executionID string) []byte {
	t.Helper()
	status, body := executionCancelRequest(t, f, token, executionID)
	if status != http.StatusOK {
		t.Fatalf("POST /v1/executions/%s/cancel: HTTP %d: %s", executionID, status, body)
	}
	return body
}

// TestExecutionCancelAfterClientDisconnect is the ③ end-to-end acceptance on a
// real Agent runtime: the client drops its socket mid-execution, the execution
// keeps running (INT-72), the client cancels by execution ID, and the terminal
// state becomes canceled on both ends.
func TestExecutionCancelAfterClientDisconnect(t *testing.T) {
	f, operator := executionResumeEnv(t, 30*time.Second)
	agent := newExecutionResumeE2EAgent(t, f)
	_ = agent
	conn := f.client(t, operator)

	marker := filepath.Join(t.TempDir(), "cancel-marker.log")
	markerArg := fmt.Sprintf("%q", marker)
	command := fmt.Sprintf("printf 'run\\n' >> %s; sleep 60; printf 'done\\n' >> %s", markerArg, markerArg)
	executionID := "exec-cancel-e2e"
	if err := conn.WriteJSON(executionResumeShellCall(2, command, executionID)); err != nil {
		t.Fatalf("send the long execution: %v", err)
	}
	waitMarker := func(want string, count int, timeout time.Duration) {
		t.Helper()
		requireEventually(t, timeout, func() bool { return executionResumeMarkerCount(t, marker, want) >= count })
	}
	waitMarker("run", 1, 30*time.Second)

	// The client goes away. With an execution ID the call is detached from the
	// socket (INT-72), so the command keeps running on the Agent.
	if err := conn.Close(); err != nil {
		t.Fatalf("close the client connection: %v", err)
	}

	status, body := executionCancelRequest(t, f, operator, executionID)
	if status != http.StatusOK {
		t.Fatalf("cancel after the client disconnected: HTTP %d: %s", status, body)
	}
	canceled := decodeExecutionCancelBody(t, body)
	if !canceled.Canceled || canceled.Execution.Status != executionStatusCanceled {
		t.Fatalf("cancel after the client disconnected: got %s, want a canceled execution", body)
	}

	status, body = executionResumeQuery(t, f, operator, executionID, 5000)
	if status != http.StatusOK {
		t.Fatalf("GET after cancel: HTTP %d: %s", status, body)
	}
	record := decodeExecutionResumeRecord(t, body)
	if record.Execution.Status != executionStatusCanceled {
		t.Fatalf("GET after cancel: status = %q, want canceled: %s", record.Execution.Status, body)
	}

	// The process was really stopped: the sleep never writes its tail.
	time.Sleep(1500 * time.Millisecond)
	if count := executionResumeMarkerCount(t, marker, "done"); count != 0 {
		t.Fatalf("the canceled command still finished: the tail was written %d time(s)", count)
	}
	if count := executionResumeMarkerCount(t, marker, "run"); count != 1 {
		t.Fatalf("the command started %d times, want exactly once", count)
	}

	second := decodeExecutionCancelBody(t, executionCancelRequestSecond(t, f, operator, executionID))
	if !second.Canceled || second.Execution.Status != executionStatusCanceled {
		t.Fatalf("the second cancel changed the canceled terminal state: %s", body)
	}
}

// TestExecutionRecordsRoundTripOnMySQL is the same persistence contract on the
// native MySQL backend: the frozen DDL, the upsert and the restore query all run
// against a real MySQL 8.4 when the operator supplies one.
func TestExecutionRecordsRoundTripOnMySQL(t *testing.T) {
	if testing.Short() {
		t.Skip("skip the MySQL round trip in -short")
	}
	store := openGatewayMySQLCredentialE2EStore(t)
	now := time.Now()
	first := newExecutionRegistry(time.Minute)
	if err := first.restore(store, now); err != nil {
		t.Fatalf("restore on an empty MySQL store: %v", err)
	}
	if _, created := first.begin("exec-mysql-done", "doops_shell", executionResumeCluster, executionResumeInstance, "op-mysql"); !created {
		t.Fatalf("MySQL already knows exec-mysql-done")
	}
	first.finish("exec-mysql-done", executionStatusSucceeded, "", executionSucceededTerminal(4, "mysql"))
	if _, created := first.begin("exec-mysql-running", "doops_shell", executionResumeCluster, executionResumeInstance, "op-mysql"); !created {
		t.Fatalf("MySQL already knows exec-mysql-running")
	}

	second := newExecutionRegistry(time.Minute)
	if err := second.restore(store, time.Now()); err != nil {
		t.Fatalf("restore from MySQL: %v", err)
	}
	done, ok := second.get("exec-mysql-done")
	if !ok || done.status != executionStatusSucceeded {
		t.Fatalf("exec-mysql-done after reopen on MySQL: %+v (ok=%v)", done, ok)
	}
	if len(done.terminal) == 0 {
		t.Fatalf("the MySQL round trip dropped the terminal payload")
	}
	running, ok := second.get("exec-mysql-running")
	if !ok || running.status != executionStatusFailed {
		t.Fatalf("a running record must restore as failed on MySQL: %+v (ok=%v)", running, ok)
	}
	if !strings.Contains(running.errText, errExecutionResultUnavailable.Error()) {
		t.Fatalf("restored running record error = %q, want the %s category", running.errText, errExecutionResultUnavailable)
	}
	if err := store.purgeExecutionRecords(time.Now().Add(3 * time.Minute)); err != nil {
		t.Fatalf("purge expired MySQL execution records: %v", err)
	}
}

var _ = os.Getenv
