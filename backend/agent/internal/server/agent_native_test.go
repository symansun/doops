package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDoagentModeForPromptUsesOnlyNativeAskMode(t *testing.T) {
	tests := []struct {
		name          string
		operation     string
		executionMode string
		want          string
		wantErr       bool
	}{
		{name: "general", want: "auto"},
		{name: "ask", operation: "ask", want: "auto"},
		{name: "apply", operation: "apply", want: "build"},
		{name: "mode is not accepted", executionMode: "apply", wantErr: true},
		{name: "unknown operation", operation: "custom", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := doagentModeForPrompt(test.operation, test.executionMode)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected mode mapping error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("map native mode: %v", err)
			}
			if got != test.want {
				t.Fatalf("native mode mismatch: got %q want %q", got, test.want)
			}
		})
	}
}

func TestDoagentAllowedToolsHardLimitOrdinaryAndApplyPrompts(t *testing.T) {
	ordinary := doagentAllowedTools(false)
	if strings.Contains(strings.Join(ordinary, ","), "Bash") ||
		strings.Contains(strings.Join(ordinary, ","), "ExecuteCode") ||
		!sliceContainsString(ordinary, "mcp_doops_k8s_read") ||
		!sliceContainsString(ordinary, "mcp_doops_web_read") ||
		sliceContainsString(ordinary, "mcp_doops_k8s") {
		t.Fatalf("ordinary prompt tool boundary is unsafe: %#v", ordinary)
	}

	apply := doagentAllowedTools(true)
	if sliceContainsString(apply, "mcp_doops_k8s") ||
		!sliceContainsString(apply, "mcp_doops_helm") ||
		!sliceContainsString(apply, "mcp_doops_web") ||
		strings.Contains(strings.Join(apply, ","), "Bash") ||
		strings.Contains(strings.Join(apply, ","), "ExecuteCode") {
		t.Fatalf("apply prompt tool boundary is unsafe: %#v", apply)
	}
}

func sliceContainsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestDoagentPermissionRequestFailsClosedWithoutReply(t *testing.T) {
	rpcCalled := make(chan struct{}, 1)
	doagent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintln(w, `data: {"jsonrpc":"2.0","method":"permission.updated","params":{"sessionId":"permission-session","permission":{"id":"permission-1","title":"run mutating tool","toolName":"shell"}}}`)
			fmt.Fprintln(w)
		case "/rpc":
			rpcCalled <- struct{}{}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer doagent.Close()

	err := subscribeDoagentSSEWithCollector(
		context.Background(),
		doagent.URL,
		"permission-session",
		func(notificationEvent) {},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "permission required") {
		t.Fatalf("unexpected permission result: %v", err)
	}
	select {
	case <-rpcCalled:
		t.Fatal("DoOps bridge must never reply to doagent permission requests")
	case <-time.After(100 * time.Millisecond):
	}
}
