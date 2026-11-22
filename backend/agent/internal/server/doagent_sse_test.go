package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSubscribeDoagentSSEReturnsErrorWhenStreamEndsWithoutFinalEvent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"partial"}}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "sse-no-final", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil {
		t.Fatal("expected error when SSE ends before final event")
	}
	if !strings.Contains(err.Error(), "ended before final") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := progress.String(); got != "" {
		t.Fatalf("progress mismatch: %q", got)
	}
}

func TestSubscribeDoagentSSEScopesUntaggedEventsToFreshMachineSession(t *testing.T) {
	for _, test := range []struct {
		name          string
		allowUntagged bool
		wantTypes     []string
	}{
		{
			name:          "fresh machine session accepts current untagged event",
			allowUntagged: true,
			wantTypes:     []string{"agent_message", "turn_finished"},
		},
		{
			name:          "reused session rejects untagged event",
			allowUntagged: false,
			wantTypes:     []string{"turn_finished"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/events" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","replay":true,"content":{"type":"text","text":"stale"}}}}`)
				fmt.Fprintln(w)
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"current"}}}}`)
				fmt.Fprintln(w)
				fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-current","status":"completed","stopReason":"end_turn"}}}`)
				fmt.Fprintln(w)
			}))
			defer ts.Close()

			turnIDReady := make(chan string, 1)
			turnIDReady <- "turn-current"
			close(turnIDReady)
			var gotTypes []string
			err := subscribeDoagentSSEWithCollectorReady(
				t.Context(),
				ts.URL,
				"scope-test",
				func(notificationEvent) {},
				func(update map[string]interface{}) error {
					gotTypes = append(gotTypes, fmt.Sprint(update["sessionUpdate"]))
					return nil
				},
				turnIDReady,
				test.allowUntagged,
				true,
				nil,
			)
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			if !reflect.DeepEqual(gotTypes, test.wantTypes) {
				t.Fatalf("collected update types = %#v, want %#v", gotTypes, test.wantTypes)
			}
		})
	}
}

