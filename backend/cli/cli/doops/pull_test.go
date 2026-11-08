package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGitWorkspaceCommitCommand(t *testing.T) {
	got := buildGitWorkspaceCommitCommand("release-1")
	for _, want := range []string{
		"cd '/root/ws/release-1'",
		"GIT_INDEX_FILE=\"$snapshot/index\"",
		"find . -name .git -prune",
		"update-index --add -z --stdin",
		"cmp \"$snapshot/files\" \"$snapshot/tree-files\"",
		"commit-tree \"$tree\" -m DoopsPullSnapshot",
		"update-ref refs/heads/doops-pull \"$commit\"",
	} {
		if !contains(got, want) {
			t.Fatalf("command missing %q\n%s", want, got)
		}
	}
}

func TestPullGitWorkspaceClonesFromBundle(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "course.tgz"), []byte("course data"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runTestGit(t, src, "init", "-b", "master")
	runTestGit(t, src, "config", "user.name", "doops")
	runTestGit(t, src, "config", "user.email", "doops@localhost")
	runTestGit(t, src, "add", "-A")
	runTestGit(t, src, "commit", "-m", "snapshot")
	bundle := filepath.Join(root, "workspace.bundle")
	runTestGit(t, src, "bundle", "create", bundle, "master")
	commit, err := gitCommandOutput(src, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(root, "dest")
	if err := pullGitWorkspace(bundle, "", dest, strings.TrimSpace(commit)); err != nil {
		t.Fatalf("pull bundle: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "course.tgz"))
	if err != nil {
		t.Fatalf("read pulled file: %v", err)
	}
	if string(got) != "course data" {
		t.Fatalf("pulled content mismatch: %q", got)
	}
}

func TestPullSnapshotCommitRequiresOneValidMarker(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, output string
		valid        bool
	}{
		{"valid", "progress\npull-commit:" + commit + "\nOperation complete.", true},
		{"missing", "Operation complete.", false},
		{"invalid", "pull-commit:not-a-commit\n", false},
		{"duplicate", "pull-commit:" + commit + "\npull-commit:" + commit, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pullSnapshotCommit(tc.output)
			if (err == nil) != tc.valid || (tc.valid && got != commit) {
				t.Fatalf("snapshot marker = %q, %v", got, err)
			}
		})
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
