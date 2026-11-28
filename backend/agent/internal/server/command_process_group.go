package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"syscall"
	"time"
)

// Grace periods are variables so tests can shrink them; production keeps 1s,
// 10s and 2s. Cancellation escalation is joined in waitProcessGroup, never left
// running after a terminal RPC response.
var commandPipeGracePeriod = time.Second
var postLeaderDescendantGracePeriod = 10 * time.Second
var processGroupKillGracePeriod = 2 * time.Second

// postLeaderCleanupGracePeriod bounds how long a group that was told to stop
// may take to disappear before the outcome is unknown.
var postLeaderCleanupGracePeriod = 10 * time.Second

// groupProbable reports a group that still has members. A denied probe counts
// as members, so cleanup still makes progress.
func groupProbable(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// pipeHolder tracks a bounded group wait whose leader exited but whose group
// did not. It is set when the group was cleaned up after the leader exited but after the leader exited but
// its output pipes were still held by processes that left the group. Such a
// holder can never be terminated through the group, so waiting for it would
// only delay the response.
type pipeHolder struct {
	announced    bool
	leaderExited bool
	signalled    bool
	// notify ends the output drain once a holder that escaped the group is
	// known; the remaining readers never see EOF on their own.
	notify func()
}

func waitProcessGroup(ctx context.Context, pid int, commandDone <-chan error) error {
	return waitProcessGroupWithHolder(ctx, pid, commandDone, nil)
}

func waitProcessGroupWithHolder(ctx context.Context, pid int, commandDone <-chan error, holder *pipeHolder) error {
	leaderExited := false
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	ctxDone := ctx.Done()
	var killTimer, cleanupTimer, postLeaderTimer *time.Timer
	var killDeadline, cleanupDeadline, postLeaderDeadline <-chan time.Time
	forcedCleanup := false
	defer func() {
		if killTimer != nil {
			killTimer.Stop()
		}
		if cleanupTimer != nil {
			cleanupTimer.Stop()
		}
		if postLeaderTimer != nil {
			postLeaderTimer.Stop()
		}
	}()
	var commandErr error
	for {
		if commandDone == nil {
			// Only reap adopted descendants after exec.Cmd.Wait owns the leader's status.
			for {
				var status syscall.WaitStatus
				reaped, _ := syscall.Wait4(-pid, &status, syscall.WNOHANG, nil)
				if reaped <= 0 {
					break
				}
			}
			err := syscall.Kill(-pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if forcedCleanup {
					return &residualProcessesTerminated{leaderErr: commandErr}
				}
				return commandErr
			}
			if err != nil {
				terminated := false
				if forcedCleanup {
					// Same-group processes we told to stop are survivors while
					// the probe is denied; keep waiting for the cleanup deadline
					// instead of inventing a failure. A denied probe must not
					// turn a known leader exit into an unknown outcome.
					log.Printf("process group %d status is unavailable after cleanup started: %v", pid, err)
					terminated = true
				} else if errors.Is(err, syscall.EPERM) {
					// Some kernels report EPERM for a signal-0 probe even for
					// our own group (seen on macOS after the leader is reaped).
					// Treat the group as present and let the cleanup deadline
					// decide; do not invent an unknown outcome from the probe.
					terminated = true
				}
				if !terminated {
					return fmt.Errorf("%w: process group status: %v", errExecutionOutcomeUnknown, err)
				}
			}
		}
		select {
		case commandErr = <-commandDone:
			commandDone = nil
			leaderExited = true
			if holder != nil {
				holder.leaderExited = true
			}
			if ctx.Err() == nil {
				postLeaderTimer = time.NewTimer(postLeaderDescendantGracePeriod)
				postLeaderDeadline = postLeaderTimer.C
			}
		case <-ctxDone:
			ctxDone = nil
			if postLeaderTimer != nil {
				postLeaderTimer.Stop()
				postLeaderDeadline = nil
			}
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			killTimer = time.NewTimer(processGroupKillGracePeriod)
			cleanupTimer = time.NewTimer(postLeaderCleanupGracePeriod)
			killDeadline = killTimer.C
			cleanupDeadline = cleanupTimer.C
		case <-killDeadline:
			killDeadline = nil
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case <-cleanupDeadline:
			return fmt.Errorf("%w: process group did not terminate", errExecutionOutcomeUnknown)
		case <-postLeaderDeadline:
			postLeaderDeadline = nil
			forcedCleanup = true
			if leaderExited && holder != nil {
				holder.announced = true
				if holder.notify != nil {
					holder.notify()
				}
			}
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			killTimer = time.NewTimer(processGroupKillGracePeriod)
			cleanupTimer = time.NewTimer(postLeaderCleanupGracePeriod)
			killDeadline = killTimer.C
			cleanupDeadline = cleanupTimer.C
		case <-ticker.C:
			// Keep the ticker for the loop's periodic probe; same-group
			// descendants are stopped by the post-leader grace period, which
			// marks the result as a known residual termination.
		}
	}
}

// residualProcessesTerminated reports a leader that exited on its own while
// same-process-group background processes kept the group alive. They were
// terminated after postLeaderDescendantGracePeriod; this is a known local
// outcome, not errExecutionOutcomeUnknown.
type residualProcessesTerminated struct {
	leaderErr error
}

func (e *residualProcessesTerminated) Error() string {
	code := 0
	var exitErr *exec.ExitError
	if e != nil && e.leaderErr != nil {
		code = -1
		if errors.As(e.leaderErr, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return fmt.Sprintf("command exited with exit code %d, but background processes in the same process group "+
		"kept it running; doops terminated them after %s (SIGTERM, then SIGKILL). "+
		"Start long-running processes detached so doops does not own or clean them up: "+
		"systemd-run --unit <name> <command>, a container, or `setsid <command> </dev/null >/path/to/log 2>&1 &` "+
		"with its standard output and error redirected to a file",
		code, postLeaderDescendantGracePeriod)
}

// Unwrap exposes the leader's exit status to exit-code helpers.
func (e *residualProcessesTerminated) Unwrap() error {
	return e.leaderErr
}

func residualProcessesTerminatedFrom(err error) *residualProcessesTerminated {
	var residual *residualProcessesTerminated
	if errors.As(err, &residual) {
		return residual
	}
	return nil
}
