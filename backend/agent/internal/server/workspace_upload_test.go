package server

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentWebSocketWorkspaceChunkUpload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()

	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	archive := tinyTarGz(t, "hello.txt", "hello chunked push\n")
	commit := sha256Hex(archive)
	begin := callTool(t, conn, "doops_workspace_begin", map[string]interface{}{
		"session_id": "smoke",
		"commit":     commit,
	})
	chunk := base64.StdEncoding.EncodeToString(archive)
	callTool(t, conn, "doops_workspace_chunk", map[string]interface{}{
		"upload_id": begin,
		"seq":       0,
		"data_b64":  chunk,
	})
	callTool(t, conn, "doops_workspace_commit", map[string]interface{}{
		"upload_id":  begin,
		"session_id": "smoke",
		"commit":     commit,
	})

	got, err := os.ReadFile(filepath.Join(root, "smoke", "hello.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "hello chunked push\n" {
		t.Fatalf("extracted content mismatch: %q", got)
	}
	ready, err := os.ReadFile(filepath.Join(root, "smoke", ".doops-ready"))
	if err != nil {
		t.Fatalf("read ready file: %v", err)
	}
	if string(bytes.TrimSpace(ready)) != commit {
		t.Fatalf("ready commit mismatch: %q", ready)
	}
}

func TestWorkspaceUploadRejectsOutOfOrderChunks(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	archive := tinyTarGz(t, "hello.txt", "ordered\n")
	commit := sha256Hex(archive)
	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "ordered",
		"commit":     commit,
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}

	_, err = handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       1,
		"data_b64":  base64.StdEncoding.EncodeToString(archive),
	}))
	if err == nil || !strings.Contains(err.Error(), "unexpected chunk sequence") {
		t.Fatalf("expected out-of-order chunk error, got %v", err)
	}
}

func TestWorkspaceCommitRejectsSHA256MismatchAndCleansUpload(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)

	archive := tinyTarGz(t, "hello.txt", "checksum\n")
	wrongCommit := strings.Repeat("0", 64)
	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "checksum",
		"commit":     wrongCommit,
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	if _, err := handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       0,
		"data_b64":  base64.StdEncoding.EncodeToString(archive),
	})); err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	_, err = handleWorkspaceCommit(mustJSON(t, map[string]interface{}{
		"upload_id":  uploadID,
		"session_id": "checksum",
		"commit":     wrongCommit,
	}))
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got %v", err)
	}
	if _, statErr := os.Stat(uploadArchivePath(uploadID)); !os.IsNotExist(statErr) {
		t.Fatalf("expected failed archive to be cleaned, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "checksum")); !os.IsNotExist(statErr) {
		t.Fatalf("expected destination not to be created on checksum failure, stat err=%v", statErr)
	}
}

func TestWorkspaceUploadRejectsSizeLimit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("DOOPS_MAX_WORKSPACE_UPLOAD_BYTES", "8")

	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "too-big",
		"commit":     strings.Repeat("1", 64),
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	_, err = handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       0,
		"data_b64":  base64.StdEncoding.EncodeToString([]byte("123456789")),
	}))
	if err == nil || !strings.Contains(err.Error(), "upload exceeds") {
		t.Fatalf("expected upload limit error, got %v", err)
	}
}

func TestWorkspaceCommitRejectsExpandedSizeLimit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("DOOPS_MAX_WORKSPACE_UPLOAD_BYTES", "512")

	archive := tinyTarGz(t, "large.txt", strings.Repeat("A", 4096))
	commit := sha256Hex(archive)
	uploadID, err := handleWorkspaceBegin(mustJSON(t, map[string]interface{}{
		"session_id": "expanded",
		"commit":     commit,
	}))
	if err != nil {
		t.Fatalf("begin upload: %v", err)
	}
	if _, err := handleWorkspaceChunk(mustJSON(t, map[string]interface{}{
		"upload_id": uploadID,
		"seq":       0,
		"data_b64":  base64.StdEncoding.EncodeToString(archive),
	})); err != nil {
		t.Fatalf("chunk upload: %v", err)
	}

	_, err = handleWorkspaceCommit(mustJSON(t, map[string]interface{}{
		"upload_id":  uploadID,
		"session_id": "expanded",
		"commit":     commit,
	}))
	if err == nil || !strings.Contains(err.Error(), "extracted workspace exceeds") {
		t.Fatalf("expected expanded size limit error, got %v", err)
	}
	if _, statErr := os.Stat(uploadArchivePath(uploadID)); !os.IsNotExist(statErr) {
		t.Fatalf("expected failed archive to be cleaned, stat err=%v", statErr)
	}
}

func TestCleanupExpiredWorkspaceUploads(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	uploadID := "upl_" + strings.Repeat("a", 32)
	if err := os.MkdirAll(uploadDir(), 0700); err != nil {
		t.Fatalf("mkdir upload dir: %v", err)
	}
	if err := os.WriteFile(uploadArchivePath(uploadID), []byte("stale"), 0600); err != nil {
		t.Fatalf("write stale archive: %v", err)
	}
	if err := saveUploadMeta(uploadID, workspaceUploadMeta{
		SessionID: "stale",
		Commit:    strings.Repeat("2", 64),
		NextSeq:   0,
		Size:      0,
		CreatedAt: time.Now().UTC().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("write stale meta: %v", err)
	}

	if err := cleanupExpiredUploads(24 * time.Hour); err != nil {
		t.Fatalf("cleanup expired uploads: %v", err)
	}
	if _, err := os.Stat(uploadArchivePath(uploadID)); !os.IsNotExist(err) {
		t.Fatalf("expected stale archive removed, stat err=%v", err)
	}
	if _, err := os.Stat(uploadMetaPath(uploadID)); !os.IsNotExist(err) {
		t.Fatalf("expected stale metadata removed, stat err=%v", err)
	}
}
