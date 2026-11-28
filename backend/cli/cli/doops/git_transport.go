package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	gitAuthCleanupOnce   sync.Once
	gitAuthCleanupMu     sync.Mutex
	signalCleanupDirs    = make(map[string]struct{})
	activeGitProcesses   = make(map[int]struct{})
	gitShutdownRequested bool
)

func buildGitRemoteURLForServer(server Server, sessionID string, authenticated bool) (string, error) {
	if strings.TrimSpace(server.Gateway) == "" {
		return "", fmt.Errorf("Git workspace transport requires a gateway target")
	}

	rawGateway := strings.TrimSpace(server.Gateway)
	if !strings.Contains(rawGateway, "://") {
		rawGateway = "https://" + rawGateway
	}
	u, err := url.Parse(rawGateway)
	if err != nil {
		return "", fmt.Errorf("invalid gateway URL %q: %w", server.Gateway, err)
	}
	switch u.Scheme {
	case "http", "https":
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return "", fmt.Errorf("unsupported gateway scheme %q", u.Scheme)
	}
	if err := enforceSecureGatewayURL(server.Gateway, u); err != nil {
		return "", err
	}
	if authenticated {
		u.User = url.User("doops")
	}
	cluster := strings.TrimSpace(server.Cluster)
	if cluster == "" {
		return "", fmt.Errorf("Git workspace transport requires target cluster")
	}
	instance := strings.TrimSpace(server.Instance)
	if instance == "" {
		return "", fmt.Errorf("Git workspace transport requires target instance")
	}
	u.Path = path.Join(u.Path, "v1", "git", cluster, instance, sessionID+".git")
	u.RawQuery = ""
	return u.String(), nil
}

