package server

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

// A command larger than one exec argument (Linux MAX_ARG_STRLEN, 128 KiB) must
// run unchanged instead of failing with "argument list too long".
func TestExecuteRawCommandRunsCommandsOfAnySize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload int
	}{
		{"short inline", 10},
		{"at inline limit", inlineCommandLimit - 64},
		{"over one argv string", 200 << 10},
		{"one megabyte", 1 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.Repeat("a", tc.payload)
			command := "printf '%s' '" + payload + "' | wc -c"
			cmd, stdout, stderr, err := executeRawCommand(context.Background(), command)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			out, _ := io.ReadAll(stdout)
			errOut, _ := io.ReadAll(stderr)
			if err := cmd.Wait(); err != nil {
				t.Fatalf("wait: %v stderr=%s", err, errOut)
			}
			if got := strings.TrimSpace(string(out)); got != strconv.Itoa(tc.payload) {
				t.Fatalf("output = %q, want %d (stderr=%s)", got, tc.payload, errOut)
			}
			if cmd.ExtraFiles != nil && len(cmd.Args) > 0 && strings.Contains(strings.Join(cmd.Args, " "), payload) {
				t.Fatal("long command must not be passed as an argument")
			}
		})
	}
}

func TestBashCommandLeavesNoScriptFileBehind(t *testing.T) {
	cmd, script, err := bashCommand(strings.Repeat("#", inlineCommandLimit+1) + "\ntrue")
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()
	if cmd.Args[1] != "/dev/fd/3" {
		t.Fatalf("long command args = %v", cmd.Args)
	}
	if _, err := script.Stat(); err != nil {
		t.Fatal(err)
	}
	if name := script.Name(); name != "" {
		if _, err := os.Stat(name); err == nil {
			t.Fatalf("script file %s must be unlinked before the command runs", name)
		}
	}
}
