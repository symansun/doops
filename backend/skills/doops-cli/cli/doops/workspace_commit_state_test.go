package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceCommitStateBindsTargetAndSession(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("DOOPS_CONFIG", configPath)
	commit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if err := saveWorkspaceCommit("gateway", "release-1", commit); err != nil {
		t.Fatal(err)
	}
	got, err := loadWorkspaceCommit("gateway", "release-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != commit {
		t.Fatalf("commit = %q", got)
	}
	if _, err := loadWorkspaceCommit("gateway", "release-2"); err == nil {
		t.Fatal("different session reused workspace commit state")
	}
	info, err := os.Stat(workspaceCommitStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("workspace commit state mode = %o", info.Mode().Perm())
	}
}
