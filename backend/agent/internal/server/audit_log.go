package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const auditLogRoot = "/root/.doops/audit"

func appendSessionAuditLog(sessionID, content string) (string, error) {
	return appendSessionAuditLogAt(auditLogRoot, sessionID, content)
}

func appendSessionAuditLogAt(root, sessionID, content string) (string, error) {
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(root, sessionID+".log")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		return "", err
	}
	return path, nil
}

func writeSessionCommandAudit(sessionID, summary string, cmdErr error) {
	if sessionID == "" {
		return
	}
	entry := fmt.Sprintf("# [%s] exit=%d\n%s\n", time.Now().Format("2006-01-02 15:04:05"), commandExitCode(cmdErr), summary)
	if logFile, err := appendSessionAuditLog(sessionID, entry); err == nil {
		log.Printf("✅ Audit Log saved to %s", logFile)
	} else {
		log.Printf("❌ Failed to save Audit Log for session %s: %v", sessionID, err)
	}
}
