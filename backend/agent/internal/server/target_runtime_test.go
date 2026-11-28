package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Package tests model a Kubernetes target unless a test says otherwise; the
// real probe would depend on the developer machine's kubectl.
func init() {
	probeKubernetesAPI = func(context.Context) bool { return true }
}

func stubKubernetesProbe(t *testing.T, available bool) {
	t.Helper()
	previous := probeKubernetesAPI
	reset := func() {
		targetRuntimeMu.Lock()
		targetRuntimeExpires = time.Time{}
		targetRuntimeMu.Unlock()
	}
	probeKubernetesAPI = func(context.Context) bool { return available }
	reset()
	t.Cleanup(func() { probeKubernetesAPI = previous; reset() })
}

func TestTargetWithoutKubernetesIsNotOfferedKubernetesReads(t *testing.T) {
	for _, available := range []bool{true, false} {
		stubKubernetesProbe(t, available)
		runtime := currentTargetRuntime(context.Background())
		if runtime.Kubernetes != available {
			t.Fatalf("probe result %v was not reported: %#v", available, runtime)
		}
		for _, skill := range []string{"", "dosql", "doweb"} {
			tools := doagentAllowedTools(false, skill, runtime.Kubernetes)
			if sliceContainsString(tools, "mcp_doops_k8s_read") != available {
				t.Fatalf("kubernetes=%v skill=%q tools=%#v", available, skill, tools)
			}
			if !sliceContainsString(tools, "Bash") || !sliceContainsString(tools, "mcp_doops_web_read") {
				t.Fatalf("host diagnostics were lost: %#v", tools)
			}
		}
		// An authorized release keeps its mutation tool so a failure is real evidence.
		if !sliceContainsString(doagentAllowedTools(true, "", runtime.Kubernetes), "mcp_doops_helm") {
			t.Fatal("apply lost its Helm tool")
		}
		facts := runtime.promptFacts()
		if strings.Contains(facts, "Kubernetes API：可用") != available ||
			strings.Contains(facts, "不提供 `mcp_doops_k8s_read`") == available {
			t.Fatalf("prompt facts disagree with the probe: %s", facts)
		}
	}
}

func TestTargetRuntimeProbeIsCached(t *testing.T) {
	stubKubernetesProbe(t, true)
	calls := 0
	probeKubernetesAPI = func(context.Context) bool { calls++; return true }
	currentTargetRuntime(context.Background())
	currentTargetRuntime(context.Background())
	if calls != 1 {
		t.Fatalf("probe ran %d times within its TTL", calls)
	}
}