func TestSubscribeDoagentSSERejectsLiveUntaggedContentOnlyToolEvidence(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","replay":true,"toolCallId":"stale","toolName":"mcp_doops_helm","status":"completed","input":{"operation":"rollback"},"content":[{"type":"content","content":{"type":"text","text":"stale"}}]}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"current","toolName":"mcp_doops_helm","status":"in_progress","input":{"operation":"upgrade-install","release":"doops-agent","chart":"deploy/helm/doops-agent","namespace":"kz-ops"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"current","toolName":"mcp_doops_helm","status":"completed","content":[{"type":"content","content":{"type":"text","text":"Helm operation complete."}}]}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","turnId":"turn-current","status":"completed","stopReason":"end_turn"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	turnIDReady := make(chan string, 1)
	turnIDReady <- "turn-current"
	close(turnIDReady)
	collector := newDoagentToolTraceCollector(false)
	err := subscribeDoagentSSEWithCollectorReady(
		t.Context(),
		ts.URL,
		"apply-evidence",
		func(notificationEvent) {},
		collector.collect,
		turnIDReady,
		true,
		true,
		nil,
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := collector.requireCompletedToolEvidence(); err == nil ||
		!strings.Contains(err.Error(), "raw_output_missing") {
		t.Fatalf("content-only tool evidence accepted: %v", err)
	}
	if traces := collector.completed(); len(traces) != 0 {
		t.Fatalf("content-only traces accepted as completed evidence: %#v", traces)
	}
}

func TestSubscribeDoagentSSEDoesNotForwardMalformedRawData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: Authorization: Bearer synthetic-canary`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "malformed-sse", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil || !strings.Contains(err.Error(), "doagent_sse_protocol_invalid") {
		t.Fatalf("malformed SSE error = %v", err)
	}
	if strings.Contains(progress.String(), "synthetic-canary") ||
		!strings.Contains(progress.String(), "doagent_sse_protocol_invalid") {
		t.Fatalf("malformed SSE raw data leaked to progress: %q", progress.String())
	}
}

func TestSubscribeDoagentSSESilentStreamEndsOnlyOnTerminalSignal(t *testing.T) {
	const silence = 100 * time.Millisecond
	toolStart := `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"helm-1","toolName":"mcp_doops_helm","status":"in_progress"}}}` + "\n\n"
	finished := `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"completed","stopReason":"end_turn"}}}` + "\n\n"

	for _, test := range []struct {
		name       string
		deadline   time.Duration // caller deadline; 0 means none
		closeAfter bool          // server drops the connection after the silence
		finish     bool          // server sends turn_finished after the silence
		wantErr    string        // empty means success
		minElapsed time.Duration
	}{
		{name: "silent tool then turn finished succeeds", finish: true, minElapsed: silence},
		{name: "silent tool then connection drop fails", closeAfter: true, wantErr: "ended before final", minElapsed: silence},
		{name: "silent tool past caller deadline is canceled", deadline: silence / 2, wantErr: "context deadline exceeded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/events" {
					t.Errorf("unexpected path: %s", r.URL.Path)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				flusher := w.(http.Flusher)
				fmt.Fprint(w, toolStart)
				flusher.Flush()
				select {
				case <-time.After(silence):
				case <-r.Context().Done():
					return
				}
				if test.finish {
					fmt.Fprint(w, finished)
					flusher.Flush()
				}
				if !test.closeAfter && !test.finish {
					<-r.Context().Done()
				}
			}))
			defer ts.Close()

			ctx := t.Context()
			if test.deadline > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.deadline)
				defer cancel()
			}
			started := time.Now()
			err := subscribeDoagentSSE(ctx, ts.URL, "sse-silent", func(notificationEvent) {})
			elapsed := time.Since(started)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("subscribe: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, test.wantErr)
			}
			if elapsed < test.minElapsed {
				t.Fatalf("returned after %s, before the %s silence ended", elapsed, test.minElapsed)
			}
		})
	}
}

func TestSubscribeDoagentSSEReturnsTurnFinishedError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"failed-turn","update":{"sessionUpdate":"turn_finished","stopReason":"error","error":"Agent error: API error (404 Not Found): vip expired"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	started := time.Now()
	err := subscribeDoagentSSE(t.Context(), ts.URL, "failed-turn", func(notificationEvent) {})
	if err == nil {
		t.Fatal("expected failed turn to return an error")
	}
	if !strings.Contains(err.Error(), "vip expired") {
		t.Fatalf("turn failure must preserve the actual doagent error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("terminal turn failure must return immediately, took %s", elapsed)
	}
}

func TestSubscribeDoagentSSEPreservesErrorEventMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"error","params":{"error":"error decoding response body"}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "error-event", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil || !strings.Contains(err.Error(), "error decoding response body") {
		t.Fatalf("error event must preserve the original message, got %v", err)
	}
	if progress.String() != "" {
		t.Fatalf("terminal error must be deferred to the caller, got progress %q", progress.String())
	}
}

func TestSubscribeDoagentSSEWaitsForAuthoritativeFailedTurn(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"looks good"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"usage_update","inputTokens":10,"outputTokens":5}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"interrupted","stopReason":"context_limit"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	err := subscribeDoagentSSE(t.Context(), ts.URL, "interrupted-turn", func(evt notificationEvent) {
		progress.WriteString(evt.Data)
	})
	if err == nil || !strings.Contains(err.Error(), "context_limit") {
		t.Fatalf("authoritative failed turn must win over agent_message, got %v", err)
	}
	if !strings.Contains(progress.String(), "looks good") {
		t.Fatalf("agent message should remain visible as progress: %q", progress.String())
	}
}

func TestSubscribeDoagentSSESanitizesAgentMessagesBeforeCollectorAndProgress(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"https://storage.example.test/object?X-Amz-Signature="}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"stream-canary"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"completed","stopReason":"end_turn"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	var collected string
	err := subscribeDoagentSSEWithCollector(
		t.Context(),
		ts.URL,
		"sse-sanitize",
		func(evt notificationEvent) { progress.WriteString(evt.Data) },
		func(update map[string]interface{}) error {
			data, _ := json.Marshal(update)
			collected += string(data)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe sanitized SSE: %v", err)
	}
	if strings.Contains(progress.String(), "stream-canary") ||
		strings.Contains(collected, "stream-canary") ||
		!strings.Contains(progress.String(), "REDACTED") {
		t.Fatalf("raw agent message escaped sanitization: progress=%q collector=%q", progress.String(), collected)
	}
}

func TestSubscribeDoagentSSEDefersStructuredAgentMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message","content":{"type":"text","text":"{\"kind\":\"DeploymentRun\",\"subject\":\"https://storage.example.test/object?X-Amz-Signature=stream-canary\"}"}}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"turn_finished","status":"completed","stopReason":"end_turn"}}}`)
		fmt.Fprintln(w)
	}))
	defer ts.Close()

	var progress strings.Builder
	var collected []string
	err := subscribeDoagentSSEWithCollectorReady(
		t.Context(),
		ts.URL,
		"structured-no-forward",
		func(evt notificationEvent) { progress.WriteString(evt.Data) },
		func(update map[string]interface{}) error {
			collected = append(collected, fmt.Sprint(update["sessionUpdate"]))
			return nil
		},
		nil,
		false,
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("subscribe structured SSE: %v", err)
	}
	if strings.Contains(progress.String(), "DeploymentRun") ||
		strings.Contains(progress.String(), "stream-canary") {
		t.Fatalf("unvalidated structured result was forwarded: %s", progress.String())
	}
	if !reflect.DeepEqual(collected, []string{"agent_message", "turn_finished"}) {
		t.Fatalf("structured updates were not collected: %#v", collected)
	}
}
