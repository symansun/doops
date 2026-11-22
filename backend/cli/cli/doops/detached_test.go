package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestExecBgFailsClosedWithoutDetachedCapability(t *testing.T) {
	var calls atomic.Int32
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			if req["method"] == "initialize" {
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "id": req["id"],
					"result": map[string]interface{}{
						"capabilities": map[string]interface{}{
							"protocols": map[string]interface{}{
								"executionLifecycle": executionLifecycleCapability(),
							},
						},
					},
				})
				continue
			}
			calls.Add(1)
		}
	}))
	defer gateway.Close()
	client := newGatewayTestClient(t, gateway.URL, "detached-gate")
	defer client.Close()
	err := client.ExecBg("sleep 1", "test")
	if err == nil || !strings.Contains(err.Error(), "Gateway does not support detached background tasks (detachedBackground); upgrade the Gateway") {
		t.Fatalf("missing detachedBackground capability was not rejected: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("capability-less gateway received %d tools/call requests", calls.Load())
	}
}

// Ctrl-C releases the stream without tools/cancel; the task stays with the
// Gateway and the follow-up commands go to stderr with exit code 130.
func TestExecBgDetachedOnInterrupt(t *testing.T) {
	release := make(chan struct{})
	var cancels atomic.Int32
	upgrader := websocket.Upgrader{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			if req["method"] == "initialize" {
				_ = conn.WriteJSON(map[string]interface{}{
					"jsonrpc": "2.0", "id": req["id"], "result": agentInitializeResult(),
				})
				continue
			}
			if req["method"] == "tools/cancel" {
				cancels.Add(1)
				continue
			}
			params, _ := req["params"].(map[string]interface{})
			args, _ := params["arguments"].(map[string]interface{})
			if req["method"] != "tools/call" || params["name"] != "doops_bg" || args["detach"] != true {
				t.Errorf("unexpected tool request: %#v", req)
			}
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "method": "notifications/doops/background_task",
				"params": map[string]interface{}{"request_id": req["id"], "audit_id": 42, "operation_id": "op-test"},
			})
			_ = conn.WriteJSON(map[string]interface{}{
				"jsonrpc": "2.0", "method": "notifications/message",
				"params": map[string]interface{}{"request_id": req["id"], "data": "TaskID: bg-1\nworking\n"},
			})
			<-release
		}
	}))
	defer gateway.Close()
	defer close(release)

	binary := buildDoopsCLIBinary(t)
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "prod", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "test-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-session", "bg-interrupt", "exec", "--target", "test", "--bg", "--cmd", "sleep 60")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderrBuf bytes.Buffer
	command.Stderr = &stderrBuf
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	deadline := time.After(8 * time.Second)
	seenProgress := false
	for !seenProgress {
		select {
		case line := <-lines:
			seenProgress = seenProgress || line == "working"
		case <-deadline:
			t.Fatal("progress was not streamed before interrupt")
		}
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitErr := command.Wait()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("interrupt exit = %v, want 130", waitErr)
	}
	rest := stderrBuf.String()
	for _, want := range []string{
		"Detached; the task keeps running on test.",
		"doops -session bg-interrupt task status --target test --id 42",
		"doops -session bg-interrupt task stop --target test --id 42",
		"Background task: audit 42",
	} {
		if !strings.Contains(rest, want) {
			t.Errorf("detach output missing %q:\n%s", want, rest)
		}
	}
	if got := cancels.Load(); got != 0 {
		t.Fatalf("interrupt sent %d tools/cancel requests", got)
	}
}

