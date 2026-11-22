package server

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// knowledgeBaseTempSeq keeps concurrent saves in one Agent process from
// picking the same scratch file name; the wall clock alone repeats at
// microsecond resolution under load.
var knowledgeBaseTempSeq atomic.Uint64

func knowledgeBaseRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("DOOPS_KB_ROOT"))
	if root == "" {
		root = "/root/kb"
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("knowledge base root must be absolute")
	}
	root = filepath.Clean(root)
	if root == string(filepath.Separator) || filepath.Dir(root) == root {
		return "", fmt.Errorf("knowledge base root is unsafe")
	}
	return root, nil
}

func knowledgeBaseFileName(root, rawPath string) (string, error) {
	if rawPath == "" || rawPath != strings.TrimSpace(rawPath) || strings.Contains(rawPath, "\x00") || !filepath.IsAbs(rawPath) {
		return "", fmt.Errorf("unsafe workspace path: %q", rawPath)
	}
	name := filepath.Base(filepath.Clean(rawPath))
	full := filepath.Join(root, name)
	if filepath.Clean(rawPath) != full || !pathWithinRoot(root, full) || !safeKnowledgeBaseName(name) {
		return "", fmt.Errorf("unsafe workspace path: %q", rawPath)
	}
	return name, nil
}

func safeKnowledgeBaseName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 240 {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	ext := filepath.Ext(name)
	if !strings.EqualFold(ext, ".md") {
		return false
	}
	stem := strings.TrimSuffix(name, ext)
	if stem == "" || stem == "." || stem == ".." || stem != strings.TrimSpace(stem) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func openKnowledgeBaseRoot() (*os.Root, string, error) {
	rootPath, err := knowledgeBaseRoot()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(rootPath)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("unsafe knowledge base root")
	}
	// Lstat only the knowledge-base directory itself. Ancestor symlinks, such as
	// macOS /var -> /private/var, are not an escape of this directory.
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, "", err
	}
	return root, rootPath, nil
}

func rejectKnowledgeBaseSymlink(root *os.Root, name, rawPath string) error {
	info, err := root.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe workspace path: %q", rawPath)
	}
	return nil
}

func writeKnowledgeBaseFile(sessionID, rawPath, content string) (string, error) {
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	rootPath, err := knowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	name, err := knowledgeBaseFileName(rootPath, rawPath)
	if err != nil {
		return "", err
	}
	// Documents are read back into an agent or console session, so the save
	// limit is the read limit, and the root has a document and byte budget.
	if err := enforceKnowledgeBaseQuota(rootPath, name, int64(len(content))); err != nil {
		return "", err
	}
	root, rootPath, err := openKnowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := rejectKnowledgeBaseSymlink(root, name, rawPath); err != nil {
		return "", err
	}
	// Write a sibling temporary file and rename it into place, so a killed
	// process or a full disk leaves the previous document intact instead of a
	// truncated one. The temporary name never ends in .md, so the console
	// listing cannot mistake it for a document.
	tmpName := fmt.Sprintf(".doops-save-%d-%d-%d.tmp", os.Getpid(), time.Now().UnixNano(), knowledgeBaseTempSeq.Add(1))
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := io.WriteString(tmp, content)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = root.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(filepath.Join(rootPath, tmpName), filepath.Join(rootPath, name)); err != nil {
		_ = root.Remove(tmpName)
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), filepath.Join(rootPath, name)), nil
}

func readKnowledgeBaseFile(sessionID, rawPath string) (string, error) {
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	rootPath, err := knowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	name, err := knowledgeBaseFileName(rootPath, rawPath)
	if err != nil {
		return "", err
	}
	root, _, err := openKnowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := rejectKnowledgeBaseSymlink(root, name, rawPath); err != nil {
		return "", err
	}
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("doops read is for small text files only: %s is not a regular file", rawPath)
	}
	if info.Size() > maxFileReadBytes() {
		return "", fmt.Errorf("doops read is for viewing small text files only; file size %d exceeds limit %d", info.Size(), maxFileReadBytes())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileReadBytes()+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxFileReadBytes() {
		return "", fmt.Errorf("doops read is for viewing small text files only; file size %d exceeds limit %d", len(data), maxFileReadBytes())
	}
	if !looksLikeText(data) {
		return "", fmt.Errorf("doops read is for viewing small text files only; %s looks like binary data", rawPath)
	}
	return string(data), nil
}
