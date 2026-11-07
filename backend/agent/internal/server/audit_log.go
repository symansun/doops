package server

import (
	"os"
	"path/filepath"
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
