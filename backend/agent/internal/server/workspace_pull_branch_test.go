package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePullExportsCurrentSnapshotAcrossBranches(t *testing.T) {
	for _, branch := range []string{"main", "stale-master", "detached"} {
		t.Run(branch, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DOOPS_WORKSPACE_ROOT", root)
			t.Setenv("TMPDIR", t.TempDir())
			session := "pull-branch-" + generateID()
			workspace := filepath.Join(root, session)
			if err := os.Mkdir(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = workspace
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-b", "main")
			git("config", "user.name", "test")
			git("config", "user.email", "test@example.invalid")
			artifact := filepath.Join(workspace, "artifact.txt")
			if err := os.WriteFile(artifact, []byte("old-artifact"), 0644); err != nil {
				t.Fatal(err)
			}
			git("add", "artifact.txt")
			git("-c", "commit.gpgsign=false", "commit", "-m", "initial")
			if branch == "stale-master" {
				git("branch", "master")
			} else if branch == "detached" {
				git("checkout", "--detach")
			}
			originalBranch := git("rev-parse", "--abbrev-ref", "HEAD")
			if err := os.WriteFile(artifact, []byte("new-artifact"), 0644); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Join("/tmp/repos", session+".git")) })
			payload, err := handleWorkspacePullBegin(mustJSON(t, map[string]interface{}{"session_id": session}))
			if err != nil {
				t.Fatal(err)
			}
			var result workspacePullBeginResult
			if err := json.Unmarshal([]byte(payload), &result); err != nil {
				t.Fatal(err)
			}
			bundle := workspacePullBundlePath(result.BundleID)
			t.Cleanup(func() { _ = os.Remove(bundle) })
			restored := filepath.Join(t.TempDir(), "restored")
			git("clone", "-b", "master", bundle, restored)
			content, err := os.ReadFile(filepath.Join(restored, "artifact.txt"))
			if err != nil || string(content) != "new-artifact" {
				t.Fatalf("exported artifact = %q, err=%v", content, err)
			}
			if got := git("rev-parse", "--abbrev-ref", "HEAD"); got != originalBranch {
				t.Fatalf("export changed active branch from %s to %s", originalBranch, got)
			}
			if branch == "stale-master" && git("show", "master:artifact.txt") != "old-artifact" {
				t.Fatal("export changed the user's master branch")
			}
		})
	}
}
