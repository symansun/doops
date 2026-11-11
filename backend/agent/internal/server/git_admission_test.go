package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayGitHTTPAdmissionRecordsKnownTerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   GatewayHubOptions
		status string
		code   int
	}{
		{"canceled after lease", GatewayHubOptions{MaxQueuedPerTarget: 1, TargetQueueTimeout: 3 * time.Second}, "canceled", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, tc.opts)
			f.agent(t, "git-admission")
			agent := f.hub.registry.Get("dev", "local")
			requestID := atomic.LoadInt64(&agent.reqID)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest(http.MethodGet,
				"/v1/git/dev/local/git-admission.git/info/refs?service=git-upload-pack", nil).WithContext(ctx)
			request.SetBasicAuth("doops", f.admin.Plaintext)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			run := func() {
				f.hub.HandleGitHTTP(response, request)
				close(done)
			}
			resourceKey := gitResourceKey(ActionPull, "dev", "local", "git-admission")
			held, err := agent.acquireActionLeaseForSession(ctx, ActionPull, resourceKey, "git-admission", 0, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Release()
			go run()
			requireEventually(t, time.Second, func() bool {
				agent.opsMu.Lock()
				defer agent.opsMu.Unlock()
				slot := agent.resources[resourceKey]
				return slot != nil && slot.queued == 1
			})
			func() {
				// Pause lease attachment after acquisition, before dispatch.
				f.hub.client.activeOpsMu.Lock()
				defer f.hub.client.activeOpsMu.Unlock()
				held.Release()
				requireEventually(t, time.Second, func() bool {
					agent.opsMu.Lock()
					defer agent.opsMu.Unlock()
					slot := agent.resources[resourceKey]
					return agent.readers == 1 && slot != nil && slot.queued == 0
				})
				cancel()
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("rejected Git request did not finish")
			}
			if response.Code != tc.code {
				t.Fatalf("HTTP %d, want %d: %s", response.Code, tc.code, response.Body.String())
			}
			events, err := f.store.ListAuditFiltered(AuditFilter{Session: "git-admission"})
			if err != nil || len(events) != 1 {
				t.Fatalf("Git admission audit = %#v, %v", events, err)
			}
			event := events[0]
			if event.Status != tc.status || event.Error == "" || event.EndedAt.IsZero() {
				t.Fatalf("lost known rejection: %#v, want %s", event, tc.status)
			}
			if got := atomic.LoadInt64(&agent.reqID); got != requestID {
				t.Fatalf("rejected request was dispatched: request ID %d -> %d", requestID, got)
			}
			if state := agent.runtimeState(); state.activeOps != 0 || state.queuedOps != 0 {
				t.Fatalf("rejected request retained its lease: %#v", state)
			}
			if ops := f.hub.listActiveOperations(); len(ops) != 0 {
				t.Fatalf("rejected request retained active ownership: %#v", ops)
			}
		})
	}
}
