package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestRelayConfirmedAdmissionReplySurvivesInterruption(t *testing.T) {
	testRelayAdmissionReplySurvivesInterruption(t, []string{
		doagentPromptQueued, doagentPromptNoPrompt, "doagent_prompt_rejected",
		doagentPromptBusy, doagentPromptAdmissionUnsupported, doagentPromptAdmissionUnavailable,
	}, false)
}

func TestRelayUnknownAdmissionReplySurvivesInterruption(t *testing.T) {
	testRelayAdmissionReplySurvivesInterruption(t, []string{
		doagentPromptCleanupUnknown, "execution_outcome_unknown: prompt_transport_failed",
	}, true)
}

func testRelayAdmissionReplySurvivesInterruption(t *testing.T, categories []string, unknown bool) {
	t.Helper()
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	for _, category := range categories {
		for _, operation := range []string{"ask", "apply"} {
			for _, interruption := range []string{"cancel", "deadline"} {
				for _, ordering := range []string{"terminal_before_interrupt", "terminal_after_cancel_ack"} {
					t.Run(category+"/"+operation+"/"+interruption+"/"+ordering, func(t *testing.T) {
						remote := f.agent(t, "relay-admission-cancel")
						agent := f.hub.getAgent("dev", "local")
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						timeout := 5 * time.Second
						if interruption == "deadline" {
							if ordering == "terminal_before_interrupt" {
								var deadlineCancel context.CancelFunc
								ctx, deadlineCancel = context.WithTimeout(ctx, time.Second)
								defer deadlineCancel()
							} else {
								timeout = 80 * time.Millisecond
							}
						}
						args, err := json.Marshal(map[string]interface{}{
							"session_id": "relay-admission-cancel", "operation": operation,
						})
						if err != nil {
							t.Fatal(err)
						}
						entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
						var releaseOnce sync.Once
						var replies []gatewayWSMessage
						var relayErr error
						go func() {
							defer close(finished)
							relayErr = agent.relayToolCall(ctx, api.ToolCallParams{
								Name: "doops_agent_prompt", Arguments: args,
							}, timeout, func(msg gatewayWSMessage) error {
								if msg.Parsed["method"] != nil {
									close(entered)
									<-release
								} else {
									replies = append(replies, msg)
								}
								return nil
							})
						}()
						t.Cleanup(func() {
							releaseOnce.Do(func() { close(release) })
							cancel()
							_ = remote.conn.Close()
							select {
							case <-finished:
							case <-time.After(3 * time.Second):
								t.Error("relay did not stop")
							}
							requireEventually(t, time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
						})
						call := remote.call(t, "doops_agent_prompt", "relay-admission-cancel")
						agent.pendingMu.Lock()
						queue := agent.pending[call.id]
						agent.pendingMu.Unlock()
						if queue == nil {
							t.Fatal("dispatched call has no pending queue")
						}
						code := -32603
						if category == doagentPromptNoPrompt {
							code = -32602
						} else if category == "doagent_prompt_rejected" {
							code = -32003
						}
						reply := map[string]interface{}{
							"jsonrpc": "2.0", "id": float64(call.id),
							"error": map[string]interface{}{"code": float64(code), "message": category},
						}
						sendTerminal := func() {
							if err := remote.write(reply); err != nil {
								t.Fatal(err)
							}
							select {
							case <-queue.terminal:
							case <-time.After(time.Second):
								t.Fatal("original terminal was not observed")
							}
						}
						if ordering == "terminal_before_interrupt" {
							if err := remote.write(map[string]interface{}{
								"jsonrpc": "2.0", "method": "notifications/message",
								"params": map[string]interface{}{"request_id": call.id, "data": "barrier"},
							}); err != nil {
								t.Fatal(err)
							}
							select {
							case <-entered:
							case <-time.After(time.Second):
								t.Fatal("relay did not reach output barrier")
							}
							sendTerminal()
							if interruption == "cancel" {
								cancel()
							} else {
								<-ctx.Done()
							}
							releaseOnce.Do(func() { close(release) })
						} else {
							if interruption == "cancel" {
								cancel()
							}
							select {
							case id := <-remote.cancels:
								if id != call.id {
									t.Fatalf("canceled %d, want original request %d", id, call.id)
								}
							case <-time.After(time.Second):
								t.Fatal("relay did not request cancellation")
							}
							// The next frame is a barrier proving readLoop consumed
							// the cancel ACK before we inspect ownership.
							if err := remote.write(map[string]interface{}{
								"jsonrpc": "2.0", "method": "notifications/message",
								"params": map[string]interface{}{"request_id": call.id, "data": "after-cancel-ack"},
							}); err != nil {
								t.Fatal(err)
							}
							requireEventually(t, time.Second, func() bool {
								queue.mu.Lock()
								defer queue.mu.Unlock()
								return len(queue.items) != 0
							})
							select {
							case <-queue.terminal:
								t.Fatal("cancel ACK was treated as the original terminal")
							case <-finished:
								t.Fatal("cancel ACK finished the relay")
							default:
							}
							sendTerminal()
						}
						select {
						case <-finished:
						case <-time.After(3 * time.Second):
							t.Fatal("relay did not finish after the original terminal")
						}
						if unknown {
							wantReason := category
							if category == doagentPromptCleanupUnknown {
								wantReason = "execution_outcome_unknown: " + category
							}
							if !errors.Is(relayErr, errExecutionOutcomeUnknown) || relayErr.Error() != wantReason {
								t.Fatalf("unknown terminal lost its reason: got %v, want %q", relayErr, wantReason)
							}
							if len(replies) != 0 {
								t.Fatalf("unknown terminal forwarded as a known failure: %#v", replies)
							}
						} else {
							if relayErr != nil {
								t.Fatalf("confirmed admission reply replaced by interruption: %v", relayErr)
							}
							if len(replies) != 1 || !reflect.DeepEqual(replies[0].Parsed, reply) {
								t.Fatalf("terminal replies = %#v, want exactly %#v", replies, reply)
							}
						}
						agent.pendingMu.Lock()
						pending, active, sessions := len(agent.pending), len(agent.activeByRequest), len(agent.activeSessions)
						agent.pendingMu.Unlock()
						if pending != 0 || active != 0 || sessions != 0 {
							t.Fatalf("relay retained routing state: pending=%d active=%d sessions=%d", pending, active, sessions)
						}
						if len(remote.calls) != 0 || len(remote.cancels) != 0 {
							t.Fatal("request was replayed or canceled more than once")
						}
					})
				}
			}
		}
	}
}
