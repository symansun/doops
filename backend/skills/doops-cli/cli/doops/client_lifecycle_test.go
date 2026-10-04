package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestRetiredLifecycleProtocolIsNotAccepted(t *testing.T) {
	if err := validateRequiredProtocol("executionLifecycle"); err == nil ||
		!strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("retired lifecycle protocol was not rejected at the CLI boundary: %v", err)
	}
	if protocolCapabilityCompatible("executionLifecycle", map[string]interface{}{
		"schemaVersion":      "doops.sh/execution-lifecycle/v1",
		"terminalResponse":   true,
		"backgroundLifetime": true,
		"promptCancellation": true,
	}) {
		t.Fatal("retired execution lifecycle protocol must not be accepted")
	}
}

func TestRetiredLifecycleCLIRejectsBeforeConfigOrNetwork(t *testing.T) {
	command := exec.Command(buildDoopsCLIBinary(t), "-session", "retired-protocol",
		"capabilities", "--target", "nonexistent", "--require", "executionLifecycle")
	command.Env = append(os.Environ(), "DOOPS_CONFIG="+filepath.Join(t.TempDir(), "missing.json"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("retired protocol request succeeded")
	}
	if !strings.Contains(string(output), `unsupported protocol "executionLifecycle"`) ||
		strings.Contains(string(output), "upgrade the Agent") {
		t.Fatalf("retired protocol produced a misleading runtime error: %s", output)
	}
}
