package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceReadProvidesCodeWorkbenchDataWithoutShell(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspace := filepath.Join(root, "code-session")
	if err := os.MkdirAll(filepath.Join(workspace, "output"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(workspace, "git", "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(workspace, "git", "config", "user.name", "test"); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(workspace, "git", "config", "user.email", "test@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(workspace, "git", "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(workspace, "git", "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("after\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "output", "result.md"), []byte("result\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var listed map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "code-session", "operation": "list", "path": "output",
	}), &listed)
	entries, _ := listed["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("workspace list = %#v", listed)
	}

	var changes map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "code-session", "operation": "changes",
	}), &changes)
	if changes["gitStatus"] != "ready" {
		t.Fatalf("workspace changes git status = %#v", changes)
	}

	var diff map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "code-session", "operation": "diff", "path": "README.md",
	}), &diff)
	if diff["baseline"] == nil {
		t.Fatalf("workspace diff missing baseline: %#v", diff)
	}

	var search map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "code-session", "operation": "search", "query": "result",
	}), &search)
	if len(search["entries"].([]interface{})) != 1 {
		t.Fatalf("workspace search = %#v", search)
	}
	var history map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "code-session", "operation": "git-log", "ref": "main",
	}), &history)
	if len(history["commits"].([]interface{})) != 1 {
		t.Fatalf("workspace history = %#v", history)
	}
	if err := os.WriteFile(filepath.Join(workspace, "output", "中文 report.md"), []byte("literal filename\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result := mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "code-session", "operation": "changes",
	})
	if !strings.Contains(result, "中文 report.md") {
		t.Fatalf("workspace changes quoted a literal filename: %s", result)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadNewSessionIsEmptyWithoutCreatingFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	for _, operation := range []string{"list", "changes", "search", "git-summary", "git-log"} {
		mustWorkspaceRead(t, map[string]interface{}{"session_id": "not-created", "operation": operation})
	}
	if _, err := os.Stat(filepath.Join(root, "not-created")); !os.IsNotExist(err) {
		t.Fatalf("read created a workspace: %v", err)
	}
}

func TestWorkspaceReadFindsDeliverablesWithoutGitAndPreservesBinary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspace := filepath.Join(root, "new-session")
	if err := os.MkdirAll(filepath.Join(workspace, "output"), 0755); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0}
	if err := os.WriteFile(filepath.Join(workspace, "output", "image.png"), png, 0644); err != nil {
		t.Fatal(err)
	}
	var changes map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "new-session", "operation": "changes",
	}), &changes)
	if changes["gitStatus"] != "no_repository" || len(changes["changes"].([]interface{})) != 1 {
		t.Fatalf("non-Git deliverables = %#v", changes)
	}
	var file map[string]interface{}
	decodeWorkspaceRead(t, mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "new-session", "operation": "read", "path": "output/image.png",
	}), &file)
	decoded, err := base64.StdEncoding.DecodeString(file["content"].(string))
	if err != nil || !bytes.Equal(decoded, png) || file["encoding"] != "base64" || file["contentType"] != "image/png" || file["binary"] != true {
		t.Fatalf("binary deliverable = %#v; decode=%v", file, err)
	}
}

func TestWorkspaceReadCannotFollowLinksOutsideSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	workspace := filepath.Join(root, "isolated")
	outside := t.TempDir()
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "private.txt"), []byte("outside-canary"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(workspace, "git", "init"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"list", "read", "diff"} {
		path := "escape/private.txt"
		if operation == "list" {
			path = "escape"
		}
		input, _ := json.Marshal(map[string]interface{}{"session_id": "isolated", "operation": operation, "path": path})
		if result, err := handleWorkspaceRead(input); err == nil || strings.Contains(result, "outside-canary") {
			t.Fatalf("%s followed a link outside the workspace: result=%q err=%v", operation, result, err)
		}
	}
	result := mustWorkspaceRead(t, map[string]interface{}{
		"session_id": "isolated", "operation": "search", "query": "outside-canary",
	})
	if strings.Contains(result, "private.txt") {
		t.Fatalf("search followed an external link: %s", result)
	}
}

func mustWorkspaceRead(t *testing.T, input map[string]interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handleWorkspaceRead(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeWorkspaceRead(t *testing.T, value string, target interface{}) {
	t.Helper()
	if err := json.Unmarshal([]byte(value), target); err != nil {
		t.Fatalf("decode workspace read: %v; value=%s", err, value)
	}
}
