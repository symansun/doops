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

func checkoutWorkflowSource(sessionID, repository, branch, revision string) error {
	if err := validateSession(strings.TrimSpace(sessionID)); err != nil {
		return err
	}
	repository = strings.TrimSpace(repository)
	branch = strings.TrimSpace(branch)
	revision = strings.TrimSpace(revision)
	if repository == "" || branch == "" || !validCredentialWorkspaceCommit(revision) {
		return fmt.Errorf("workflow source repository, branch, and revision are required")
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
	gitEnvironment := append(
		os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ALLOW_PROTOCOL=https",
	)
	commands := [][]string{
		{"init", "-q", staging},
		{"-C", staging, "remote", "add", "origin", repository},
		{"-C", staging, "fetch", "--depth", "1", "--no-tags", "origin", revision},
		{"-C", staging, "checkout", "--detach", "-q", "FETCH_HEAD"},
	}
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, "git", arguments...)
		command.Env = gitEnvironment
		output, commandErr := command.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("workflow source checkout timed out")
		}
		if commandErr != nil {
			return fmt.Errorf("workflow source checkout failed: %s", strings.TrimSpace(string(output)))
		}
	}
	if err := verifyWorkflowSourceRevision(ctx, staging, revision, gitEnvironment); err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("replace workflow workspace: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("activate workflow workspace: %w", err)
	}
	return nil
}

func verifyWorkflowSourceRevision(ctx context.Context, workspace, revision string, environment []string) error {
	command := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "HEAD")
	command.Env = environment
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != strings.TrimSpace(revision) {
		return fmt.Errorf("workflow source revision changed during checkout")
	}
	return nil
}
