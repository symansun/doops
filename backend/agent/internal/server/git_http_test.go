package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func gitHTTPTestPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	requireEventually(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		return pid > 0
	})
	return pid
}

func requireGitHTTPProcessGone(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(-pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("handler returned while Git process group %d was still present: %v", pid, err)
	}
}

type gitFailingWriter struct {
	http.ResponseWriter
	started string
	pid     int
}

func (w *gitFailingWriter) Write([]byte) (int, error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(w.started)
		if err == nil {
			w.pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if w.pid > 0 {
				return 0, errors.New("client output closed")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0, errors.New("Git process did not start")
}

func TestGitHTTPOutputFailureJoinsProcessGroup(t *testing.T) {
	root := t.TempDir()
	started := filepath.Join(root, "started")
	gitPath := filepath.Join(root, "git-helper")
	script := fmt.Sprintf("#!/bin/sh\ntrap '' TERM\nprintf '%%s\\n' \"$$\" > %q\nwhile :; do sleep 1; done\n", started)
	if err := os.WriteFile(gitPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, root, "init", "--bare", "cancel.git")
	handler := &gitHTTPHandler{root: root, gitPath: gitPath}
	writer := &gitFailingWriter{ResponseWriter: httptest.NewRecorder(), started: started}
	t.Cleanup(func() {
		if writer.pid > 0 {
			_ = syscall.Kill(-writer.pid, syscall.SIGKILL)
		}
	})
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/cancel.git/info/refs?service=git-upload-pack", nil))
	if writer.pid <= 0 {
		t.Fatal("test did not observe a running Git process")
	}
	requireGitHTTPProcessGone(t, writer.pid)
}

type gitFailingBody struct {
	ready chan struct{}
}

func (b *gitFailingBody) Read([]byte) (int, error) {
	<-b.ready
	return 0, errors.New("client input closed")
}

func (b *gitFailingBody) Close() error { return nil }

func TestGitHTTPInputFailureAndCancellationJoinProcessGroup(t *testing.T) {
	for _, failure := range []string{"input", "cancel", "init"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			started := filepath.Join(root, "started")
			gitPath := filepath.Join(root, "git-helper")
			script := fmt.Sprintf("#!/bin/sh\ntrap '' TERM\nprintf '%%s\\n' \"$$\" > %q\nwhile :; do sleep 1; done\n", started)
			if err := os.WriteFile(gitPath, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			if failure != "init" {
				runGitCommand(t, root, "init", "--bare", "cancel.git")
			}
			handler := &gitHTTPHandler{root: root, gitPath: gitPath}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			body := &gitFailingBody{ready: make(chan struct{})}
			defer close(body.ready)
			request := httptest.NewRequest(http.MethodPost, "/cancel.git/git-receive-pack", body).WithContext(ctx)
			if failure != "input" {
				request = httptest.NewRequest(http.MethodGet, "/cancel.git/info/refs?service=git-upload-pack", nil).WithContext(ctx)
			}
			done := make(chan struct{})
			writer := httptest.NewRecorder()
			go func() {
				defer close(done)
				handler.ServeHTTP(writer, request)
			}()
			pid := gitHTTPTestPID(t, started)
			defer syscall.Kill(-pid, syscall.SIGKILL)
			if failure == "input" {
				body.ready <- struct{}{}
			} else {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(12 * time.Second):
				t.Fatal("Git handler did not complete bounded cleanup")
			}
			requireGitHTTPProcessGone(t, pid)
		})
	}
}

func TestGitHTTPFailureAfterHeadersIsUnknownTerminal(t *testing.T) {
	root := t.TempDir()
	gitPath := filepath.Join(root, "git-helper")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nprintf partial-advertisement\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, root, "init", "--bare", "failed.git")
	handler := &gitHTTPHandler{root: root, gitPath: gitPath}
	conn, messages := gitCancellationTestConnection(t, http.StripPrefix("/git", handler))
	gitCancellationTestSend(t, conn, 42, "git/http", gitHTTPRequestParams{
		Method: http.MethodGet, Path: "/git/failed.git/info/refs", RawQuery: "service=git-upload-pack",
	})
	headerSeen := false
	for {
		message := gitCancellationTestMessage(t, messages)
		if message["method"] == "git/response" {
			params, _ := message["params"].(map[string]interface{})
			headerSeen = params["status"] == float64(http.StatusOK)
		}
		if message["error"] != nil {
			gitCancellationTestTerminal(t, message, 42, -32008, "execution_outcome_unknown")
			break
		}
		params, _ := message["params"].(map[string]interface{})
		if message["result"] != nil || params["eof"] == true {
			t.Fatalf("failed Git process emitted a success terminal: %#v", message)
		}
	}
	if !headerSeen {
		t.Fatal("test did not exercise failure after HTTP 200")
	}
}

