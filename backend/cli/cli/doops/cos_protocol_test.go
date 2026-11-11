package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func cosGatewayCapabilities() map[string]interface{} {
	return map[string]interface{}{"gatewayProtocols": map[string]interface{}{
		"cosSnapshotExecutable": map[string]interface{}{"schemaVersion": cosSnapshotExecutableSchema, "executable": true},
	}}
}

// The HTTP server is a real MCP WebSocket peer; the counter observes requests
// received by the peer, rather than the client's in-memory capability flag.
func cosProtocolServer(t *testing.T, capabilities func(int64) map[string]interface{}, disconnectAfterProbe bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var connections, toolCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		generation := connections.Add(1)
		for {
			var request struct {
				ID     interface{} `json:"id"`
				Method string      `json:"method"`
				Params struct {
					Arguments map[string]interface{} `json:"arguments"`
				} `json:"params"`
			}
			if conn.ReadJSON(&request) != nil {
				return
			}
			result := map[string]interface{}{"capabilities": capabilities(generation)}
			if request.Method == "tools/call" {
				toolCalls.Add(1)
				result = map[string]interface{}{"structuredContent": map[string]interface{}{"storage": "cos", "url": "https://example.invalid/file", "workspace_status": "synced"}}
			}
			if conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result}) != nil {
				return
			}
			if disconnectAfterProbe && request.Params.Arguments["operation"] == "probe" {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &toolCalls
}

func TestCOSSnapshotRejectsGatewayWithoutExecutableCapabilityBeforeUploading(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities map[string]interface{}
	}{
		{"missing", nil},
		{"agent-only", map[string]interface{}{"protocols": cosGatewayCapabilities()["gatewayProtocols"]}},
		{"wrong-schema", map[string]interface{}{"gatewayProtocols": map[string]interface{}{"cosSnapshotExecutable": map[string]interface{}{"schemaVersion": "wrong", "executable": true}}}},
		{"false", map[string]interface{}{"gatewayProtocols": map[string]interface{}{"cosSnapshotExecutable": map[string]interface{}{"schemaVersion": cosSnapshotExecutableSchema, "executable": false}}}},
		{"wrong-type", map[string]interface{}{"gatewayProtocols": map[string]interface{}{"cosSnapshotExecutable": map[string]interface{}{"schemaVersion": cosSnapshotExecutableSchema, "executable": "true"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			srv, calls := cosProtocolServer(t, func(int64) map[string]interface{} { return tc.capabilities }, false)
			src := t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := pushCOS(Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}, src, "", false, "session", "", "synthetic-token", nil)
			if err == nil || !strings.Contains(err.Error(), "cos_snapshot_executable_unsupported") {
				t.Fatalf("unsupported Gateway error = %v", err)
			}
			if calls.Load() != 0 {
				t.Fatalf("unsupported Gateway received %d tool calls", calls.Load())
			}
		})
	}
}

func TestCOSSnapshotRechecksGatewayAfterConnectionReplacement(t *testing.T) {
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	srv, calls := cosProtocolServer(t, func(generation int64) map[string]interface{} {
		if generation == 1 {
			return cosGatewayCapabilities()
		}
		return nil
	}, true)
	gateway, err := newGatewayCOS(Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}, "synthetic-token", "session")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err := gateway.ProbeSnapshot(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		gateway.client.mu.Lock()
		connected := gateway.client.connected
		gateway.client.mu.Unlock()
		if !connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("peer closed the connection but client did not retire it")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := gateway.Put("snapshots", "commit/run.sh", []byte("#!/bin/sh\n")); err == nil || !strings.Contains(err.Error(), "cos_snapshot_executable_unsupported") {
		t.Fatalf("replacement Gateway accepted upload: %v", err)
	}
	if _, err := gateway.Sync(strings.Repeat("a", 40)); err == nil || !strings.Contains(err.Error(), "cos_snapshot_executable_unsupported") {
		t.Fatalf("replacement Gateway accepted sync: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected only original read-only probe, received %d calls", calls.Load())
	}
}

func TestCOSFileReadWriteDoNotRequireSnapshotCapability(t *testing.T) {
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	srv, calls := cosProtocolServer(t, func(int64) map[string]interface{} { return nil }, false)
	server := Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}
	if _, err := runCOSRead(server, "session", "", "synthetic-token", "file.txt", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runCOSWrite(server, "session", "", "synthetic-token", "file.txt", []byte("data"), nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("file probe/read/write calls = %d, want 4", calls.Load())
	}
}

func TestCOSSnapshotRechecksAnExistingFileConnection(t *testing.T) {
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	srv, calls := cosProtocolServer(t, func(int64) map[string]interface{} { return nil }, false)
	gateway, err := newGatewayCOS(Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}, "synthetic-token", "session")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	if err := gateway.Probe(); err != nil {
		t.Fatal(err)
	}
	if err := gateway.ProbeSnapshot(); err == nil || !strings.Contains(err.Error(), "cos_snapshot_executable_unsupported") {
		t.Fatalf("existing legacy connection accepted snapshot mode: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("legacy connection received %d calls, want only original health probe", calls.Load())
	}
}

func TestCOSSnapshotMutationCannotSkipTheCapabilityCheck(t *testing.T) {
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	srv, calls := cosProtocolServer(t, func(int64) map[string]interface{} { return nil }, false)
	for _, operation := range []string{"put", "presign_put", "confirm", "sync"} {
		gateway, err := newGatewayCOS(Server{Name: "test", Gateway: srv.URL, Cluster: "test", Instance: "node"}, "synthetic-token", "session")
		if err != nil {
			t.Fatal(err)
		}
		_, err = gateway.call(operation, "snapshots", "commit/run.sh", nil)
		gateway.Close()
		if err == nil || !strings.Contains(err.Error(), "cos_snapshot_executable_unsupported") {
			t.Fatalf("%s skipped the Gateway capability: %v", operation, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("legacy Gateway received %d snapshot calls", calls.Load())
	}
}
