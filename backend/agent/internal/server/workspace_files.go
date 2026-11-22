package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/user/doops/agent/api"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func handleFileWrite(argBytes json.RawMessage) (string, error) {
	var args api.FileWriteParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid file write params: %w", err)
	}
	if int64(len(args.Content)) > maxFileWriteBytes() {
		return "", fmt.Errorf("file write exceeds max size: %d > %d", len(args.Content), maxFileWriteBytes())
	}
	if rawPath := strings.TrimSpace(args.Path); filepath.IsAbs(rawPath) {
		// Knowledge-base documents live outside the session workspace so they
		// survive workspace cleanup. Absolute paths are not session files.
		return writeKnowledgeBaseFile(args.SessionID, rawPath, args.Content)
	}
	path, err := resolveWorkspaceFilePath(args.SessionID, args.Path)
	if err != nil {
		return "", err
	}
	root, err := openWorkspaceFileRoot(args.SessionID, true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name, err := filepath.Rel(filepath.Join(workspaceRoot(), args.SessionID), path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(name)
	if parent != "." {
		current := ""
		for _, component := range strings.Split(parent, string(os.PathSeparator)) {
			current = filepath.Join(current, component)
			if err := root.Mkdir(current, 0755); err != nil && !os.IsExist(err) {
				return "", err
			}
		}
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	_, writeErr := io.WriteString(file, args.Content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), path), nil
}

func handleFileRead(argBytes json.RawMessage) (string, error) {
	var args api.FileReadParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid file read params: %w", err)
	}
	if rawPath := strings.TrimSpace(args.Path); filepath.IsAbs(rawPath) {
		return readKnowledgeBaseFile(args.SessionID, rawPath)
	}
	path, err := resolveWorkspaceFilePath(args.SessionID, args.Path)
	if err != nil {
		return "", err
	}
	root, err := openWorkspaceFileRoot(args.SessionID, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name, err := filepath.Rel(filepath.Join(workspaceRoot(), args.SessionID), path)
	if err != nil {
		return "", err
	}
	info, err := root.Stat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("doops read is for regular text files only: %s", path)
	}
	if info.Size() > maxFileReadBytes() {
		return "", fmt.Errorf("doops read is for viewing small text files only; file size %d exceeds limit %d", info.Size(), maxFileReadBytes())
	}
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileReadBytes()+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxFileReadBytes() {
		return "", errors.New("doops read exceeded file size limit")
	}
	if !looksLikeText(data) {
		return "", fmt.Errorf("doops read is for viewing small text files only; %s looks like binary data", path)
	}
	return string(data), nil
}

func openWorkspaceFileRoot(sessionID string, create bool) (*os.Root, error) {
	base := workspaceRoot()
	if create {
		if err := os.MkdirAll(base, 0755); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if create {
		if err := root.Mkdir(sessionID, 0755); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := root.Lstat(sessionID)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("workspace session root must be a directory")
	}
	return root.OpenRoot(sessionID)
}

func resolveWorkspaceFilePath(sessionID, rawPath string) (string, error) {
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rawPath) || strings.Contains(rawPath, "\x00") {
		return "", fmt.Errorf("unsafe workspace path: %q", rawPath)
	}
	clean := filepath.Clean(rawPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe workspace path: %q", rawPath)
	}
	root, err := workspacePath(sessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, clean)
	if !pathWithinRoot(root, path) {
		return "", fmt.Errorf("workspace path escapes session: %q", rawPath)
	}
	return path, nil
}

func pathWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

func workspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("DOOPS_WORKSPACE_ROOT")); root != "" {
		return root
	}
	return "/root/ws"
}

func workspacePath(sessionID string) (string, error) {
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	return filepath.Join(workspaceRoot(), sessionID), nil
}
