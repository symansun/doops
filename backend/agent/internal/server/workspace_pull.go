package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type workspacePullBeginParams struct {
	SessionID string `json:"session_id"`
}

type workspacePullChunkParams struct {
	BundleID string `json:"bundle_id"`
	Offset   int64  `json:"offset"`
	Limit    int64  `json:"limit,omitempty"`
}

type workspacePullBeginResult struct {
	BundleID  string `json:"bundle_id"`
	SessionID string `json:"session_id"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	ChunkSize int64  `json:"chunk_size"`
}

type workspacePullChunkResult struct {
	BundleID   string `json:"bundle_id"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	Size       int64  `json:"size"`
	EOF        bool   `json:"eof"`
	DataB64    string `json:"data_b64"`
}

func handleWorkspacePullBegin(argBytes json.RawMessage) (string, error) {
	var args workspacePullBeginParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid workspace pull begin params: %w", err)
	}
	sessionID := strings.TrimSpace(args.SessionID)
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	wsPath := filepath.Join(workspaceRoot(), sessionID)
	if info, err := os.Stat(wsPath); err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory: %s", wsPath)
	}

	if err := ensureWorkspaceGitSnapshot(wsPath, sessionID); err != nil {
		return "", err
	}

	if err := os.MkdirAll(workspacePullBundleDir(), 0700); err != nil {
		return "", err
	}
	bundleID := generateID()
	bundlePath := workspacePullBundlePath(bundleID)
	// The snapshot was pushed from the workspace's current HEAD to this bare
	// repository. Its master is the snapshot, not the workspace's own branch.
	repoPath := filepath.Join("/tmp/repos", sessionID+".git")
	if err := runCommand("", "git", "--git-dir", repoPath, "bundle", "create", bundlePath, "master"); err != nil {
		return "", err
	}
	info, err := os.Stat(bundlePath)
	if err != nil {
		return "", err
	}
	sum, err := sha256File(bundlePath)
	if err != nil {
		return "", err
	}
	result := workspacePullBeginResult{
		BundleID:  bundleID,
		SessionID: sessionID,
		Size:      info.Size(),
		SHA256:    sum,
		ChunkSize: workspacePullChunkBytes(),
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func handleWorkspacePullChunk(argBytes json.RawMessage) (string, error) {
	var args workspacePullChunkParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid workspace pull chunk params: %w", err)
	}
	if !validID(args.BundleID) {
		return "", fmt.Errorf("invalid bundle_id")
	}
	if args.Offset < 0 {
		return "", fmt.Errorf("offset must be >= 0")
	}
	limit := args.Limit
	maxChunk := workspacePullChunkBytes()
	if limit <= 0 || limit > maxChunk {
		limit = maxChunk
	}
	bundlePath := workspacePullBundlePath(args.BundleID)
	info, err := os.Stat(bundlePath)
	if err != nil {
		return "", err
	}
	if args.Offset > info.Size() {
		return "", fmt.Errorf("offset exceeds bundle size: %d > %d", args.Offset, info.Size())
	}
	f, err := os.Open(bundlePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Seek(args.Offset, io.SeekStart); err != nil {
		return "", err
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	buf = buf[:n]
	next := args.Offset + int64(n)
	result := workspacePullChunkResult{
		BundleID:   args.BundleID,
		Offset:     args.Offset,
		NextOffset: next,
		Size:       info.Size(),
		EOF:        next >= info.Size(),
		DataB64:    base64.StdEncoding.EncodeToString(buf),
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func ensureWorkspaceGitSnapshot(wsPath, sessionID string) error {
	if _, err := os.Stat(filepath.Join(wsPath, ".git")); os.IsNotExist(err) {
		if err := runCommand(wsPath, "git", "init", "-b", "master"); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := runCommand(wsPath, "git", "config", "user.name", "doops"); err != nil {
		return err
	}
	if err := runCommand(wsPath, "git", "config", "user.email", "doops@localhost"); err != nil {
		return err
	}
	if err := runCommand(wsPath, "git", "add", "-A"); err != nil {
		return err
	}
	if err := runCommand(wsPath, "git", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "DoopsPullSnapshot"); err != nil {
		return err
	}
	if err := os.MkdirAll("/tmp/repos", 0755); err != nil {
		return err
	}
	repoPath := filepath.Join("/tmp/repos", sessionID+".git")
	if err := ensureBareRepo(repoPath); err != nil {
		return err
	}
	return runCommand(wsPath, "git", "push", "-f", repoPath, "HEAD:refs/heads/master")
}

func ensureBareRepo(repoPath string) error {
	if _, err := os.Stat(repoPath); err == nil {
		if err := runCommand("", "git", "--git-dir", repoPath, "rev-parse", "--is-bare-repository"); err == nil {
			return nil
		}
		if err := os.RemoveAll(repoPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return runCommand("", "git", "init", "--bare", repoPath)
}

func runCommand(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func workspacePullBundleDir() string {
	return filepath.Join(os.TempDir(), "doops-pull-bundles")
}

func workspacePullBundlePath(bundleID string) string {
	return filepath.Join(workspacePullBundleDir(), bundleID+".bundle")
}

func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
