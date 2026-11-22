package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/user/doops/agent/internal/dispatcher"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// -----------------------------------------------------------------------------
// 共享的底层基础设施流式处理器
// -----------------------------------------------------------------------------

// executeRawCommand 使用纯标准的 os/exec 启动进程（去 PTY 化），返回 cmd 对象、标准输出管、错误输出管
func executeRawCommand(ctx context.Context, command string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	cmd, script, err := bashCommand(command)
	if err != nil {
		return nil, nil, nil, err
	}
	if script != nil {
		// The child holds its own copy of the descriptor after Start.
		defer script.Close()
	}
	return startRawCommand(ctx, cmd)
}

// inlineCommandLimit keeps `bash -c` for ordinary commands. Linux caps one
// argv entry at 128 KiB (MAX_ARG_STRLEN), so a longer command is written to an
// unlinked private file and run as `bash /dev/fd/3`; callers see no difference
// and nothing is left on disk.
const inlineCommandLimit = 64 << 10

func bashCommand(command string) (*exec.Cmd, *os.File, error) {
	cmd, err := dispatcher.ShellCommand(command, false)
	if err != nil {
		return nil, nil, err
	}
	if len(command) <= inlineCommandLimit {
		return cmd, nil, nil
	}
	file, err := os.CreateTemp("", "doops-cmd-*.sh")
	if err != nil {
		return nil, nil, fmt.Errorf("prepare long command: %w", err)
	}
	_ = os.Remove(file.Name())
	if _, err := file.WriteString(command); err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("prepare long command: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("prepare long command: %w", err)
	}
	cmd.Args = []string{cmd.Args[0], "/dev/fd/3"}
	cmd.ExtraFiles = []*os.File{file}
	return cmd, file, nil
}

func startRawCommand(ctx context.Context, cmd *exec.Cmd) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = commandPipeGracePeriod

	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, nil, nil, errors.New("command output already set")
	}
	stdoutPipe, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	defer stdoutWriter.Close()
	stderrPipe, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutPipe.Close()
		return nil, nil, nil, err
	}
	defer stderrWriter.Close()
	// Wait must not own the read ends; fast commands can exit before readers run.
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return nil, nil, nil, err
	}
	return cmd, stdoutPipe, stderrPipe, nil
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func sshExitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	code := exitErr.ExitCode()
	return code, code >= 0 && code <= 255
}