func detachedAuthFixture(t *testing.T, gateway string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(dir, "config.json"))
	a := AuthStore{}
	a.SetGateway(gateway, "synthetic-session")
	data, err := json.Marshal(&a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTaskCommandHTTPPaths(t *testing.T) {
	var requested []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-session" {
			t.Error("missing bearer token")
		}
		requested = append(requested, r.Method+" "+r.URL.String())
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/background-tasks":
			if r.URL.Query().Get("cluster") != "prod" || r.URL.Query().Get("instance") != "node" {
				t.Errorf("task list missed target filter: %s", r.URL)
			}
			w.Write([]byte(`{"tasks":[{"audit_id":42,"operation_id":"op-x","cluster":"prod","instance":"node","session":"s1","command_summary":"sleep 5","status":"running","started_at":"2026-11-22T00:00:00Z"}]}`))
		case r.Method == "GET" && r.URL.Path == "/v1/background-tasks/42":
			w.Write([]byte(`{"audit_id":42,"operation_id":"op-x","cluster":"prod","instance":"node","session":"s1","command_summary":"sleep 5","status":"success","started_at":"2026-11-22T00:00:00Z","output":"done\n"}`))
		case r.Method == "POST" && r.URL.Path == "/v1/background-tasks/42/cancel":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"audit_id":42,"cancel_requested":true}`))
		default:
			t.Errorf("unexpected task request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer gateway.Close()
	detachedAuthFixture(t, gateway.URL)
	servers := []Server{{Name: "route", Gateway: gateway.URL, Cluster: "prod", Instance: "node"}}

	var out bytes.Buffer
	if code := runTaskCommand([]string{"list", "--target", "route"}, servers, nil, &out); code != 0 {
		t.Fatalf("task list: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "42") || !strings.Contains(out.String(), "running") {
		t.Fatalf("task list output: %s", out.String())
	}
	out.Reset()
	if code := runTaskCommand([]string{"status", "--target", "route", "--id", "42"}, servers, nil, &out); code != 0 {
		t.Fatalf("task status: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "done") || !strings.Contains(out.String(), "task 42: success") {
		t.Fatalf("task status output: %s", out.String())
	}
	out.Reset()
	if code := runTaskCommand([]string{"stop", "--target", "route", "--id", "42"}, servers, nil, &out); code != 0 {
		t.Fatalf("task stop: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Stop requested; run task status for the terminal result") {
		t.Fatalf("task stop output: %s", out.String())
	}
	want := []string{
		"GET /v1/background-tasks?cluster=prod&instance=node",
		"GET /v1/background-tasks/42",
		"POST /v1/background-tasks/42/cancel",
	}
	if fmt.Sprint(requested) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", requested, want)
	}
}

func TestTaskStatusFollowExitsOnTerminal(t *testing.T) {
	polls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		status := "running"
		output := "line 1\n"
		if polls >= 2 {
			status = "success"
			output = "line 1\nline 2\n"
		}
		fmt.Fprintf(w, `{"audit_id":42,"status":%q,"output":%q}`, status, output)
	}))
	defer gateway.Close()
	detachedAuthFixture(t, gateway.URL)
	servers := []Server{{Name: "route", Gateway: gateway.URL, Cluster: "prod", Instance: "node"}}
	var out bytes.Buffer
	if code := runTaskCommand([]string{"status", "--target", "route", "--id", "42", "--follow"}, servers, nil, &out); code != 0 {
		t.Fatalf("task status --follow: code=%d out=%s", code, out.String())
	}
	if strings.Count(out.String(), "line ") != 2 || !strings.HasSuffix(out.String(), "task 42: success\n") {
		t.Fatalf("follow printed duplicate or missing output: %q", out.String())
	}
}

func TestDetachedFollowUpTruthfulBeforeAuditNotification(t *testing.T) {
	capture := func(auditID int64) string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		old := os.Stderr
		os.Stderr = w
		defer func() { os.Stderr = old }()
		c := &MCPClient{SessionName: "s1"}
		c.detachedFollowUp("prod/node", auditID)
		w.Close()
		out, _ := io.ReadAll(r)
		return string(out)
	}

	unknown := capture(0)
	if !strings.Contains(unknown, "may not have started") || !strings.Contains(unknown, "doops -session s1 task list --target prod/node") {
		t.Fatalf("unconfirmed detach output: %q", unknown)
	}
	if strings.Contains(unknown, "task status") || strings.Contains(unknown, "task stop") || strings.Contains(unknown, "<audit-id>") {
		t.Fatalf("unconfirmed detach leaked placeholder commands: %q", unknown)
	}
	confirmed := capture(42)
	for _, want := range []string{"the task keeps running on prod/node.", "--id 42"} {
		if !strings.Contains(confirmed, want) {
			t.Fatalf("confirmed detach output missing %q: %q", want, confirmed)
		}
	}
}
