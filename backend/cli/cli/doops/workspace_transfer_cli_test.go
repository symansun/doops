package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Adapted from the R13/R17 review fixture: real CLI, Git HTTP and production
// checkout hook, with only the Agent's absolute directories mapped into TempDir.
type workspaceTransferFixture struct {
	root, bare, workspace string
	failDownload          atomic.Bool
	gitRequests           atomic.Int32
	replaceSnapshotRef    atomic.Bool
	snapshotCommit        atomic.Value
}

func transferFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func transferGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := gitCommandOutput(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(output)
}

func newWorkspaceTransferFixture(t *testing.T) *workspaceTransferFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(root, "state", "config.json"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("TMPDIR", root)
	f := &workspaceTransferFixture{
		root: root, bare: filepath.Join(root, "repos", "review.git"),
		workspace: filepath.Join(root, "workspaces", "review"),
	}
	if err := os.MkdirAll(f.workspace, 0755); err != nil {
		t.Fatal(err)
	}
	transferGit(t, root, "init", "--bare", "--initial-branch=master", f.bare)
	transferGit(t, root, "--git-dir", f.bare, "config", "http.receivepack", "true")
	source, err := os.ReadFile("../../../agent/internal/server/gateway.go")
	if err != nil {
		t.Fatal(err)
	}
	_, hook, found := strings.Cut(string(source), "newGitHTTPHandler(\"/tmp/repos\", `")
	if !found {
		t.Fatal("production hook start not found")
	}
	hook, _, found = strings.Cut(hook, "`)")
	if !found {
		t.Fatal("production hook end not found")
	}
	hook = strings.ReplaceAll(hook, "/root/ws", filepath.Join(root, "workspaces"))
	hook = strings.Replace(hook, "set -e\n", "set -e\nprintf 'hook\\n' >> "+shellQuote(filepath.Join(root, "hook-calls"))+"\n", 1)
	hookPath := filepath.Join(f.bare, "hooks", "post-receive")
	transferFile(t, hookPath, hook)
	if err := os.Chmod(hookPath, 0755); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitHTTP := http.StripPrefix("/v1/git/review/node", &cgi.Handler{
		Path: gitPath, Args: []string{"http-backend"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + filepath.Dir(f.bare), "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/git/", func(w http.ResponseWriter, r *http.Request) {
		f.gitRequests.Add(1)
		if f.failDownload.Load() {
			http.Error(w, "synthetic download failure", http.StatusServiceUnavailable)
			return
		}
		gitHTTP.ServeHTTP(w, r)
	})
	mux.HandleFunc("/v1/rpc", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if conn.ReadJSON(&req) != nil {
				return
			}
			if req["method"] == "initialize" {
				_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": req["id"], "result": map[string]interface{}{}})
				continue
			}
			params, ok := req["params"].(map[string]interface{})
			if !ok || req["method"] != "tools/call" || params["name"] != "doops_shell" {
				t.Errorf("unexpected RPC: %#v", req)
				return
			}
			args := params["arguments"].(map[string]interface{})
			command := args["command"].(string)
			if !strings.Contains(command, "/root/ws/review") {
				t.Errorf("unexpected shell command: %q", command)
				return
			}
			command = strings.ReplaceAll(command, "/root/ws", filepath.Join(root, "workspaces"))
			command = strings.ReplaceAll(command, "/tmp/repos", filepath.Dir(f.bare))
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			cmd := exec.CommandContext(ctx, "bash", "-c", command)
			cmd.Dir = root
			output, commandErr := cmd.CombinedOutput()
			cancel()
			if commandErr == nil && f.replaceSnapshotRef.Swap(false) {
				commit, parseErr := pullSnapshotCommit(string(output))
				if parseErr != nil {
					t.Error(parseErr)
					return
				}
				f.snapshotCommit.Store(commit)
				tree, gitErr := gitCommandOutput(root, "--git-dir", f.bare, "rev-parse", "master^{tree}")
				if gitErr != nil {
					t.Error(gitErr)
					return
				}
				replacement, gitErr := gitCommandOutput(root, "--git-dir", f.bare,
					"-c", "user.name=review", "-c", "user.email=review@localhost",
					"commit-tree", strings.TrimSpace(tree), "-m", "competing download")
				if gitErr == nil {
					_, gitErr = gitCommandOutput(root, "--git-dir", f.bare, "update-ref",
						"refs/heads/doops-pull", strings.TrimSpace(replacement))
				}
				if gitErr != nil {
					t.Error(gitErr)
					return
				}
			}
			scanner := bufio.NewScanner(strings.NewReader(string(output)))
			for scanner.Scan() {
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "method": "notifications/message",
					"params": map[string]interface{}{"request_id": req["id"], "kind": "raw", "data": scanner.Text() + "\n"},
				})
			}
			text := "Operation complete."
			if commandErr != nil {
				text = fmt.Sprintf("Command failed with error: %v", commandErr)
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]interface{}{"isError": commandErr != nil,
					"content": []map[string]string{{"type": "text", "text": text}}},
			})
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	if err := saveServers([]Server{{Name: "local-review", Gateway: server.URL, Cluster: "review", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(server.URL, "synthetic-local-fixture")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	return f
}

func transferCLI(t *testing.T, binary, command string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append([]string{"-session", "review", command, "--target", "local-review"}, args...)...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("CLI exceeded fixture deadline: %v\n%s", ctx.Err(), output)
	}
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	t.Logf("%s exit=%d\n%s", command, code, output)
	return code, string(output)
}

func TestWorkspaceTransferCLI(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	t.Run("push_failure_is_not_replayed", func(t *testing.T) {
		f := newWorkspaceTransferFixture(t)
		src := filepath.Join(f.root, "src")
		transferFile(t, filepath.Join(src, "main.txt"), "source\n")
		f.failDownload.Store(true)
		code, _ := transferCLI(t, binary, "push", "--storage", "gateway", "--src", src)
		if code != 1 || f.gitRequests.Load() != 1 {
			t.Fatalf("failed push replayed or succeeded: exit=%d requests=%d", code, f.gitRequests.Load())
		}
		data, err := os.ReadFile(filepath.Join(src, "main.txt"))
		if err != nil || string(data) != "source\n" {
			t.Fatalf("failed push changed source: %q, %v", data, err)
		}
	})
	t.Run("push_preserves_filtered_tracked_files", func(t *testing.T) {
		f := newWorkspaceTransferFixture(t)
		src := filepath.Join(f.root, "src")
		transferFile(t, filepath.Join(src, ".gitignore"), "*.json\n")
		transferFile(t, filepath.Join(src, "package.json"), "{\"name\":\"required-manifest\"}\n")
		transferFile(t, filepath.Join(src, "main.txt"), "source\n")
		transferGit(t, src, "init", "-b", "master")
		transferGit(t, src, "add", ".gitignore", "main.txt")
		transferGit(t, src, "add", "-f", "package.json")
		transferGit(t, src, "-c", "user.name=review", "-c", "user.email=review@localhost", "commit", "-m", "source")
		transferFile(t, filepath.Join(src, "ignored.json"), "untracked\n")
		status := transferGit(t, src, "status", "--porcelain")
		code, output := transferCLI(t, binary, "push", "--storage", "gateway", "--src", src, "--dry-run")
		if code != 0 || !strings.Contains(output, "package.json") || strings.Contains(output, "ignored.json") {
			t.Fatalf("incorrect selected files: %s", output)
		}
		code, _ = transferCLI(t, binary, "push", "--storage", "gateway", "--src", src)
		tree := transferGit(t, f.root, "--git-dir", f.bare, "ls-tree", "-r", "--name-only", "master")
		data, err := os.ReadFile(filepath.Join(f.workspace, "package.json"))
		if code != 0 || tree != ".gitignore\nmain.txt\npackage.json" || err != nil || string(data) != "{\"name\":\"required-manifest\"}\n" {
			t.Fatalf("snapshot differs from selected files: exit=%d tree=%q data=%q err=%v", code, tree, data, err)
		}
		if got := transferGit(t, src, "status", "--porcelain"); got != status {
			t.Fatalf("push changed source status: %q -> %q", status, got)
		}
	})
	for _, metadata := range []string{"directory", "gitfile"} {
		t.Run("pull_nested_repository_"+metadata, func(t *testing.T) {
			f := newWorkspaceTransferFixture(t)
			src := filepath.Join(f.root, "src")
			transferFile(t, filepath.Join(src, "main.txt"), "source\n")
			if code, _ := transferCLI(t, binary, "push", "--storage", "gateway", "--src", src); code != 0 {
				t.Fatal("setup push failed")
			}
			nested := filepath.Join(f.workspace, "project")
			transferFile(t, filepath.Join(nested, ".gitignore"), "dist/\n")
			transferFile(t, filepath.Join(nested, "source.txt"), "nested source\n")
			gitDir := filepath.Join(nested, ".git")
			initArgs := []string{"init", "-b", "master"}
			if metadata == "gitfile" {
				gitDir = filepath.Join(f.root, "nested-git")
				initArgs = append(initArgs, "--separate-git-dir", gitDir)
			}
			transferGit(t, nested, initArgs...)
			transferGit(t, nested, "add", ".")
			transferGit(t, nested, "-c", "user.name=review", "-c", "user.email=review@localhost", "commit", "-m", "nested")
			artifactPath := filepath.Join("project", "dist", "report.zip")
			artifact := filepath.Join(f.workspace, artifactPath)
			content := "nested-only-generated-copy\x00\n"
			transferFile(t, artifact, content)
			specialPath := filepath.Join("project", "dist", "space tab\tnewline\n.zip")
			transferFile(t, filepath.Join(f.workspace, specialPath), "special name\n")
			if err := os.Symlink("dist/report.zip", filepath.Join(nested, "report-link")); err != nil {
				t.Fatal(err)
			}
			preserved := map[string][]byte{}
			paths := []string{
				filepath.Join(f.bare, "refs", "heads", "master"),
				filepath.Join(f.bare, "index"), filepath.Join(f.root, "hook-calls"),
				filepath.Join(f.workspace, ".doops-ready"),
			}
			for _, name := range []string{"HEAD", "index", "config", "refs/heads/master"} {
				paths = append(paths, filepath.Join(gitDir, name))
			}
			if metadata == "gitfile" {
				paths = append(paths, filepath.Join(nested, ".git"))
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				preserved[path] = data
			}
			expectedFiles := strings.Join([]string{
				".doops-ready", "main.txt", "project/.gitignore",
				"project/dist/report.zip", filepath.ToSlash(specialPath),
				"project/report-link", "project/source.txt", "",
			}, "\x00")
			for _, failDownload := range []bool{false, true} {
				f.failDownload.Store(failDownload)
				dest := filepath.Join(f.root, fmt.Sprintf("download-%t", failDownload))
				code, _ := transferCLI(t, binary, "pull", "--storage", "gateway", "--dest", dest)
				wantCode := 0
				if failDownload {
					wantCode = 1
				}
				if code != wantCode {
					t.Fatalf("pull exit=%d, want %d", code, wantCode)
				}
				tree := transferGit(t, f.root, "--git-dir", f.bare, "ls-tree", "-r", "-z", "--name-only", "doops-pull")
				if tree != expectedFiles {
					t.Fatalf("snapshot omitted files or included Git metadata: got %q want %q", tree, expectedFiles)
				}
				for path, before := range preserved {
					after, err := os.ReadFile(path)
					if err != nil || string(after) != string(before) {
						t.Fatalf("pull changed %s: %v", path, err)
					}
				}
				data, err := os.ReadFile(artifact)
				if err != nil || string(data) != content {
					t.Fatalf("nested source lost: %q, %v", data, err)
				}
				if !failDownload {
					data, err := os.ReadFile(filepath.Join(dest, artifactPath))
					if err != nil || string(data) != content {
						t.Fatalf("nested artifact missing: %q, %v", data, err)
					}
					data, err = os.ReadFile(filepath.Join(dest, specialPath))
					if err != nil || string(data) != "special name\n" {
						t.Fatalf("special filename lost: %q, %v", data, err)
					}
					link, err := os.Readlink(filepath.Join(dest, "project", "report-link"))
					if err != nil || link != "dist/report.zip" {
						t.Fatalf("symlink changed: %q, %v", link, err)
					}
				}
				t.Logf("metadata=%s failed_download=%t nested_artifact_preserved=true CLI_exit=%d", metadata, failDownload, code)
			}
		})
	}
	for _, failure := range []string{"none", "source_git", "ref_race", "download", "snapshot"} {
		t.Run("pull_preserves_source_"+failure, func(t *testing.T) {
			f := newWorkspaceTransferFixture(t)
			src := filepath.Join(f.root, "src")
			transferFile(t, filepath.Join(src, ".gitignore"), "dist/\n")
			transferFile(t, filepath.Join(src, "main.txt"), "source\n")
			if code, _ := transferCLI(t, binary, "push", "--storage", "gateway", "--src", src); code != 0 {
				t.Fatal("setup push failed")
			}
			ready, err := os.ReadFile(filepath.Join(f.workspace, ".doops-ready"))
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(f.workspace, "dist", "report.zip")
			content := "only-generated-copy\x00\n"
			transferFile(t, artifact, content)
			if failure == "source_git" {
				transferGit(t, f.workspace, "init", "-b", "master")
				transferGit(t, f.workspace, "add", ".")
				transferGit(t, f.workspace, "-c", "user.name=review", "-c", "user.email=review@localhost", "commit", "-m", "source metadata")
				transferFile(t, filepath.Join(f.workspace, "main.txt"), "staged source\n")
				transferGit(t, f.workspace, "add", "main.txt")
				transferFile(t, filepath.Join(f.workspace, "main.txt"), "unstaged source\n")
			}
			preserved := map[string][]byte{}
			paths := []string{filepath.Join(f.bare, "refs", "heads", "master"), filepath.Join(f.bare, "index"), filepath.Join(f.root, "hook-calls")}
			if failure == "source_git" {
				for _, name := range []string{"HEAD", "index", "config", "refs/heads/master"} {
					paths = append(paths, filepath.Join(f.workspace, ".git", name))
				}
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				preserved[path] = data
			}
			dest := filepath.Join(f.root, "download")
			if failure == "ref_race" {
				f.replaceSnapshotRef.Store(true)
			}
			if failure == "download" {
				f.failDownload.Store(true)
			}
			if failure == "snapshot" {
				// A real Git object-store failure must not touch the source.
				objects := filepath.Join(f.bare, "objects")
				if err := os.Rename(objects, objects+".saved"); err != nil {
					t.Fatal(err)
				}
				transferFile(t, objects, "not an object directory\n")
			}
			code, _ := transferCLI(t, binary, "pull", "--storage", "gateway", "--dest", dest)
			wantCode := 0
			if failure == "download" || failure == "snapshot" {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("pull exit=%d, want %d", code, wantCode)
			}
			data, err := os.ReadFile(artifact)
			if err != nil || string(data) != content {
				t.Fatalf("source lost on %s: %q, %v", failure, data, err)
			}
			if failure != "source_git" {
				if _, err := os.Stat(filepath.Join(f.workspace, ".git")); !os.IsNotExist(err) {
					t.Fatalf("pull created a source repository: %v", err)
				}
			}
			after, err := os.ReadFile(filepath.Join(f.workspace, ".doops-ready"))
			if err != nil || string(after) != string(ready) {
				t.Fatalf("deployment sentinel changed: %q, %v", after, err)
			}
			for path, before := range preserved {
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatalf("pull changed %s: %v", path, err)
				}
			}
			if wantCode == 0 {
				data, err := os.ReadFile(filepath.Join(dest, "dist", "report.zip"))
				if err != nil || string(data) != content {
					t.Fatalf("missing download: %q, %v", data, err)
				}
				if failure == "ref_race" {
					head := transferGit(t, dest, "rev-parse", "HEAD")
					if head != f.snapshotCommit.Load().(string) {
						t.Fatalf("download followed the replaced ref: %s", head)
					}
				}
				if failure == "source_git" {
					data, err := os.ReadFile(filepath.Join(dest, "main.txt"))
					if err != nil || string(data) != "unstaged source\n" {
						t.Fatalf("download used the source index instead of its worktree: %q, %v", data, err)
					}
				}
			}
			if failure == "none" {
				transferFile(t, artifact, "second snapshot\n")
				if code, _ := transferCLI(t, binary, "pull", "--storage", "gateway", "--dest", dest); code != 0 {
					t.Fatal("pull into existing repository failed")
				}
				data, err = os.ReadFile(filepath.Join(dest, "dist", "report.zip"))
				if err != nil || string(data) != "second snapshot\n" {
					t.Fatalf("existing destination not updated: %q, %v", data, err)
				}
			}
			t.Logf("failure=%s source_preserved=true CLI_exit=%d", failure, code)
		})
	}
}