func TestGitHTTPUploadPackWaitsForRequestEOF(t *testing.T) {
	for _, finish := range []string{"eof", "cancel"} {
		t.Run(finish, func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "repo")
			runGitCommand(t, root, "init", "-b", "master", src)
			runGitCommand(t, src, "-c", "user.name=doops", "-c", "user.email=doops@example.com", "commit", "--allow-empty", "-m", "delayed eof")
			commit := strings.TrimSpace(runGitCommand(t, src, "rev-parse", "HEAD"))
			gitPath, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			exited := filepath.Join(root, "git-exited")
			wrapper := filepath.Join(root, "git-wrapper")
			script := fmt.Sprintf("#!/bin/sh\n%q \"$@\"\nstatus=$?\nprintf '%%s\\n' \"$status\" > %q\nexit \"$status\"\n", gitPath, exited)
			if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			done := make(chan error, 1)
			go func() {
				done <- runGitHTTPCommand(ctx, exec.Command(wrapper, "upload-pack", "--stateless-rpc", src), reader, io.Discard, "")
			}()
			want := "want " + commit + "\n"
			if _, err := fmt.Fprintf(writer, "%04x%s00000009done\n", len(want)+4, want); err != nil {
				t.Fatal(err)
			}
			requireEventually(t, 3*time.Second, func() bool {
				data, err := os.ReadFile(exited)
				return err == nil && strings.TrimSpace(string(data)) == "0"
			})
			select {
			case err := <-done:
				t.Fatalf("Git closed the request before its delayed EOF: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if finish == "cancel" {
				cancel()
			} else {
				_ = writer.Close()
			}
			select {
			case err := <-done:
				if (finish == "eof" && err != nil) || (finish == "cancel" && !errors.Is(err, context.Canceled)) {
					t.Fatalf("unexpected result after %s: %v", finish, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Git input completion did not respond to EOF/cancellation")
			}
		})
	}
}

func TestGitHTTPInterruptedInitRestoresRequiredHook(t *testing.T) {
	root := t.TempDir()
	repos := filepath.Join(root, "repos")
	marker := filepath.Join(root, "hook-completed")
	hook := fmt.Sprintf("#!/bin/sh\nprintf complete > %q\n", marker)
	handler, err := newGitHTTPHandler(repos, hook)
	if err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	initialized := filepath.Join(root, "init-pid")
	wrapper := filepath.Join(root, "git-wrapper")
	script := fmt.Sprintf("#!/bin/sh\n%q \"$@\" || exit $?\nprintf '%%s\\n' \"$$\" > %q\nwhile :; do sleep 1; done\n", gitPath, initialized)
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	handler.gitPath = wrapper
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/retry.git/info/refs?service=git-receive-pack", nil).WithContext(ctx))
	}()
	pid := gitHTTPTestPID(t, initialized)
	defer syscall.Kill(-pid, syscall.SIGKILL)
	cancel()
	select {
	case <-done:
	case <-time.After(12 * time.Second):
		t.Fatal("interrupted init did not stop")
	}
	requireGitHTTPProcessGone(t, pid)
	if _, err := os.Stat(filepath.Join(repos, "retry.git", "hooks", "post-receive")); !os.IsNotExist(err) {
		t.Fatalf("test did not reproduce the interrupted hook installation: %v", err)
	}
	// This models a new request after explicit administrator reconciliation.
	handler.gitPath = gitPath
	server := httptest.NewServer(handler)
	defer server.Close()
	src := t.TempDir()
	runGitCommand(t, src, "init", "-b", "master")
	runGitCommand(t, src, "-c", "user.name=doops", "-c", "user.email=doops@example.com", "commit", "--allow-empty", "-m", "retry")
	runGitCommand(t, src, "push", server.URL+"/retry.git", "HEAD:master")
	if data, err := os.ReadFile(marker); err != nil || string(data) != "complete" {
		t.Fatalf("push skipped the required post-receive hook after interrupted init: %q, %v", data, err)
	}
}

