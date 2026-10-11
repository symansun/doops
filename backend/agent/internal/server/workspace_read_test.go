package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal(err)
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
