package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBuildK8SRequestGetPods(t *testing.T) {
	req, err := buildK8SRequest([]string{"get", "pods", "--target", "prod", "--namespace", "oilan-system", "--output", "json"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Target != "prod" {
		t.Fatalf("target mismatch: %q", req.Target)
	}
	want := map[string]interface{}{
		"operation": "get",
		"kind":      "pods",
		"namespace": "oilan-system",
		"output":    "json",
	}
	if !reflect.DeepEqual(req.Payload, want) {
		t.Fatalf("payload mismatch:\n got: %#v\nwant: %#v", req.Payload, want)
	}
}

func TestBuildK8SRequestLogs(t *testing.T) {
	req, err := buildK8SRequest([]string{"logs", "deploy/exam-api", "--target", "prod", "--namespace", "oilan-system", "--tail", "100"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	want := map[string]interface{}{
		"operation": "logs",
		"resource":  "deploy/exam-api",
		"namespace": "oilan-system",
		"tail":      100,
	}
	if !reflect.DeepEqual(req.Payload, want) {
		t.Fatalf("payload mismatch:\n got: %#v\nwant: %#v", req.Payload, want)
	}
}

func TestBuildK8SRequestTopNodes(t *testing.T) {
	req, err := buildK8SRequest([]string{"top", "nodes", "--target", "prod"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	want := map[string]interface{}{
		"operation": "top",
		"kind":      "nodes",
	}
	if !reflect.DeepEqual(req.Payload, want) {
		t.Fatalf("payload mismatch:\n got: %#v\nwant: %#v", req.Payload, want)
	}
}

func TestBuildK8SRequestRolloutRestart(t *testing.T) {
	req, err := buildK8SRequest([]string{"rollout", "restart", "deploy/exam-api", "--target", "prod", "--namespace", "oilan-system"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	want := map[string]interface{}{
		"operation": "rollout-restart",
		"resource":  "deploy/exam-api",
		"namespace": "oilan-system",
	}
	if !reflect.DeepEqual(req.Payload, want) {
		t.Fatalf("payload mismatch:\n got: %#v\nwant: %#v", req.Payload, want)
	}
}

func TestRunK8SMutationsWithoutConfirm(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		operation string
	}{
		{[]string{"rollout", "restart", "deployment/api"}, "rollout-restart"},
		{[]string{"scale", "deployment/api", "--replicas", "2"}, "scale"},
		{[]string{"node", "cordon", "node-a"}, "cordon"},
		{[]string{"node", "uncordon", "node-a"}, "uncordon"},
		{[]string{"delete", "pod", "pod-a"}, "delete-pod"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			req, err := buildK8SRequest(append(tc.args, "--target", "prod"))
			if err != nil {
				t.Fatalf("build explicit mutation: %v", err)
			}
			caller := &fakeK8SCaller{}
			if _, err := runK8SRequest(caller, req); err != nil {
				t.Fatalf("run explicit mutation: %v", err)
			}
			if len(caller.calls) != 1 || caller.calls[0].tool != "doops_k8s" || caller.calls[0].args["operation"] != tc.operation {
				t.Fatalf("mutation was not dispatched exactly once: %#v", caller.calls)
			}
			if _, exists := caller.calls[0].args["confirm"]; exists {
				t.Fatalf("removed confirm field sent: %#v", caller.calls)
			}
		})
	}
}

func TestBuildK8SRequestRejectsRemovedConfirmFlag(t *testing.T) {
	_, err := buildK8SRequest([]string{"node", "cordon", "node-a", "--target", "prod", "--confirm"})
	if err == nil || !strings.Contains(err.Error(), "unknown k8s flag --confirm") {
		t.Fatalf("removed flag accepted: %v", err)
	}
}

func TestBuildK8SRequestScaleRequiresReplicas(t *testing.T) {
	_, err := buildK8SRequest([]string{
		"scale", "deployment/api", "--target", "prod",
	})
	if err == nil {
		t.Fatal("expected missing replicas to fail")
	}
}

func TestBuildK8SRequestRejectsExtraPositionals(t *testing.T) {
	_, err := buildK8SRequest([]string{
		"delete", "pod", "pod-a", "pod-b", "--target", "prod",
	})
	if err == nil {
		t.Fatal("expected extra positional argument to fail")
	}
}

func TestBuildK8SRequestRejectsMissingTarget(t *testing.T) {
	if _, err := buildK8SRequest([]string{"get", "pods", "--namespace", "oilan-system"}); err == nil {
		t.Fatal("expected missing target to fail")
	}
}

func TestK8SInvalidSyntaxPointsToHelp(t *testing.T) {
	for _, tc := range []struct{ args, why string }{
		{"", "subcommand"},
		{"drain node-a", "drain"},
		{"rollout undo deploy/api", "undo"},
		{"logs pod/api --tail many", "integer"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			req, err := buildK8SRequest(append([]string{"--target", "test"}, strings.Fields(tc.args)...))
			if err == nil || !strings.Contains(err.Error(), tc.why) || !strings.Contains(err.Error(), "next: doops k8s -h") || req.Payload != nil {
				t.Fatalf("expected local error with reason and next step; got req=%#v err=%v", req, err)
			}
		})
	}
}

func TestK8SPolicyDenialStopsAtStructuredRoute(t *testing.T) {
	req, err := buildK8SRequest([]string{"--target", "test", "get", "secrets"})
	if err != nil {
		t.Fatalf("CLI rejected valid syntax before Agent policy check: %v", err)
	}
	denied := errors.New("resource denied by policy")
	caller := &fakeK8SCaller{err: denied}
	msg, err := runK8SRequest(caller, req)
	if err != denied || msg != "" || len(caller.calls) != 1 || caller.calls[0].tool != "doops_k8s" {
		t.Fatalf("expected only the original denial; calls=%#v msg=%q err=%v", caller.calls, msg, err)
	}
}

func TestRunK8SRequestDirectCallsDoopsK8S(t *testing.T) {
	req, err := buildK8SRequest([]string{"get", "pods", "--target", "prod", "--namespace", "oilan-system"})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	caller := &fakeK8SCaller{}
	msg, err := runK8SRequest(caller, req)
	if err != nil {
		t.Fatalf("run request: %v", err)
	}
	if msg != "" {
		t.Fatalf("unexpected message: %q", msg)
	}
	if len(caller.calls) != 1 || caller.calls[0].tool != "doops_k8s" || caller.calls[0].args["operation"] != "get" {
		t.Fatalf("remote call mismatch: %#v", caller.calls)
	}
}

type fakeK8SCaller struct {
	calls []fakeK8SCall
	err   error
}

type fakeK8SCall struct {
	tool string
	args map[string]interface{}
}

func (f *fakeK8SCaller) Call(toolName string, arguments map[string]interface{}) error {
	copied := make(map[string]interface{}, len(arguments))
	for k, v := range arguments {
		copied[k] = v
	}
	f.calls = append(f.calls, fakeK8SCall{tool: toolName, args: copied})
	return f.err
}

func (f *fakeK8SCaller) CallAndCapture(toolName string, arguments map[string]interface{}) (string, error) {
	if err := f.Call(toolName, arguments); err != nil {
		return "", err
	}
	return "verified", nil
}

func TestBuildK8SRequestRawAndQuietKeepPayloadClean(t *testing.T) {
	for _, flagName := range []string{"--raw", "--quiet"} {
		t.Run(flagName, func(t *testing.T) {
			req, err := buildK8SRequest([]string{flagName, "--target", "prod", "get", "pods", "-n", "apps", "-o", "json"})
			if err != nil {
				t.Fatalf("buildK8SRequest %s: %v", flagName, err)
			}
			if !req.RawOutput {
				t.Fatalf("expected RawOutput=true for %s", flagName)
			}
			want := map[string]interface{}{
				"operation": "get",
				"kind":      "pods",
				"namespace": "apps",
				"output":    "json",
			}
			if !reflect.DeepEqual(req.Payload, want) {
				t.Fatalf("payload polluted by CLI flag %s:\n got: %#v\nwant: %#v", flagName, req.Payload, want)
			}
		})
	}
}

func TestCLIK8SRawAndQuietSuppressProgressAndTerminalText(t *testing.T) {
	const jsonPayload = `{"items":[{"metadata":{"name":"api-0"}}]}`
	const progressTick = "[agent] k8s progress heartbeat"

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			switch req["method"] {
			case "initialize":
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  agentInitializeResult(),
				})
			case "tools/call":
				params, _ := req["params"].(map[string]interface{})
				args, _ := params["arguments"].(map[string]interface{})
				ns, _ := args["namespace"].(string)

				sendNotice := func(kind, data string) {
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"method":  "notifications/message",
						"params": map[string]interface{}{
							"request_id": req["id"],
							"kind":       kind,
							"data":       data,
						},
					})
				}

				switch ns {
				case "apps":
					sendNotice("progress", progressTick)
					sendNotice("raw", jsonPayload)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Kubernetes operation complete."}},
						},
					})
				case "empty":
					sendNotice("progress", progressTick)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "Kubernetes operation complete."}},
						},
					})
				case "fail":
					sendNotice("progress", progressTick)
					_ = conn.WriteJSON(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      req["id"],
						"result": map[string]interface{}{
							"content": []map[string]interface{}{{"type": "text", "text": "kubectl get pods failed: forbidden"}},
							"isError": true,
						},
					})
				}
			}
		}
	}))
	defer gateway.Close()

	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "dev", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "synthetic-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	bin := buildDoopsCLIBinary(t)
	run := func(args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	for _, flagName := range []string{"--raw", "--quiet"} {
		t.Run("flag_"+flagName, func(t *testing.T) {
			stdout, stderr, err := run("-session", "k8s-raw", "k8s", flagName, "--target", "test", "get", "pods", "-n", "apps", "-o", "json")
			if err != nil {
				t.Fatalf("k8s %s failed: %v (stderr=%q)", flagName, err, stderr)
			}
			if stdout != jsonPayload {
				t.Fatalf("k8s %s stdout = %q, want exact %q", flagName, stdout, jsonPayload)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
				t.Fatalf("k8s %s stdout not valid JSON: %v (%q)", flagName, err, stdout)
			}
		})
	}

	// -verbose diagnostics go to stderr in --raw mode.
	verboseOut, verboseErr, err := run("-verbose", "-session", "k8s-verbose", "k8s", "--raw", "--target", "test", "get", "pods", "-n", "apps", "-o", "json")
	if err != nil {
		t.Fatalf("verbose k8s --raw failed: %v", err)
	}
	if verboseOut != jsonPayload || !strings.Contains(verboseErr, "[CLIENT:") {
		t.Fatalf("verbose k8s --raw stdout=%q stderr=%q", verboseOut, verboseErr)
	}

	// Silent k8s output suppresses "Kubernetes operation complete." wrapper text.
	silentOut, silentErr, err := run("-session", "k8s-empty", "k8s", "--raw", "--target", "test", "get", "pods", "-n", "empty")
	if err != nil {
		t.Fatalf("silent k8s --raw failed: %v (stderr=%q)", err, silentErr)
	}
	if silentOut != "" {
		t.Fatalf("silent k8s --raw must keep stdout empty, got: %q", silentOut)
	}

	// Tool error and invalid syntax go to stderr in --raw mode.
	failOut, failErr, err := run("-session", "k8s-fail", "k8s", "--raw", "--target", "test", "get", "pods", "-n", "fail")
	if err == nil || failOut != "" || !strings.Contains(failErr, "forbidden") {
		t.Fatalf("failing k8s --raw err=%v stdout=%q stderr=%q", err, failOut, failErr)
	}

	// Default non-raw missing target keeps stdout error message for contract compatibility.
	defaultMissingOut, _, err := run("-session", "k8s-contract", "k8s", "--target", "contract", "get", "pods")
	if err == nil || !strings.Contains(defaultMissingOut, `target "contract" is not configured`) {
		t.Fatalf("default k8s missing target contract broken: err=%v stdout=%q", err, defaultMissingOut)
	}
}
