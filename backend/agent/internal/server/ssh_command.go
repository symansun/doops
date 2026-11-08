package server

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"unicode"

	"github.com/symansun/doops/backend/agent/api"
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

	if strings.ContainsFunc(host, isSSHControlOrSpace) {
		return nil, fmt.Errorf("invalid SSH host")
	}
	args, err := validatedSSHOptions(params.Options)
	if err != nil {
		return nil, err
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

func validatedSSHOptions(options []string) ([]string, error) {
	args := make([]string, 0, len(options))
	for index := 0; index < len(options); index++ {
		option := strings.TrimSpace(options[index])
		if option == "" || strings.ContainsRune(option, 0) {
			return nil, fmt.Errorf("SSH options must not be empty or contain NUL")
		}
		if !strings.HasPrefix(option, "-") || option == "-" || option == "--" {
			return nil, fmt.Errorf("SSH option must be a flag")
		}
		switch {
		case option == "-4" || option == "-6":
			args = append(args, option)
		case option == "-p" || strings.HasPrefix(option, "-p"):
			port, next, err := sshOptionValue("-p", option, options, index)
			if err != nil {
				return nil, err
			}
			if err := validateSSHPort(port); err != nil {
				return nil, err
			}
			args = appendSSHOption(args, "-p", option, port)
			index = next
		case option == "-i" || strings.HasPrefix(option, "-i"):
			path, next, err := sshOptionValue("-i", option, options, index)
			if err != nil {
				return nil, err
			}
			if err := validateIdentityFile(path); err != nil {
				return nil, err
			}
			args = appendSSHOption(args, "-i", option, path)
			index = next
		case option == "-o" || strings.HasPrefix(option, "-o"):
			assignment, next, err := sshOptionValue("-o", option, options, index)
			if err != nil {
				return nil, err
			}
			if err := validateSSHConfigAssignment(assignment); err != nil {
				return nil, err
			}
			args = appendSSHOption(args, "-o", option, strings.TrimSpace(assignment))
			index = next
		default:
			return nil, fmt.Errorf("SSH option is not allowed")
		}
	}
	return args, nil
}

func appendSSHOption(args []string, flag, option, value string) []string {
	if option == flag {
		return append(args, flag, value)
	}
	return append(args, option)
}

func sshOptionValue(flag, option string, options []string, index int) (string, int, error) {
	if option != flag {
		value := strings.TrimSpace(option[len(flag):])
		if value == "" || strings.ContainsRune(value, 0) || strings.ContainsFunc(value, isSSHLineBreak) {
			return "", index, fmt.Errorf("SSH option %s has an invalid value", flag)
		}
		return value, index, nil
	}
	if index+1 >= len(options) {
		return "", index, fmt.Errorf("SSH option %s requires a value", flag)
	}
	value := strings.TrimSpace(options[index+1])
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsRune(value, 0) {
		return "", index, fmt.Errorf("SSH option %s requires a value", flag)
	}
	return value, index + 1, nil
}

func validateSSHConfigAssignment(assignment string) error {
	key, value, ok := strings.Cut(strings.TrimSpace(assignment), "=")
	if !ok {
		return fmt.Errorf("SSH -o value must be key=value")
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	keyword, ok := sshKeyword(key)
	if !ok || value == "" || strings.ContainsFunc(value, isSSHLineBreak) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("SSH config option is not allowed")
	}
	switch keyword {
	case "batchmode", "identitiesonly":
		if !isYesOrNo(value) {
			return fmt.Errorf("SSH config option %s has an invalid value", keyword)
		}
	case "stricthostkeychecking":
		switch strings.ToLower(value) {
		case "yes", "no", "ask", "accept-new":
		default:
			return fmt.Errorf("SSH config option %s has an invalid value", keyword)
		}
	case "connecttimeout":
		return validateSSHTimeout(value)
	case "port":
		return validateSSHPort(value)
	case "identityfile":
		return validateIdentityFile(value)
	default:
		return fmt.Errorf("SSH config option %s is not allowed", keyword)
	}
	return nil
}

func sshKeyword(key string) (string, bool) {
	if key == "" || len(key) > 64 {
		return "", false
	}
	for _, r := range key {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) {
			return "", false
		}
	}
	keyword := strings.ToLower(key)
	switch keyword {
	case "batchmode", "stricthostkeychecking", "connecttimeout", "identitiesonly", "identityfile", "port":
		return keyword, true
	default:
		return keyword, false
	}
}

func validateSSHPort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || strconv.Itoa(port) != value || port < 1 || port > 65535 {
		return fmt.Errorf("SSH port is invalid")
	}
	return nil
}

func validateSSHTimeout(value string) error {
	timeout, err := strconv.Atoi(value)
	if err != nil || strconv.Itoa(timeout) != value || timeout < 0 || timeout > 86400 {
		return fmt.Errorf("SSH connect timeout is invalid")
	}
	return nil
}

func validateIdentityFile(value string) error {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsRune(value, 0) || strings.ContainsFunc(value, isSSHLineBreak) {
		return fmt.Errorf("SSH identity file is invalid")
	}
	return nil
}

func isYesOrNo(value string) bool {
	switch strings.ToLower(value) {
	case "yes", "no":
		return true
	default:
		return false
	}
}

func isSSHLineBreak(r rune) bool {
	return r == '\n' || r == '\r'
}

func isSSHControlOrSpace(r rune) bool {
	return r <= 0x20 || r == 0x7f || r == '\n' || r == '\r'
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
