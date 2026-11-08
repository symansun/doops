package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRawCommandOutputAfterWait(t *testing.T) {
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), "printf 'STDOUT_CANARY\\n'; printf 'STDERR_CANARY\\n' >&2")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	defer stderr.Close()
	// Delay both readers until the leader is reaped, not merely scheduled to exit.
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []struct {
		name string
		pipe io.Reader
		want string
	}{
		{"stdout", stdout, "STDOUT_CANARY\n"},
		{"stderr", stderr, "STDERR_CANARY\n"},
	} {
		got, err := io.ReadAll(stream.pipe)
		if err != nil || string(got) != stream.want {
			t.Errorf("%s after Wait = %q, %v; want %q", stream.name, got, err, stream.want)
		}
	}
}

func TestRawCommandOutputFastCommand(t *testing.T) {
	for i := 0; i < 100; i++ {
		command := "printf 'OUT_1\\nOUT_2\\n'"
		want := []string{"OUT_1\n", "OUT_2\n"}
		if i%2 == 0 {
			command += "; printf 'ERR_1\\nERR_2\\n' >&2"
			want = append(want, "ERR_1\n", "ERR_2\n")
		}
		cmd, stdout, stderr, err := executeRawCommand(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var output strings.Builder
		err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(event notificationEvent) {
			mu.Lock()
			defer mu.Unlock()
			output.WriteString(event.Data)
		}, false)
		got := output.String()
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		for _, line := range want {
			if strings.Count(got, line) != 1 {
				t.Fatalf("iteration %d: output = %q, want exactly one %q", i, got, line)
			}
		}
	}
}

func TestRawCommandOutputFastRPC(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	_, conn, messages := cancellationTestAgent(t)
	for id := 1; id <= 25; id++ {
		want := fmt.Sprintf("FAST_RPC_%d\n", id)
		if err := conn.WriteJSON(map[string]interface{}{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]interface{}{
				"name": "doops_shell", "arguments": map[string]interface{}{"command": fmt.Sprintf("printf 'FAST_RPC_%d\\n'", id)},
			},
		}); err != nil {
			t.Fatal(err)
		}
		var output strings.Builder
		final := awaitCancellationTestMessage(t, messages, func(message map[string]interface{}) bool {
			if message["method"] == "notifications/message" {
				params := message["params"].(map[string]interface{})
				output.WriteString(fmt.Sprint(params["data"]))
			}
			return message["id"] == float64(id)
		})
		if final["error"] != nil || final["result"].(map[string]interface{})["isError"] == true || output.String() != want {
			t.Fatalf("RPC %d: output = %q, want %q; terminal = %#v", id, output.String(), want, final)
		}
	}
}

func TestRawCommandOutputLargeStreams(t *testing.T) {
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), "for ((i=0; i<12000; i++)); do printf 'OUT_LINE\\n'; printf 'ERR_LINE\\n' >&2; done")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	counts := make(map[string]int)
	err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(event notificationEvent) {
		mu.Lock()
		defer mu.Unlock()
		counts[event.Data]++
	}, false)
	if err != nil || len(counts) != 2 || counts["OUT_LINE\n"] != 12000 || counts["ERR_LINE\n"] != 12000 {
		t.Fatalf("large streams incomplete: counts=%v err=%v", counts, err)
	}
}

type rawCommandOutputErrorPipe struct {
	io.ReadCloser
}

func (rawCommandOutputErrorPipe) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestRawCommandOutputReadFailureIsUnknown(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			cmd, stdout, stderr, err := executeRawCommand(context.Background(), "exit 0")
			if err != nil {
				t.Fatal(err)
			}
			if stream == "stdout" {
				stdout = rawCommandOutputErrorPipe{stdout}
			} else {
				stderr = rawCommandOutputErrorPipe{stderr}
			}
			err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(notificationEvent) {}, false)
			if !errors.Is(err, errExecutionOutcomeUnknown) || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("read failure became success or lost its cause: %v", err)
			}
		})
	}
}

func TestRawCommandOutputSignal(t *testing.T) {
	cmd, stdout, stderr, err := executeRawCommand(context.Background(), "printf 'SIGNAL_OUT\\n'; printf 'SIGNAL_ERR\\n' >&2; kill -TERM $$")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var output strings.Builder
	err = waitCommandStreams(context.Background(), cmd, stdout, stderr, func(event notificationEvent) {
		mu.Lock()
		defer mu.Unlock()
		output.WriteString(event.Data)
	}, false)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("signal exit was not preserved: %v", err)
	}
	if strings.Count(output.String(), "SIGNAL_OUT\n") != 1 || strings.Count(output.String(), "SIGNAL_ERR\n") != 1 {
		t.Fatalf("signal exit lost output: %q", output.String())
	}
}

func TestRawCommandOutputEscapedWriterHelper(t *testing.T) {
	if os.Getenv("DOOPS_RAW_OUTPUT_HELPER") != "1" {
		return
	}
	child := exec.Command("sleep", "60")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("ESCAPED_PID=%d\n", child.Process.Pid)
	os.Exit(0)
}

func TestRawCommandOutputEscapedWriterIsBounded(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			helper := exec.Command(os.Args[0], "-test.run=^TestRawCommandOutputEscapedWriterHelper$")
			helper.Env = append(os.Environ(), "DOOPS_RAW_OUTPUT_HELPER=1")
			cmd, stdout, stderr, err := startRawCommand(ctx, helper)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cancel()
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				_ = stdout.Close()
				_ = stderr.Close()
			})
			pidReady := make(chan int, 1)
			done := make(chan error, 1)
			started := time.Now()
			go func() {
				done <- waitCommandStreams(ctx, cmd, stdout, stderr, func(event notificationEvent) {
					var pid int
					if _, err := fmt.Sscanf(event.Data, "ESCAPED_PID=%d", &pid); err == nil {
						pidReady <- pid
					}
				}, false)
			}()
			select {
			case pid := <-pidReady:
				t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				group, err := syscall.Getpgid(pid)
				if err != nil || group == cmd.Process.Pid {
					t.Fatalf("writer did not escape leader PGID: group=%d err=%v", group, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("escaped writer did not report its PID")
			}
			if canceled {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, errExecutionOutcomeUnknown) || time.Since(started) < commandPipeGracePeriod {
					t.Fatalf("escaped writer did not reach bounded unknown: err=%v elapsed=%s", err, time.Since(started))
				}
			case <-time.After(4 * time.Second):
				t.Fatal("escaped writer blocked drain beyond its grace period")
			}
		})
	}
}
