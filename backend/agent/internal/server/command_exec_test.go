package server

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestWaitProcessGroupBoundsDetachedDescendants(t *testing.T) {
	withShortProcessGracePeriods(t)
	cmd := exec.Command("bash", "-c", "sleep 60 & exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	commandDone := make(chan error, 1)
	go func() { commandDone <- cmd.Wait() }()

	started := time.Now()
	err := waitProcessGroup(context.Background(), cmd.Process.Pid, commandDone)
	if !isResidualProcessesTerminated(err) || errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("same-group descendants were not reported as a known residual termination: %v", err)
	}
	if elapsed := time.Since(started); elapsed < postLeaderDescendantGracePeriod || elapsed > postLeaderDescendantGracePeriod+3*time.Second {
		t.Fatalf("detached descendant wait was not bounded: %s", elapsed)
	}
	if groupHasLiveMember(t, cmd.Process.Pid) {
		t.Fatalf("detached process group survived cleanup")
	}
}

func TestWaitCommandStreamsPreservesExitCode(t *testing.T) {
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), "exit 42")
	if err != nil {
		t.Fatal(err)
	}
	err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(notificationEvent) {}, false)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("waitCommandStreams error = %v, want exit 42", err)
	}
}

func TestWaitCommandStreamsBoundsDescendantHoldingOutputPipe(t *testing.T) {
	withShortProcessGracePeriods(t)
	// The background child belongs to the same group: it is cleaned up after
	// the grace period, but a grandchild that left the group keeps the pipe
	// open, so a reader is still pending when the drain grace period ends.
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), "sleep 60 & sh -c '(sleep 60 >&1 &) ; exit 0'")
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)

	var notices []string
	started := time.Now()
	// waitCommandStreams uses this sender for both streams, and the drain notice
	// is delivered through it too.
	var noticesMu sync.Mutex
	err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(event notificationEvent) {
		noticesMu.Lock()
		defer noticesMu.Unlock()
		if event.Data == commandOutputDrainNotice {
			notices = append(notices, event.Data)
		}
	}, false)
	if !isResidualProcessesTerminated(err) || errors.Is(err, errExecutionOutcomeUnknown) {
		t.Fatalf("descendant holding output pipe was not reported as a known residual termination: %v", err)
	}
	if len(notices) != 1 || notices[0] != commandOutputDrainNotice {
		t.Fatalf("drain notice = %q, want exactly %q", notices, commandOutputDrainNotice)
	}
	// The group is cleaned up after the post-leader grace; the escaped holder
	// is announced at once, so the RPC never waits for the drain grace period.
	grace := postLeaderDescendantGracePeriod
	if elapsed := time.Since(started); elapsed < grace || elapsed > grace+3*time.Second {
		t.Fatalf("descendant output pipe wait was not bounded: %s", elapsed)
	}
}
