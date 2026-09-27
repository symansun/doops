package server

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/user/doops/agent/api"
)

// executeStructuredSSHCommand keeps local SSH argv structured and quotes only
// the remote argv at the OpenSSH protocol boundary.
func executeStructuredSSHCommand(ctx context.Context, params api.SSHParams) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	args, err := buildSSHInvocation(params)
	if err != nil {
		return nil, nil, nil, err
	}
	return startRawCommand(ctx, exec.Command("ssh", args...))
}

func buildSSHInvocation(params api.SSHParams) ([]string, error) {
	host := strings.TrimSpace(params.Host)
	if host == "" {
		return nil, fmt.Errorf("SSH host must not be empty")
	}
	if strings.HasPrefix(host, "-") || strings.IndexByte(host, 0) >= 0 {
		return nil, fmt.Errorf("invalid SSH host")
	}

	args := make([]string, 0, len(params.Options)+2)
	for _, option := range params.Options {
		if option == "" || strings.IndexByte(option, 0) >= 0 {
			return nil, fmt.Errorf("SSH options must not be empty or contain NUL")
		}
		args = append(args, option)
	}
	args = append(args, host)
	if len(params.Command) == 0 {
		return args, nil
	}

	remote := make([]string, len(params.Command))
	for index, value := range params.Command {
		if strings.IndexByte(value, 0) >= 0 {
			return nil, fmt.Errorf("SSH command argument %d contains NUL", index)
		}
		remote[index] = shellQuote(value)
	}
	args = append(args, strings.Join(remote, " "))
	return args, nil
}

// shellQuote returns one POSIX shell word. OpenSSH sends the final argument
// as a remote command string, so each original argv must be quoted here.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sshAuditSummary(params api.SSHParams) string {
	return fmt.Sprintf("ssh host=%s options=%d remote_args=%d",
		strings.TrimSpace(params.Host), len(params.Options), len(params.Command))
}
