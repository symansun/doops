package server

// INT-100 O1: a terminal record whose terminal bytes were never stored (the
// Gateway restarted while it was running, then converged it to "failed") must
// replay the error GET already shows, not claim the stored state is unreadable.
// Truly unreadable bytes (a stored payload that does not parse) keep the old
// wording, so the two cases stay distinguishable.

import (
	"context"
	"encoding/json"
	"testing"
)

type replayErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// replayTerminalForTest drives replayExecutionRecord with an in-memory record
// and returns the JSON-RPC error it wrote.
func replayTerminalForTest(t *testing.T, f *executionLifecycleFixture, operator CreatedToken, record executionSnapshot) replayErrorBody {
	t.Helper()
	auth, err := f.store.VerifyUserToken(operator.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	var body replayErrorBody
	writeJSON := func(v interface{}) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &body)
	}
	writeRaw := func(raw []byte) error {
		t.Fatalf("a failed terminal without bytes must not replay as a result: %s", raw)
		return nil
	}
	if err := f.hub.client.replayExecutionRecord(context.Background(), 1, auth, record, writeRaw, writeJSON); err != nil {
		t.Fatalf("replayExecutionRecord: %v", err)
	}
	return body
}

func TestExecutionReplayTerminalWithoutBytesUsesRecordError(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	operator := f.user(t, "o1-owner", ScopeGrant{Cluster: executionResumeCluster, Instance: executionResumeInstance})
	const restartError = "execution_result_unavailable: the Gateway restarted while the execution was running"
	record := executionSnapshot{
		id: "exec-o1-restart", ownerID: executionFixtureUserID(t, f, operator),
		cluster: executionResumeCluster, instance: executionResumeInstance,
		status: executionStatusFailed, errText: restartError,
	}

	body := replayTerminalForTest(t, f, operator, record)
	if body.Error.Code != -32009 {
		t.Fatalf("replay of a failed record: code = %d, want -32009", body.Error.Code)
	}
	if body.Error.Message != restartError {
		t.Fatalf("replay of a failed record without stored terminal bytes:\n got %q\nwant %q (the same text GET /v1/executions/<id> reports)", body.Error.Message, restartError)
	}
}

func TestExecutionReplayUnparseableTerminalStaysUnreadable(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	operator := f.user(t, "o1-owner-bytes", ScopeGrant{Cluster: executionResumeCluster, Instance: executionResumeInstance})
	record := executionSnapshot{
		id: "exec-o1-corrupt", ownerID: executionFixtureUserID(t, f, operator),
		cluster: executionResumeCluster, instance: executionResumeInstance,
		status: executionStatusFailed, errText: "the tool failed",
		terminal: json.RawMessage("{not json"),
	}

	body := replayTerminalForTest(t, f, operator, record)
	if body.Error.Code != -32009 || body.Error.Message != "execution_result_unavailable: the stored terminal state is unreadable" {
		t.Fatalf("unparseable stored terminal: got %+v, want the -32009 unreadable message", body.Error)
	}
}
