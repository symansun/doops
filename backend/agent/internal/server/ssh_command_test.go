package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestBuildSSHInvocationPreservesNestedShellArguments(t *testing.T) {
	params := api.SSHParams{
		Options: []string{"-o", "BatchMode=yes"},
		Host:    "ops@example.com",
		Command: []string{
			"bash",
			"-lc",
			`printf '%s|%s|%s' "a b" "c'd" '$HOME'`,
		},
	}
	args, err := buildSSHInvocation(params)
	if err != nil {
		t.Fatalf("build SSH invocation: %v", err)
	}
	if len(args) != 4 || args[0] != "-o" || args[1] != "BatchMode=yes" || args[2] != params.Host {
		t.Fatalf("unexpected SSH argv: %#v", args)
	}

	output, err := exec.Command("bash", "-c", args[3]).CombinedOutput()
	if err != nil {
		t.Fatalf("simulate remote shell: %v\n%s", err, output)
	}
	if got, want := string(output), "a b|c'd|$HOME"; got != want {
		t.Fatalf("nested shell argv was changed: got %q want %q", got, want)
	}
}

func TestBuildSSHInvocationRejectsInvalidBoundaryValues(t *testing.T) {
	tests := []api.SSHParams{
		{Host: ""},
		{Host: "-oProxyCommand=bad"},
		{Host: "good host"},
		{Host: "host", Options: []string{""}},
		{Host: "host", Options: []string{"ops@evil"}},
		{Host: "host", Options: []string{"-W", "evil:22"}},
		{Host: "host", Options: []string{"-o", "ProxyCommand=nc evil 22"}},
		{Host: "host", Options: []string{"-oProxyCommand=nc"}},
		{Host: "host", Options: []string{"-o", "HostName=evil"}},
		{Host: "host", Options: []string{"-o", "KnownHostsCommand=touch /tmp/x"}},
		{Host: "host", Options: []string{"-L", "127.0.0.1:80:localhost:80"}},
		{Host: "host", Options: []string{"-J", "jump.example"}},
		{Host: "host", Options: []string{"-F", "/tmp/ssh_config"}},
		{Host: "host", Command: []string{"bad\x00arg"}},
	}
	for _, params := range tests {
		if _, err := buildSSHInvocation(params); err == nil {
			t.Fatalf("expected invalid SSH params to fail: %#v", params)
		}
	}
}

func TestBuildSSHInvocationAcceptsConnectionFlagsOnly(t *testing.T) {
	args, err := buildSSHInvocation(api.SSHParams{
		Host: "ops@example.com",
		Options: []string{
			"-4",
			"-p", "2222",
			"-oBatchMode=yes",
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=5",
			"-o", "IdentitiesOnly=yes",
			"-i", "/keys/id_ed25519",
		},
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatalf("build SSH invocation: %v", err)
	}
	want := []string{
		"-4",
		"-p", "2222",
		"-oBatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=5",
		"-o", "IdentitiesOnly=yes",
		"-i", "/keys/id_ed25519",
		"ops@example.com",
		"'true'",
	}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("SSH argv = %#v\nwant %#v", args, want)
	}
}

func TestShellQuotePreservesSingleQuotes(t *testing.T) {
	value := `printf '%s' "a b"`
	got := shellQuote(value)
	want := `'printf '"'"'%s'"'"' "a b"'`
	if got != want {
		t.Fatalf("shell quote = %q want %q", got, want)
	}
}

func TestAgentWebSocketStructuredSSHPreservesRemoteCommandBoundary(t *testing.T) {
	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "ssh-args.txt")
	sshPath := filepath.Join(binDir, "ssh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOOPS_TEST_SSH_ARGS\"\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("DOOPS_TEST_SSH_ARGS", argsPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_ssh", map[string]interface{}{
		"session_id": "ssh-structured",
		"host":       "ops@example.com",
		"options":    []string{"-o", "BatchMode=yes"},
		"command": []string{
			"bash",
			"-lc",
			`printf '%s' "a b" "c'd"`,
		},
	})
	if errObj, ok := result["error"]; ok {
		t.Fatalf("structured SSH returned error: %#v", errObj)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake SSH argv: %v", err)
	}
	want := strings.Join([]string{
		"-o",
		"BatchMode=yes",
		"ops@example.com",
		`'bash' '-lc' 'printf '"'"'%s'"'"' "a b" "c'"'"'d"'`,
	}, "\n") + "\n"
	if string(data) != want {
		t.Fatalf("SSH argv boundary changed:\n got %q\nwant %q", string(data), want)
	}
}

func TestAgentWebSocketStructuredSSHRejectsPositionalOption(t *testing.T) {
	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "ssh-args.txt")
	sshPath := filepath.Join(binDir, "ssh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOOPS_TEST_SSH_ARGS\"\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake ssh: %v", err)
	}
	t.Setenv("DOOPS_TEST_SSH_ARGS", argsPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	gw := NewGateway()
	ts := newGatewayAuthorizedAgentServer(t, gw)
	defer ts.Close()
	conn := dialAgentTestWS(t, ts.URL)
	defer conn.Close()
	initializeAgentTestWS(t, conn)

	result := callToolResult(t, conn, "doops_ssh", map[string]interface{}{
		"session_id": "ssh-structured",
		"host":       "ops@good.example",
		"options":    []string{"ops@evil.example"},
		"command":    []string{"true"},
	})
	errObj, ok := result["error"].(map[string]interface{})
	if !ok || !strings.Contains(fmt.Sprint(errObj["message"]), "SSH option must be a flag") {
		t.Fatalf("positional SSH option must be rejected: %#v", result)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatal("rejected SSH option was still executed")
	}
}