func prepareGitAuthEnvironment(token string) ([]string, func(), error) {
	cleanupDeadGitAuthDirectories()
	env := filteredEnvironment(
		os.Environ(),
		"GIT_ASKPASS",
		"GIT_TERMINAL_PROMPT",
		"GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_NOSYSTEM",
		"GIT_CONFIG_COUNT",
		"DOOPS_GIT_TOKEN_FILE",
	)
	env = filteredEnvironmentPrefixes(env, "GIT_TRACE", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_")
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+gitNullDevice)
	if token == "" {
		return env, func() {}, nil
	}

	ensureSignalCleanupHandler()
	gitAuthCleanupMu.Lock()
	if gitShutdownRequested {
		gitAuthCleanupMu.Unlock()
		return nil, nil, fmt.Errorf("Git authentication canceled by shutdown")
	}
	buildingDir, err := os.MkdirTemp("", ".doops-git-auth-")
	if err != nil {
		gitAuthCleanupMu.Unlock()
		return nil, nil, fmt.Errorf("create Git authentication directory: %w", err)
	}
	lockPath := filepath.Join(buildingDir, "active.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.RemoveAll(buildingDir)
		gitAuthCleanupMu.Unlock()
		return nil, nil, fmt.Errorf("create Git authentication lock: %w", err)
	}
	if err := gitAuthLockExclusive(lockFile); err != nil {
		_ = lockFile.Close()
		_ = os.RemoveAll(buildingDir)
		gitAuthCleanupMu.Unlock()
		return nil, nil, fmt.Errorf("lock Git authentication directory: %w", err)
	}
	authDir := filepath.Join(
		filepath.Dir(buildingDir),
		fmt.Sprintf("doops-git-auth-%d-%s", os.Getpid(), strings.TrimPrefix(filepath.Base(buildingDir), ".doops-git-auth-")),
	)
	if err := os.Rename(buildingDir, authDir); err != nil {
		_ = gitAuthLockUnlock(lockFile)
		_ = lockFile.Close()
		_ = os.RemoveAll(buildingDir)
		gitAuthCleanupMu.Unlock()
		return nil, nil, fmt.Errorf("activate Git authentication directory: %w", err)
	}
	signalCleanupDirs[authDir] = struct{}{}
	gitAuthCleanupMu.Unlock()
	cleanup := func() {
		_ = gitAuthLockUnlock(lockFile)
		_ = lockFile.Close()
		cleanupSignalDirectory(authDir)
	}
	tokenPath := filepath.Join(authDir, "token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write Git authentication token: %w", err)
	}
	askPassPath := filepath.Join(authDir, "askpass")
	askPass := `#!/bin/sh
case "$1" in
  *Username*) printf '%s\n' 'doops' ;;
  *Password*) cat "$DOOPS_GIT_TOKEN_FILE"; printf '\n' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(askPassPath, []byte(askPass), 0o700); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write Git authentication helper: %w", err)
	}
	env = append(env, "GIT_ASKPASS="+askPassPath, "DOOPS_GIT_TOKEN_FILE="+tokenPath)
	return env, cleanup, nil
}

func ensureSignalCleanupHandler() {
	gitAuthCleanupOnce.Do(func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		go func() {
			sig := <-signals
			gitAuthCleanupMu.Lock()
			gitShutdownRequested = true
			dirs := make([]string, 0, len(signalCleanupDirs))
			for dir := range signalCleanupDirs {
				dirs = append(dirs, dir)
			}
			pids := make([]int, 0, len(activeGitProcesses))
			for pid := range activeGitProcesses {
				pids = append(pids, pid)
			}
			gitAuthCleanupMu.Unlock()
			terminateGitProcessGroups(pids)
			for _, dir := range dirs {
				_ = os.RemoveAll(dir)
			}
			if value, ok := sig.(syscall.Signal); ok {
				os.Exit(128 + int(value))
			}
			os.Exit(1)
		}()
	})
}

func runIsolatedGitCommand(cmd *exec.Cmd) error {
	setGitProcAttr(cmd)
	gitAuthCleanupMu.Lock()
	if gitShutdownRequested {
		gitAuthCleanupMu.Unlock()
		return fmt.Errorf("Git command canceled by shutdown")
	}
	if err := cmd.Start(); err != nil {
		gitAuthCleanupMu.Unlock()
		return err
	}
	pid := cmd.Process.Pid
	activeGitProcesses[pid] = struct{}{}
	gitAuthCleanupMu.Unlock()
	err := cmd.Wait()
	gitAuthCleanupMu.Lock()
	delete(activeGitProcesses, pid)
	gitAuthCleanupMu.Unlock()
	return err
}

func terminateGitProcessGroups(pids []int) {
	for _, pid := range pids {
		terminateGitPID(pid, false)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allExited := true
		for _, pid := range pids {
			if gitProcessAlive(pid) {
				allExited = false
				break
			}
		}
		if allExited {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, pid := range pids {
		terminateGitPID(pid, true)
	}
}

func cleanupSignalDirectory(dir string) {
	gitAuthCleanupMu.Lock()
	err := os.RemoveAll(dir)
	delete(signalCleanupDirs, dir)
	gitAuthCleanupMu.Unlock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: remove temporary directory: %v\n", err)
	}
}

func cleanupDeadGitAuthDirectories() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "doops-git-auth-") {
			continue
		}
		dir := filepath.Join(os.TempDir(), entry.Name())
		lockFile, err := os.OpenFile(filepath.Join(dir, "active.lock"), os.O_RDWR, 0)
		if err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		if err := gitAuthLockExclusive(lockFile); err != nil {
			_ = lockFile.Close()
			continue
		}
		_ = gitAuthLockUnlock(lockFile)
		_ = lockFile.Close()
		_ = os.RemoveAll(dir)
	}
}

func filteredEnvironment(env []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, found := blocked[key]; found {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func filteredEnvironmentPrefixes(env []string, prefixes ...string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		blocked := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func redactGitArgs(args []string) []string {
	redacted := append([]string(nil), args...)
	for i, arg := range redacted {
		if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
			redacted[i] = redactRemoteURL(arg)
		}
	}
	return redacted
}

func redactRemoteURL(remote string) string {
	parsed, err := url.Parse(remote)
	if err != nil || parsed.User == nil {
		return remote
	}
	parsed.User = url.User(parsed.User.Username())
	return parsed.String()
}

func describeGitStep(args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "init":
		return "初始化临时 Git 仓库"
	case "add":
		return "写入快照索引"
	case "push":
		return "推送增量到远端探针"
	case "-c":
		if len(args) >= 4 && args[2] == "commit" {
			return "生成同步提交"
		}
	}
	return ""
}

func gitCommandOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s 失败: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
