package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// writeKnowledgeBaseBytes stores a binary attachment (an image) with the same
// atomic rename as a text document. The temporary name never ends in an image
// extension, so list and preview cannot pick it up.
func writeKnowledgeBaseBytes(sessionID, rawPath string, data []byte) (string, error) {
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
	if err := checkKnowledgeBaseDocumentSize(name, int64(len(data))); err != nil {
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
	tmpName := fmt.Sprintf(".doops-save-%d-%d-%d.tmp", os.Getpid(), time.Now().UnixNano(), knowledgeBaseTempSeq.Add(1))
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := tmp.Write(data)
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
	return fmt.Sprintf("Wrote %d bytes to %s", len(data), filepath.Join(rootPath, name)), nil
}

// readKnowledgeBaseImage returns an attachment as base64 with its MIME type.
// Callers inline it as a data URL; the knowledge base is never served by a
// separate HTTP endpoint.
func readKnowledgeBaseImage(sessionID, rawPath string) (string, error) {
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
	mime := knowledgeBaseImageMIME(name)
	if mime == "" {
		return "", fmt.Errorf("knowledge base attachment %s is not a supported image", name)
	}
	path := filepath.Join(rootPath, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("knowledge base attachment %s does not exist", name)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("knowledge base attachment %s is not a regular file", name)
	}
	if info.Size() > maxKnowledgeBaseImageBytes {
		return "", fmt.Errorf("knowledge base image is %d bytes, over the %d-byte limit", info.Size(), maxKnowledgeBaseImageBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxKnowledgeBaseImageBytes+1))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]interface{}{
		"name":     name,
		"kind":     "image",
		"mime":     mime,
		"encoding": "base64",
		"size":     len(data),
		"data":     base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}
