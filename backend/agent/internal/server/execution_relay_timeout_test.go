package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symansun/doops/backend/agent/api"
)

func TestExecutionRelayBufferedOutputInterruption(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "continuous", "success"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 80 * time.Millisecond
			if mode == "cancel" || mode == "success" {
				timeout = 5 * time.Second
			}
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{OperationTimeout: timeout})
			remote := f.agent(t, "buffered-output-"+mode)
			auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			var forwarded atomic.Int64
			var chunks []string
			var response map[string]interface{}
			write := func(raw []byte) error {
				var message map[string]interface{}
				if err := json.Unmarshal(raw, &message); err != nil {
					return err
				}
				if message["method"] == "notifications/message" {
					enterOnce.Do(func() { close(entered) })
					<-release
					if mode == "continuous" {
						time.Sleep(3 * time.Millisecond)
					}
					params := message["params"].(map[string]interface{})
					chunks = append(chunks, params["data"].(string))
					forwarded.Add(1)
				} else {
					response = message
				}
				return nil
			}
			finished := make(chan struct{})
			var callErr error
			go func() {
				defer close(finished)
				callErr = f.hub.client.handleGatewayToolCall(ctx, auth, "dev", "local", api.JSONRPCRequest{
					JSONRPC: "2.0", ID: 2, Method: "tools/call",
					Params: json.RawMessage(`{"name":"doops_shell","arguments":{"session_id":"buffered-output","command":"synthetic streaming operation"}}`),
				}, write, func(value interface{}) error {
					raw, err := json.Marshal(value)
					if err != nil {
						return err
					}
					return write(raw)
				})
			}()
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				_ = remote.conn.Close()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("Gateway call did not stop")
				}
				requireEventually(t, time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
			})
			call := remote.call(t, "doops_shell", "buffered-output")
			op := f.operation(t, "buffered-output", "running")
			sendChunk := func(text string) error {
				return remote.write(map[string]interface{}{
					"jsonrpc": "2.0", "method": "notifications/message",
					"params": map[string]interface{}{"request_id": call.id, "data": text},
				})
			}
			if err := sendChunk("first"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("output forwarding did not start")
			}
			agent := f.hub.getAgent("dev", "local")
			agent.pendingMu.Lock()
			queue := agent.pending[call.id]
			agent.pendingMu.Unlock()
			for i := 0; i < 16; i++ {
				if err := sendChunk(fmt.Sprintf("buffered-%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			requireEventually(t, time.Second, func() bool {
				queue.mu.Lock()
				defer queue.mu.Unlock()
				return len(queue.items) >= 16
			})

			if mode == "continuous" {
				stop, stopped := make(chan struct{}), make(chan struct{})
				go func() {
					defer close(stopped)
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-stop:
							return
						case <-ticker.C:
							if err := sendChunk("continuing"); err != nil {
								return
							}
						}
					}
				}()
				defer func() { close(stop); <-stopped }()
			}
			switch mode {
			case "timeout":
				time.Sleep(2 * timeout)
			case "cancel":
				cancel()
			case "success":
				remote.complete(t, call, "")
				select {
				case <-queue.terminal:
				case <-time.After(time.Second):
					t.Fatal("normal terminal was not received")
				}
			}
			releaseOnce.Do(func() { close(release) })

			if mode != "success" {
				select {
				case id := <-remote.cancels:
					if id != call.id {
						t.Fatalf("canceled request %d, want %d", id, call.id)
					}
				case <-time.After(time.Second):
					t.Fatal("buffered output prevented cancellation")
				}
				if mode != "continuous" && forwarded.Load() != 1 {
					t.Errorf("forwarded %d chunks after interruption; want only the in-flight chunk", forwarded.Load())
				}
				// A later frame proves the cancel ACK has passed through readLoop.
				queue.mu.Lock()
				queued := len(queue.items)
				queue.mu.Unlock()
				if err := sendChunk("after-cancel-ack"); err != nil {
					t.Fatal(err)
				}
				requireEventually(t, time.Second, func() bool {
					queue.mu.Lock()
					defer queue.mu.Unlock()
					return len(queue.items) > queued
				})
				select {
				case <-queue.terminal:
					t.Fatal("cancel ACK became original terminal evidence")
				case <-finished:
					t.Fatal("cancel ACK finished the operation")
				default:
				}
				if event := executionAuditTestEvent(t, f.store, op.AuditID); event.Status != "executing" {
					t.Fatalf("cancel ACK retired audit: %s", event.Status)
				}
				lease, err := agent.acquireActionLeaseForSession(context.Background(), ActionWrite,
					"workspace:buffered-output", "buffered-output", 0, time.Second)
				if err == nil {
					lease.Release()
					t.Fatal("cancel ACK released the workspace exclusion")
				}
				// Even a successful late terminal cannot erase the interruption.
				remote.complete(t, call, "")
			}
			select {
			case <-finished:
				if callErr != nil {
					t.Fatal(callErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Gateway did not finish after original terminal")
			}
			wantStatus := "outcome_unknown"
			if mode == "success" {
				wantStatus = "success"
				if response["error"] != nil || response["result"] == nil || forwarded.Load() != 17 {
					t.Fatalf("normal output/result changed: forwarded=%d response=%#v", forwarded.Load(), response)
				}
				for i, chunk := range chunks {
					want := "first"
					if i > 0 {
						want = fmt.Sprintf("buffered-%d", i-1)
					}
					if chunk != want {
						t.Fatalf("output chunk %d=%q, want %q", i, chunk, want)
					}
				}
			} else if !executionResponseUnknown(response) {
				t.Fatalf("interrupted mutation reported a known result: %#v", response)
			}
			if event := executionAuditTestEvent(t, f.store, op.AuditID); event.Status != wantStatus {
				t.Fatalf("audit=%s, want %s", event.Status, wantStatus)
			}
			if len(remote.calls) != 0 || len(remote.cancels) != 0 {
				t.Fatalf("operation replayed or canceled twice: calls=%d cancels=%d", len(remote.calls), len(remote.cancels))
			}
			lease, err := agent.acquireActionLeaseForSession(context.Background(), ActionWrite,
				"workspace:buffered-output", "buffered-output", 0, time.Second)
			if err != nil {
				t.Fatalf("original terminal did not release the workspace: %v", err)
			}
			lease.Release()
			t.Logf("mode=%s forwarded=%d audit=%s; no replay", mode, forwarded.Load(), wantStatus)
		})
	}
}
