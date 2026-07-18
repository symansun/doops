package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func workspaceCommitStatePath() string {
	return filepath.Join(doopsStateDir(), "workspace-commits.json")
}

func workspaceCommitStateKey(target, sessionID string) (string, error) {
	target = strings.TrimSpace(target)
	sessionID = strings.TrimSpace(sessionID)
	if target == "" || sessionID == "" {
		return "", fmt.Errorf("target and session are required for workspace commit state")
	}
	return target + ":" + sessionID, nil
}

func saveWorkspaceCommit(target, sessionID, commit string) error {
	key, err := workspaceCommitStateKey(target, sessionID)
	if err != nil {
		return err
	}
	commit = strings.TrimSpace(commit)
	if !validWorkspaceCommit(commit) {
		return fmt.Errorf("workspace commit is invalid")
	}
	path := workspaceCommitStatePath()
	state := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode workspace commit state: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read workspace commit state: %w", err)
	}
	state[key] = commit
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace commit state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create workspace commit state directory: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("write workspace commit state: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace workspace commit state: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func loadWorkspaceCommit(target, sessionID string) (string, error) {
	key, err := workspaceCommitStateKey(target, sessionID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(workspaceCommitStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("workspace commit state is missing; run doops push for this target and session")
		}
		return "", fmt.Errorf("read workspace commit state: %w", err)
	}
	var state map[string]string
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("decode workspace commit state: %w", err)
	}
	commit := strings.TrimSpace(state[key])
	if !validWorkspaceCommit(commit) {
		return "", fmt.Errorf("workspace commit state is missing; run doops push for this target and session")
	}
	return commit, nil
}
