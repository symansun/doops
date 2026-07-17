package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func checkoutWorkflowSource(sessionID, repository, branch string) error {
	if err := validateSession(strings.TrimSpace(sessionID)); err != nil {
		return err
	}
	repository = strings.TrimSpace(repository)
	branch = strings.TrimSpace(branch)
	if repository == "" || branch == "" {
		return fmt.Errorf("workflow source repository and branch are required")
	}

	destination, err := workspacePath(sessionID)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create workflow workspace parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".doops-source-")
	if err != nil {
		return fmt.Errorf("create workflow source staging directory: %w", err)
	}
	if err := os.Remove(staging); err != nil {
		return fmt.Errorf("prepare workflow source staging directory: %w", err)
	}
	defer os.RemoveAll(staging)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--single-branch", "--branch", branch, "--", repository, staging)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("workflow source clone timed out")
	}
	if err != nil {
		return fmt.Errorf("workflow source clone failed: %s", strings.TrimSpace(string(output)))
	}
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("replace workflow workspace: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("activate workflow workspace: %w", err)
	}
	return nil
}
