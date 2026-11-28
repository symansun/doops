package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"
)

// waitCommandStreams owns the read ends independently of Cmd.Wait. Bound the
// final drain because writers can escape the process-group watchdog.
func waitCommandStreams(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.ReadCloser, push notificationSender, heartbeat bool) error {
	return waitCommandOutput(ctx, cmd, stdout, stderr, push, push, heartbeat)
}

// commandOutputChunkBytes bounds every output notification; output lines have
// no length limit and are split only at UTF-8 rune boundaries.
const commandOutputChunkBytes = 64 << 10

// commandOutputDrainNotice is emitted once when a process that is not the
// command leader still holds an output pipe after the leader exited.
const commandOutputDrainNotice = "[doops] command output is incomplete: the command exited, but a process still holds its output pipe (for example a background process in the same process group), so later output is not collected. " +
	"To start a daemon, redirect its standard output and error, for example " +
	"`setsid <command> </dev/null >/path/to/log 2>&1 &`, systemd-run --unit <name> <command>, or a container."

func waitCommandOutput(ctx context.Context, cmd *exec.Cmd, stdout, stderr io.ReadCloser, push, pushError notificationSender, heartbeat bool) error {
	streamDone := make(chan error, 2)
	readStream := func(pipe io.ReadCloser, send notificationSender) {
		readErr := streamCommandOutput(pipe, send)
		if errors.Is(readErr, os.ErrClosed) {
			// Closing our own read end is not a command failure.
			readErr = nil
		}
		_ = pipe.Close()
		streamDone <- readErr
	}
	go readStream(stdout, push)
	go readStream(stderr, pushError)

	var done, heartbeatDone chan struct{}
	if heartbeat {
		done = make(chan struct{})
		heartbeatDone = make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			slowProgressHeartbeat(done, push)
		}()
	}
	commandDone := make(chan error, 1)
	go func() { commandDone <- cmd.Wait() }()
	holder := &pipeHolder{}
	released := make(chan struct{})
	var releaseOnce sync.Once
	holder.notify = func() {
		releaseOnce.Do(func() {
			_ = stdout.Close()
			_ = stderr.Close()
			close(released)
		})
	}
	cmdErr := waitProcessGroupWithHolder(ctx, cmd.Process.Pid, commandDone, holder)
	if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
		_ = stdout.Close()
		_ = stderr.Close()
	}
	// A known leader exit status (success, a normal exit code, cancellation, or
	// residual same-group processes) is evidence the command itself finished or
	// was stopped on request, so it is never downgraded to an unknown outcome.
	// Only a read error or an unkillable group before the leader exit is unknown.
	leaderKnown := !errors.Is(cmdErr, errExecutionOutcomeUnknown)
	drainTimer := time.NewTimer(commandPipeGracePeriod)
	defer drainTimer.Stop()
	drainDeadline := drainTimer.C
	var readErr error
	pending := 2
	// drainEnded reports whether the pipes had to be closed, i.e. a process that
	// is not the command leader (same group or escaped) still held them.
	drainEnded := false
	emitDrainNotices := func() {
		if holder.announced && cmdErr != nil {
			push(errorNotification(cmdErr.Error()))
		}
		push(errorNotification(commandOutputDrainNotice))
	}
	if holder.announced && leaderKnown {
		// Cleanup already signalled the group, so the holder left the group and
		// would never release its pipe on its own. Close now; later output cannot
		// be collected.
		drainEnded = true
		go func(pending int) {
			for ; pending > 0; pending-- {
				<-streamDone
			}
		}(pending)
		pending = 0
		emitDrainNotices()
	}
