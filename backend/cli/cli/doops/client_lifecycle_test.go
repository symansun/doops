package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

func TestAgentReleaseRequiresExactCommit(t *testing.T) {
	expected := strings.Repeat("a", 40)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var request map[string]interface{}
		if err := conn.ReadJSON(&request); err != nil {
			t.Error(err)
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]interface{}{
				"capabilities": map[string]interface{}{"releaseCommit": expected},
			},
		})
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	client := newGatewayTestClient(t, server.URL, "release-probe")
	defer client.Close()
	if err := client.RequireAgentRelease(expected); err != nil {
		t.Fatalf("exact release commit rejected: %v", err)
	}
	if err := client.RequireAgentRelease(strings.Repeat("b", 40)); err == nil ||
		!strings.Contains(err.Error(), "release commit mismatch") {
		t.Fatalf("mismatched release commit was accepted: %v", err)
	}
}

func executionLifecycleCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion":      "doops.sh/execution-lifecycle/v1",
		"terminalResponse":   true,
		"backgroundLifetime": true,
		"promptCancellation": true,
	}
}

func TestExecutionLifecycleCapabilityRequiresExactSchema(t *testing.T) {
	if err := validateRequiredProtocol("executionLifecycle"); err != nil {
		t.Fatalf("execution lifecycle protocol rejected: %v", err)
	}
	if !protocolCapabilityCompatible("executionLifecycle", executionLifecycleCapability()) {
		t.Fatal("current execution lifecycle capability must be accepted")
	}
	invalid := []interface{}{nil, false, true, "doops.sh/execution-lifecycle/v1", []interface{}{}}
	for field := range executionLifecycleCapability() {
		missing := executionLifecycleCapability()
		delete(missing, field)
		invalid = append(invalid, missing)
		for _, value := range []interface{}{false, "true", 1, nil} {
			malformed := executionLifecycleCapability()
			malformed[field] = value
			invalid = append(invalid, malformed)
		}
	}
	extra := executionLifecycleCapability()
	extra["extra"] = true
	invalid = append(invalid, extra)
	for _, capability := range invalid {
		if protocolCapabilityCompatible("executionLifecycle", capability) {
			t.Fatalf("invalid lifecycle capability was accepted: %#v", capability)
		}
	}
}

func TestUnknownProtocolRejectsBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected network request", http.StatusInternalServerError)
	}))
	defer gateway.Close()
	client := newGatewayTestClient(t, gateway.URL, "unknown-protocol")
	defer client.Close()
	if err := client.RequireProtocol("unknownProtocol"); err == nil ||
		!strings.Contains(err.Error(), `unsupported protocol "unknownProtocol"`) {
		t.Fatalf("unknown protocol did not produce an argument error: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unknown protocol sent %d network requests", requests.Load())
	}
}

func TestUnknownProtocolCLIRejectsBeforeConfigOrNetwork(t *testing.T) {
	command := exec.Command(buildDoopsCLIBinary(t), "-session", "unknown-protocol",
		"capabilities", "--target", "nonexistent", "--require", "unknownProtocol")
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+filepath.Join(t.TempDir(), "missing.json"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("unknown protocol request succeeded")
	}
	if !strings.Contains(string(output), `unsupported protocol "unknownProtocol"`) ||
		strings.Contains(string(output), "upgrade the Agent") {
		t.Fatalf("unknown protocol produced a misleading runtime error: %s", output)
	}
}

func TestExecutionLifecycleCLIHelp(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	for _, tc := range []struct {
		command string
		want    string
	}{
		{"capabilities", "structuredSSH, executionLifecycle"},
		{"exec", "requires executionLifecycle"},
	} {
		command := exec.Command(binary, tc.command, "--help")
		command.Env = append(os.Environ(), "DOOPS_CONFIG="+filepath.Join(t.TempDir(), "missing.json"))
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), tc.want) {
			t.Fatalf("%s help: err=%v output=%s", tc.command, err, output)
		}
	}
}
