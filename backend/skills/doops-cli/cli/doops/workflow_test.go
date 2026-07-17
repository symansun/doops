package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowPathInGitRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "deploy", "environments.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("apiVersion: doops.sh/v3\nkind: EnvironmentRegistry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotRoot, relative, err := workflowPathInGitRepository(path)
	if err != nil {
		t.Fatalf("resolve workflow path: %v", err)
	}
	if gotRoot != root || relative != "deploy/environments.yaml" {
		t.Fatalf("root=%q relative=%q", gotRoot, relative)
	}
}

func TestValidWorkspaceCommit(t *testing.T) {
	if !validWorkspaceCommit(strings.Repeat("a", 40)) {
		t.Fatal("valid commit was rejected")
	}
	if validWorkspaceCommit("main") {
		t.Fatal("branch name was accepted as commit")
	}
}
