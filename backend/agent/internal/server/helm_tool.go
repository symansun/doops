package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/user/doops/agent/api"
)

var helmValueKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)

type helmCommand struct {
	args []string
	cwd  string
}

func handleHelmTool(ctx context.Context, raw json.RawMessage, push notificationSender) (string, error) {
	var params api.HelmParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return "", fmt.Errorf("invalid helm request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid helm request")
	}

	command, err := buildHelmCommand(params)
	if err != nil {
		return "", err
	}
	if err := runHelmCommand(ctx, command, push); err != nil {
		return "", err
	}
	return "Helm operation complete.", nil
}

func buildHelmCommand(params api.HelmParams) (helmCommand, error) {
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.Operation = strings.TrimSpace(params.Operation)
	params.Release = strings.TrimSpace(params.Release)
	params.Chart = strings.TrimSpace(params.Chart)
	params.Namespace = strings.TrimSpace(params.Namespace)
	params.Timeout = strings.TrimSpace(params.Timeout)

	if err := validateSession(params.SessionID); err != nil {
		return helmCommand{}, err
	}
	if !params.Confirm {
		return helmCommand{}, fmt.Errorf("confirm=true is required for helm mutation %q", params.Operation)
	}
	workspace, err := workspacePath(params.SessionID)
	if err != nil {
		return helmCommand{}, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return helmCommand{}, fmt.Errorf("resolve bound workspace: %w", err)
	}
	workspaceInfo, err := os.Stat(workspace)
	if err != nil || !workspaceInfo.IsDir() {
		return helmCommand{}, fmt.Errorf("bound workspace is not available")
	}
	if !k8sNamePattern.MatchString(params.Release) {
		return helmCommand{}, fmt.Errorf("invalid helm release")
	}
	if !k8sNamePattern.MatchString(params.Namespace) {
		return helmCommand{}, fmt.Errorf("invalid kubernetes namespace")
	}
	if params.Timeout != "" && !k8sTimeoutPattern.MatchString(params.Timeout) {
		return helmCommand{}, fmt.Errorf("invalid helm timeout")
	}

	common := []string{"--namespace", params.Namespace, "--wait"}
	if params.Timeout != "" {
		common = append(common, "--timeout", params.Timeout)
	}

	switch params.Operation {
	case "upgrade-install":
		chart, err := resolveHelmWorkspacePath(workspace, params.Chart, "deploy/helm/", true)
		if err != nil {
			return helmCommand{}, err
		}
		args := []string{"upgrade", params.Release, chart, "--install", "--atomic"}
		for _, valuePath := range params.Values {
			resolved, err := resolveHelmWorkspacePath(workspace, strings.TrimSpace(valuePath), "deploy/", false)
			if err != nil {
				return helmCommand{}, err
			}
			args = append(args, "--values", resolved)
		}
		keys := make([]string, 0, len(params.SetStrings))
		for key := range params.SetStrings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := params.SetStrings[key]
			if !helmValueKeyPattern.MatchString(key) {
				return helmCommand{}, fmt.Errorf("invalid helm set_strings key")
			}
			if credentialSecretLikeKey(key) || credentialScalarContainsInlineMaterial(value) ||
				strings.ContainsAny(value, "\r\n") {
				return helmCommand{}, fmt.Errorf("helm set_strings contains forbidden credential material")
			}
			args = append(args, "--set-string", key+"="+escapeHelmSetString(value))
		}
		return helmCommand{args: append(args, common...), cwd: workspace}, nil
	case "rollback":
		if params.Revision <= 0 {
			return helmCommand{}, fmt.Errorf("positive revision is required for helm rollback")
		}
		args := []string{"rollback", params.Release, fmt.Sprintf("%d", params.Revision), "--atomic"}
		return helmCommand{args: append(args, common...), cwd: workspace}, nil
	case "uninstall":
		if params.Revision != 0 {
			return helmCommand{}, fmt.Errorf("revision is not valid for helm uninstall")
		}
		args := []string{"uninstall", params.Release}
		return helmCommand{args: append(args, common...), cwd: workspace}, nil
	default:
		return helmCommand{}, fmt.Errorf("unsupported helm operation %q", params.Operation)
	}
}

func resolveHelmWorkspacePath(root, value, requiredPrefix string, directory bool) (string, error) {
	if value == "" || filepath.IsAbs(value) {
		return "", fmt.Errorf("helm path must be a workspace-relative %s path", requiredPrefix)
	}
	clean := filepath.Clean(value)
	slash := filepath.ToSlash(clean)
	if slash == "." || strings.HasPrefix(slash, "../") || !strings.HasPrefix(slash, requiredPrefix) {
		return "", fmt.Errorf("helm path must be under %s", requiredPrefix)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("resolve helm path: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("helm path escapes the workspace")
	}
	resolvedSlash := filepath.ToSlash(relative)
	if !strings.HasPrefix(resolvedSlash, requiredPrefix) {
		return "", fmt.Errorf("resolved helm path must remain under %s", requiredPrefix)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect helm path: %w", err)
	}
	if info.IsDir() != directory {
		if directory {
			return "", fmt.Errorf("helm chart path must be a directory")
		}
		return "", fmt.Errorf("helm values path must be a file")
	}
	return resolvedSlash, nil
}

func escapeHelmSetString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, ",", `\,`, "=", `\=`)
	return replacer.Replace(value)
}

func runHelmCommand(ctx context.Context, command helmCommand, push notificationSender) error {
	cmd := exec.CommandContext(ctx, "helm", command.args...)
	cmd.Dir = command.cwd
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("start helm: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("start helm: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helm: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamReader(stdout, push)
	}()
	go func() {
		defer wg.Done()
		streamReader(stderr, push)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("helm %s failed: %w", strings.Join(command.args, " "), err)
	}
	return nil
}
