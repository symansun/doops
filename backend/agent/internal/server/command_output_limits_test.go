package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// withShortProcessGracePeriods shrinks the production 1s/10s/2s grace periods
// and restores them synchronously before the test returns, so a stray background
// goroutine from one test cannot observe a later test's values.
func withShortProcessGracePeriods(t *testing.T) {
	t.Helper()
	previousDrain, previousResidual := commandPipeGracePeriod, postLeaderDescendantGracePeriod
	previousKill, previousCleanup := processGroupKillGracePeriod, postLeaderCleanupGracePeriod
	commandPipeGracePeriod, postLeaderDescendantGracePeriod = 200*time.Millisecond, 300*time.Millisecond
	processGroupKillGracePeriod, postLeaderCleanupGracePeriod = 100*time.Millisecond, time.Second
	t.Cleanup(func() {
		commandPipeGracePeriod, postLeaderDescendantGracePeriod = previousDrain, previousResidual
		processGroupKillGracePeriod, postLeaderCleanupGracePeriod = previousKill, previousCleanup
	})
}

func TestProcessGracePeriodDefaults(t *testing.T) {
	if commandPipeGracePeriod != time.Second {
		t.Fatalf("commandPipeGracePeriod = %s, want 1s (text check keeps the production default)", commandPipeGracePeriod)
	}
	if postLeaderDescendantGracePeriod != 10*time.Second {
		t.Fatalf("postLeaderDescendantGracePeriod = %s, want 10s", postLeaderDescendantGracePeriod)
	}
	if processGroupKillGracePeriod != 2*time.Second {
		t.Fatalf("processGroupKillGracePeriod = %s, want 2s", processGroupKillGracePeriod)
	}
	if postLeaderCleanupGracePeriod != 10*time.Second {
		t.Fatalf("postLeaderCleanupGracePeriod = %s, want 10s", postLeaderCleanupGracePeriod)
	}
}

// runShellThroughWS runs one doops_shell call through the real Agent WebSocket
// handler and returns the terminal response plus every forwarded notification.
func runShellThroughWS(t *testing.T, gatewayTimeoutMS int64, command string) (map[string]interface{}, string) {
	t.Helper()
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	gateway := NewGateway()
	server := newGatewayAuthorizedAgentServer(t, gateway)
	defer server.Close()
	conn := dialAgentTestWS(t, server.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	rawArgs, err := json.Marshal(map[string]interface{}{"session_id": "output-limits", "command": command})
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]interface{}{"name": "doops_shell", "arguments": json.RawMessage(rawArgs)}
	if gatewayTimeoutMS > 0 {
		params["timeout_ms"] = gatewayTimeoutMS
	}
	if err := conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": 77, "method": "tools/call", "params": params}); err != nil {
		t.Fatalf("send shell call: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var forwarded strings.Builder
	for {
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read shell response: %v (forwarded %q)", err, forwarded.String())
		}
		if params, _ := msg["params"].(map[string]interface{}); msg["method"] != nil {
			if data, _ := params["data"].(string); data != "" {
				forwarded.WriteString(data)
			}
			continue
		}
		return msg, forwarded.String()
	}
}

func TestAgentShellLargeOutputDoesNotKillTheCommand(t *testing.T) {
	const size = 3 << 20
	response, forwarded := runShellThroughWS(t, 0, fmt.Sprintf("head -c %d /dev/zero | tr '\\0' 'x'", size))
	if response["error"] != nil {
		t.Fatalf("3MiB single line was reported as an error: %#v", response)
	}
	result, _ := response["result"].(map[string]interface{})
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("3MiB single line was reported as a tool error: %#v", result)
	}
	if len(forwarded) < size {
		t.Fatalf("forwarded %d bytes, want at least %d", len(forwarded), size)
	}
	if strings.Count(forwarded, "x") < size {
		t.Fatalf("forwarded output lost the long line: %d bytes", len(forwarded))
	}
}

func TestStreamCommandOutputChunksSingleLongLine(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 3<<20)
	var forwarded bytes.Buffer
	if err := streamCommandOutput(bytes.NewReader(payload), func(event notificationEvent) {
		forwarded.WriteString(event.Data)
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forwarded.Bytes(), payload) {
		t.Fatalf("forwarded %d bytes, want the exact %d-byte line", forwarded.Len(), len(payload))
	}
}

func TestStreamCommandOutputSplitsOnlyOnRuneBoundaries(t *testing.T) {
	// 200k three-byte runes and no newline: the chunk boundary lands mid-rune
	// unless every notification is trimmed to a valid UTF-8 boundary.
	payload := []byte(strings.Repeat("世", 200_000))
	var notifications []string
	if err := streamCommandOutput(bytes.NewReader(payload), func(event notificationEvent) {
		notifications = append(notifications, event.Data)
	}); err != nil {
		t.Fatal(err)
	}
	if len(notifications) < 2 {
		t.Fatalf("expected several bounded notifications, got %d", len(notifications))
	}
	var joined strings.Builder
	for index, chunk := range notifications {
		if !utf8.ValidString(chunk) {
			t.Fatalf("notification %d is not valid UTF-8 (%d bytes)", index, len(chunk))
		}
		if len(chunk) > commandOutputChunkBytes+utf8.UTFMax {
			t.Fatalf("notification %d is %d bytes, want at most ~%d", index, len(chunk), commandOutputChunkBytes)
		}
		joined.WriteString(chunk)
	}
	if joined.String() != string(payload) {
		t.Fatalf("concatenated notifications differ from the output")
	}
}

