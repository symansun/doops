package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentWebSocketFileReadWrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	callTool(t, conn, "doops_file_write", map[string]interface{}{
		"session_id": "files",
		"path":       "hello.txt",
		"content":    "hello gateway",
	})
	result := callTool(t, conn, "doops_file_read", map[string]interface{}{
		"session_id": "files",
		"path":       "hello.txt",
	})
	if result != "hello gateway" {
		t.Fatalf("read content mismatch: %q", result)
	}
	if _, err := os.Stat(filepath.Join(root, "files", "hello.txt")); err != nil {
		t.Fatalf("expected file in session workspace: %v", err)
	}
}

func TestAgentWebSocketFileToolsRejectWorkspaceEscape(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if _, err := handleFileWrite(mustJSON(t, map[string]interface{}{
		"session_id": "files",
		"path":       outside,
		"content":    "nope",
	})); err == nil {
		t.Fatal("expected absolute file write path to be rejected")
	}
	if _, err := handleFileRead(mustJSON(t, map[string]interface{}{
		"session_id": "files",
		"path":       "../outside.txt",
	})); err == nil {
		t.Fatal("expected relative escape read path to be rejected")
	}
}

func TestFileReadRejectsLargeFiles(t *testing.T) {
	t.Setenv("DOOPS_MAX_FILE_READ_BYTES", "8")
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	path := filepath.Join(root, "large", "large.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0644); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}

	_, err := handleFileRead(mustJSON(t, map[string]interface{}{"session_id": "large", "path": "large.txt"}))
	if err == nil || !strings.Contains(err.Error(), "viewing small text files only") {
		t.Fatalf("expected small-text-only error, got %v", err)
	}
}

func TestFileReadRejectsBinaryFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	path := filepath.Join(root, "binary", "archive.tgz")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte{0x1f, 0x8b, 0x08, 0x00, 0x61, 0x00}, 0644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}

	_, err := handleFileRead(mustJSON(t, map[string]interface{}{"session_id": "binary", "path": "archive.tgz"}))
	if err == nil || !strings.Contains(err.Error(), "looks like binary data") {
		t.Fatalf("expected binary rejection, got %v", err)
	}
}