func TestGatewayGitCancellationStopsRealReceiveHook(t *testing.T) {
	root := t.TempDir()
	started := filepath.Join(root, "hook-pid")
	childStarted := filepath.Join(root, "child-pid")
	hook := fmt.Sprintf(`#!/bin/sh
trap '' TERM
printf '%%s\n' "$$" > %q
sh -c 'trap "" TERM; printf "%%s\n" "$$" > "$1"; while :; do sleep 1; done' sh %q &
wait
`, started, childStarted)
	handler, err := newGitHTTPHandler(filepath.Join(root, "repos"), hook)
	if err != nil {
		t.Fatal(err)
	}
	hub, server, tokens := newGatewayGitE2E(t, []gatewayGitE2ETarget{{Cluster: "dev", Instance: "local"}})
	defer server.Close()
	agent := &Gateway{gitHandler: http.StripPrefix("/git", handler)}
	conn := dialFakeGatewayAgent(t, server.URL, "dev", "local")
	defer conn.Close()
	go agent.ServeGatewayWebSocketConnWithReady(conn, "real-git-hook", nil)
	waitForGatewayAgent(t, hub, "dev", "local")
	src := t.TempDir()
	runGitCommand(t, src, "init", "-b", "master")
	runGitCommand(t, src, "-c", "user.name=doops", "-c", "user.email=doops@example.com", "commit", "--allow-empty", "-m", "hook test")
	repoURL := gatewayGitTestURL(t, server.URL, "dev", "local", "hook", tokens.Targets[0].UserToken)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	push := exec.CommandContext(ctx, "git", "-C", src, "push", repoURL, "HEAD:master")
	pushDone := make(chan error, 1)
	if err := push.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { pushDone <- push.Wait() }()
	hookPID, childPID := gitHTTPTestPID(t, started), gitHTTPTestPID(t, childStarted)
	operations := hub.listActiveOperations()
	if len(operations) != 1 || operations[0].State != "running" || operations[0].Session != "hook" {
		t.Fatalf("real Git push is not owned by Gateway: %#v", operations)
	}
	op := operations[0]
	if !hub.cancelActiveOperation(op.ID) {
		t.Fatal("Gateway did not accept cancellation")
	}
	if operations := hub.listActiveOperations(); len(operations) != 1 || operations[0].State != "cancel_requested" {
		t.Fatalf("cancellation acknowledgement released running hook ownership: %#v", operations)
	}
	requireEventually(t, 12*time.Second, func() bool { return len(hub.listActiveOperations()) == 0 })
	for _, pid := range []int{hookPID, childPID} {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("Gateway released operation before receive hook process %d stopped: %v", pid, err)
		}
	}
	select {
	case err := <-pushDone:
		if err == nil {
			t.Fatal("interrupted real Git push unexpectedly succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Git client did not observe failed push")
	}
	audits, err := hub.store.ListAudit(10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, audit := range audits {
		if audit.ID == op.AuditID {
			found = audit.Status == "outcome_unknown"
		}
	}
	if !found {
		t.Fatalf("interrupted receive hook lost uncertainty guard: %#v", audits)
	}
	response, err := http.Get(repoURL + "/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusConflict || !strings.Contains(string(body), "execution_outcome_unknown") {
		t.Fatalf("Gateway allowed work before hook reconciliation: HTTP %d %q, %v", response.StatusCode, body, err)
	}
}
