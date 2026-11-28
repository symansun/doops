package server

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentChildProcessesScrubSensitiveEnv(t *testing.T) {
	const (
		enrollCanary = "synthetic-enroll-canary-778899"
		mutCanary    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	t.Setenv("DOOPS_AGENT_ENROLL_TOKEN", enrollCanary)
	t.Setenv("DO_AGENT_MUTATION_AUTHORITY_SECRET", mutCanary)

	// 1. executeRawCommand (doops_shell / doops_bg path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, stdout, stderr, err := executeRawCommand(ctx, `printf 'E=[%s] M=[%s]\n' "${DOOPS_AGENT_ENROLL_TOKEN:-}" "${DO_AGENT_MUTATION_AUTHORITY_SECRET:-}"`)
	if err != nil {
		t.Fatalf("executeRawCommand: %v", err)
	}
	outBytes, _ := io.ReadAll(stdout)
	_, _ = io.ReadAll(stderr)
	_ = stdout.Close()
	_ = stderr.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("raw command wait: %v", err)
	}
	outText := string(outBytes)
	if strings.Contains(outText, enrollCanary) || strings.Contains(outText, mutCanary) {
		t.Fatalf("executeRawCommand leaked sensitive env: %q", outText)
	}
	if strings.TrimSpace(outText) != "E=[] M=[]" {
		t.Fatalf("executeRawCommand env output = %q, want E=[] M=[]", outText)
	}

	// 2. terminalManager.open (interactive PTY terminal path)
	tm := newTerminalManager()
	t.Cleanup(tm.closeAll)
	sess, err := tm.open("env-scrub-sess", 120, 32)
	if err != nil {
		t.Fatalf("terminalManager.open: %v", err)
	}
	t.Cleanup(func() { _ = tm.close("env-scrub-sess", sess.id) })
	if err := sess.write([]byte("printf 'TERM_E=[%s] TERM_M=[%s] DONE_SCRUB_%s\\n' \"${DOOPS_AGENT_ENROLL_TOKEN:-}\" \"${DO_AGENT_MUTATION_AUTHORITY_SECRET:-}\" \"$((40+2))\"\n")); err != nil {
		t.Fatalf("terminal write: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var termOut string
	for time.Now().Before(deadline) {
		chunk, _, _, _, _ := sess.readFrom(0)
		termOut = stripTerminalANSI(string(chunk))
		if strings.Contains(termOut, "DONE_SCRUB_42") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(termOut, enrollCanary) || strings.Contains(termOut, mutCanary) {
		t.Fatalf("terminalSession leaked sensitive env: %q", termOut)
	}
	if !strings.Contains(termOut, "TERM_E=[] TERM_M=[] DONE_SCRUB_42") {
		t.Fatalf("terminalSession did not confirm empty sensitive env, got: %q", termOut)
	}

	// 3. probeStdioMCP (both empty entry.Env and non-empty entry.Env)
	tmpDir := t.TempDir()
	probeScript := filepath.Join(tmpDir, "env_check_mcp.sh")
	body := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"env-check","version":"%s|%s|%s"}}}\n' \
        "${DOOPS_AGENT_ENROLL_TOKEN:-}" "${DO_AGENT_MUTATION_AUTHORITY_SECRET:-}" "${MCP_EXPLICIT_VAR:-}"
      ;;
    *'"method":"tools/list"'*)
      printf '{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}\n'
      ;;
  esac
done
`
	if err := os.WriteFile(probeScript, []byte(body), 0755); err != nil {
		t.Fatalf("write probe script: %v", err)
	}

	infoEmpty, _, err := probeStdioMCP(ctx, agentMCPServerEntry{
		Name:      "env-empty",
		Transport: "stdio",
		Command:   "/bin/sh",
		Args:      []string{probeScript},
	})
	if err != nil {
		t.Fatalf("probeStdioMCP empty env: %v", err)
	}
	if infoEmpty == nil || infoEmpty.Version != "||" {
		t.Fatalf("probeStdioMCP (empty env) leaked env: %+v", infoEmpty)
	}

	infoExplicit, _, err := probeStdioMCP(ctx, agentMCPServerEntry{
		Name:      "env-explicit",
		Transport: "stdio",
		Command:   "/bin/sh",
		Args:      []string{probeScript},
		Env:       map[string]string{"MCP_EXPLICIT_VAR": "kept-ok"},
	})
	if err != nil {
		t.Fatalf("probeStdioMCP explicit env: %v", err)
	}
	if infoExplicit == nil || infoExplicit.Version != "||kept-ok" {
		t.Fatalf("probeStdioMCP (explicit env) = %+v, want version=||kept-ok", infoExplicit)
	}
}
