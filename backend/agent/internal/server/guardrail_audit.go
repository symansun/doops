package server

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/symansun/doops/backend/agent/internal/dispatcher"
)

func (gw *Gateway) checkCommand(sessionID, command string) error {
	if gw.Dispatcher == nil {
		return nil
	}
	err := gw.Dispatcher.Check(command)
	if err == nil {
		return nil
	}
	var blocked *dispatcher.BlockedError
	if !errors.As(err, &blocked) {
		return err
	}
	if auditErr := recordBlockedCommand(sessionID, blocked.Rule, command); auditErr != nil {
		return fmt.Errorf("%w; guardrail audit unavailable: %v", err, auditErr)
	}
	return err
}

func recordBlockedCommand(sessionID, rule, command string) error {
	if err := validateSession(sessionID); err != nil {
		return err
	}
	if err := os.MkdirAll(workspaceRoot(), 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(workspaceRoot())
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir(sessionID, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	workspace, err := root.OpenRoot(sessionID)
	if err != nil {
		return err
	}
	defer workspace.Close()
	file, err := workspace.OpenFile(".doops-audit-log", os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "# [%s] blocked=1 rule=%s\n%s\n", time.Now().UTC().Format(time.RFC3339), rule, redactSecretText(command))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