func TestStreamCommandOutputPassesThroughInvalidUTF8(t *testing.T) {
	payload := append([]byte{0xff, 0xfe, 'a'}, bytes.Repeat([]byte{0x80}, 200_000)...)
	var joined bytes.Buffer
	if err := streamCommandOutput(bytes.NewReader(payload), func(event notificationEvent) {
		joined.WriteString(event.Data)
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(joined.Bytes(), payload) {
		t.Fatalf("invalid bytes were altered: got %d bytes, want %d", joined.Len(), len(payload))
	}
}

func TestStreamCommandOutputKeepsUnterminatedTail(t *testing.T) {
	var joined strings.Builder
	if err := streamCommandOutput(strings.NewReader("no trailing newline"), func(event notificationEvent) {
		joined.WriteString(event.Data)
	}); err != nil {
		t.Fatal(err)
	}
	if joined.String() != "no trailing newline" {
		t.Fatalf("tail = %q, want the exact unterminated output", joined.String())
	}
}

func TestStreamCommandOutputDoesNotAppendNewline(t *testing.T) {
	var joined strings.Builder
	if err := streamCommandOutput(strings.NewReader("one\ntwo"), func(event notificationEvent) {
		joined.WriteString(event.Data)
	}); err != nil {
		t.Fatal(err)
	}
	if joined.String() != "one\ntwo" {
		t.Fatalf("output = %q, want the exact bytes without an added newline", joined.String())
	}
}

func TestStreamCommandOutputReportsGenuineReadError(t *testing.T) {
	readErr := errors.New("synthetic read failure")
	err := streamCommandOutput(io.MultiReader(strings.NewReader("partial"), errorReader{err: readErr}), func(notificationEvent) {})
	if !errors.Is(err, readErr) {
		t.Fatalf("read error = %v, want %v", err, readErr)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

// groupHasLiveMember reports whether any non-zombie process still belongs to pgid.
// ps may be unavailable in a sandbox; that is a skip, not a pass.
func groupHasLiveMember(t *testing.T, pgid int) bool {
	t.Helper()
	processes, err := exec.Command("ps", "-e", "-o", "pid=,pgid=,state=").CombinedOutput()
	if err != nil {
		t.Skipf("ps is unavailable: %v (%s)", err, processes)
	}
	for _, line := range strings.Split(string(processes), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == fmt.Sprint(pgid) && !strings.Contains(fields[2], "Z") {
			return true
		}
	}
	return false
}

func TestWaitCommandStreamsDetachedChildWithRedirectedStdioReturnsNil(t *testing.T) {
	withShortProcessGracePeriods(t)
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skipf("setsid is unavailable: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	command := fmt.Sprintf("setsid sleep 30 </dev/null >%s 2>&1 & echo started", logPath)
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("pkill", "-f", "sleep 30").Run()
	}()
	started := time.Now()
	err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(notificationEvent) {}, false)
	if err != nil {
		t.Fatalf("detached child with redirected stdio = %v, want nil", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("detached child delayed the result by %s", elapsed)
	}
}

func TestWaitCommandStreamsSameGroupChildIsResidualTermination(t *testing.T) {
	withShortProcessGracePeriods(t)
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), "sleep 30 & echo spawned; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	defer syscall.Kill(-pgid, syscall.SIGKILL)

	// waitCommandStreams uses this sender for both streams, and the drain notice
	// is delivered through it too.
	var noticesMu sync.Mutex
	var notices []string
	err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(event notificationEvent) {
		if event.Data == commandOutputDrainNotice {
			noticesMu.Lock()
			notices = append(notices, event.Data)
			noticesMu.Unlock()
		}
	}, false)
	var residual *residualProcessesTerminated
	if !errors.As(err, &residual) {
		t.Fatalf("same-group child error = %v, want residualProcessesTerminated", err)
	}
	if errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("same-group child must not be an unknown outcome: %v", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(residual.leaderErr, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("leader exit = %v, want exit code 3", residual.leaderErr)
	}
	message := residual.Error()
	if !strings.Contains(message, "exit code 3") || !strings.Contains(message, "setsid") || !strings.Contains(message, "systemd-run") {
		t.Fatalf("residual message lacks the exit code or the detached-start guidance: %s", message)
	}
	// The residual keeps only the leader status for audit.
	if exit, ok := sshExitCode(currentCommandExit(err)); !ok || exit != 3 {
		t.Fatalf("audit exit = %d (%v), want 3", exit, ok)
	}
	if len(notices) != 1 {
		t.Fatalf("drain notices = %d, want one notice that later output is not collected", len(notices))
	}
	deadline := time.Now().Add(3 * time.Second)
	for groupHasLiveMember(t, pgid) {
		if time.Now().After(deadline) {
			t.Fatalf("same-group background process survived cleanup")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWaitCommandStreamsCanceledContextIsUnchanged(t *testing.T) {
	withShortProcessGracePeriods(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd, stdout, stderr, err := executeRawCommand(ctx, "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err = waitCommandStreams(ctx, cmd, stdout, stderr, func(notificationEvent) {}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command error = %v, want context.Canceled", err)
	}
	if errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("cancellation must not be an unknown outcome: %v", err)
	}
}

func TestWaitCommandOutputGenuineReadErrorStaysUnknown(t *testing.T) {
	withShortProcessGracePeriods(t)
	_, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("true")
	cmd.Stdout = io.Discard
	cmd.Stderr = stderrWrite
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stderrWrite.Close()
	// A real read failure (not our own drain close) leaves the outcome unproven.
	err = waitCommandOutput(context.Background(), cmd, io.NopCloser(strings.NewReader("")),
		io.NopCloser(errorReader{err: errors.New("synthetic read failure")}),
		func(notificationEvent) {}, func(notificationEvent) {}, false)
	if !errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("genuine read error = %v, want execution_outcome_unknown", err)
	}
	if !strings.Contains(err.Error(), "command output read") {
		t.Fatalf("read error lost its reason: %v", err)
	}
}