drain:
	for pending > 0 {
		select {
		case err := <-streamDone:
			pending--
			if err != nil && readErr == nil {
				readErr = err
			}
		case <-released:
			// The holder-notify path above already closed the pipes.
			break drain
		case <-drainDeadline:
			drainDeadline = nil
			drainEnded = true
			_ = stdout.Close()
			_ = stderr.Close()
			if leaderKnown {
				go func(pending int) {
					for ; pending > 0; pending-- {
						<-streamDone
					}
				}(pending)
				emitDrainNotices()
			} else {
				cmdErr = fmt.Errorf("%w: command output drain timed out", errExecutionOutcomeUnknown)
			}
			break drain
		}
	}
	if readErr != nil {
		// A real read failure before the drain closed the pipes leaves the output
		// outcome unproven. Residual same-group cleanup alone does not: with no
		// read error the known leader exit stands, and a canceled parent keeps
		// context.Canceled because the caller already owns the cancel semantics.
		// Once the drain closed the pipes, later read errors say nothing about
		// the command; an escaped holder is then reported as unknown below.
		var residual *residualProcessesTerminated
		switch {
		case drainEnded:
			if errors.As(cmdErr, &residual) {
				cmdErr = residual
			}
		case errors.As(cmdErr, &residual), errors.Is(cmdErr, context.Canceled):
			// Known outcome: the leader exit status (the residual) or the caller's
			// cancellation already explains this result.
		case errors.Is(cmdErr, errExecutionOutcomeUnknown):
			cmdErr = fmt.Errorf("%w: command output read: %w", errExecutionOutcomeUnknown, readErr)
		default:
			cause := readErr
			if cmdErr != nil {
				cause = errors.Join(cmdErr, readErr)
			}
			cmdErr = fmt.Errorf("%w: command output read: %w", errExecutionOutcomeUnknown, cause)
		}
	}
	// A pipe held after a known leader exit only means later output is
	// missing, not that the command outcome is unknown. The drain notice above
	// says so explicitly; the known exit status (or residual cleanup) stands.
	if done != nil {
		close(done)
		<-heartbeatDone
	}
	return cmdErr
}

// streamCommandOutput forwards output in bounded chunks as it arrives. There is
// no per-line limit: a line longer than the buffer is forwarded piece by piece
// instead of failing the read. A chunk never ends inside a multi-byte UTF-8
// sequence. Bytes that are not valid UTF-8 are passed through unchanged, so the
// concatenated notifications reproduce the command output exactly.
func streamCommandOutput(pipe io.Reader, send notificationSender) error {
	reader := bufio.NewReaderSize(pipe, commandOutputChunkBytes)
	pending := make([]byte, 0, commandOutputChunkBytes)
	for {
		chunk, err := reader.ReadSlice('\n')
		pending = append(pending, chunk...)
		complete := 0
		if err == bufio.ErrBufferFull {
			// The buffer is full: the only reason to hold bytes back is a rune
			// split exactly at the end of the buffer.
			complete = utf8ChunkBoundary(pending)
		} else {
			// The line, or the stream, ends here, so nothing can be split.
			complete = len(pending)
		}
		if complete > 0 {
			send(rawNotification(string(pending[:complete])))
			pending = append(pending[:0], pending[complete:]...)
		}
		switch err {
		case nil, bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(pending) > 0 {
				send(rawNotification(string(pending)))
			}
			return nil
		default:
			return err
		}
	}
}

// utf8ChunkBoundary returns how many leading bytes of data can be forwarded
// without splitting a multi-byte UTF-8 sequence. A trailing invalid or
// incomplete sequence is not held back: malformed input must still stream.
func utf8ChunkBoundary(data []byte) int {
	if utf8.Valid(data) {
		return len(data)
	}
	// Scan back to the last rune start in the final UTFMax bytes and keep the
	// sequence only when it is complete; otherwise it crosses the boundary.
	start := len(data)
	if start > utf8.UTFMax {
		start -= utf8.UTFMax
	}
	for index := len(data) - 1; index >= start; index-- {
		if !utf8.RuneStart(data[index]) {
			continue
		}
		sequence := data[index:]
		if runeValue, size := utf8.DecodeRune(sequence); runeValue != utf8.RuneError && size > 1 {
			return len(data)
		}
		return index
	}
	return len(data)
}
