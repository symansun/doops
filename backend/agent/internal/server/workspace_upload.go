package server

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var uploadLocks sync.Map
var workspaceReplacementMu sync.Mutex

type workspaceBeginParams struct {
	SessionID string `json:"session_id"`
	Commit    string `json:"commit"`
}

type workspaceChunkParams struct {
	UploadID string `json:"upload_id"`
	Seq      int    `json:"seq"`
	DataB64  string `json:"data_b64"`
}

type workspaceCommitParams struct {
	UploadID  string `json:"upload_id"`
	SessionID string `json:"session_id"`
	Commit    string `json:"commit"`
}

type workspaceUploadMeta struct {
	SessionID string    `json:"session_id"`
	Commit    string    `json:"commit"`
	NextSeq   int       `json:"next_seq"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

func handleWorkspaceBegin(argBytes json.RawMessage) (string, error) {
	var args workspaceBeginParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid workspace begin params: %w", err)
	}
	if err := validateSession(args.SessionID); err != nil {
		return "", err
	}
	commit, err := normalizeSHA256(args.Commit)
	if err != nil {
		return "", err
	}
	if err := cleanupExpiredUploads(defaultWorkspaceUploadMaxAge); err != nil {
		return "", err
	}
	uploadID := "upl_" + randomHex(16)
	if err := os.MkdirAll(uploadDir(), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(uploadArchivePath(uploadID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = cleanupUpload(uploadID)
		return "", err
	}
	if err := saveUploadMeta(uploadID, workspaceUploadMeta{
		SessionID: args.SessionID,
		Commit:    commit,
		NextSeq:   0,
		Size:      0,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		_ = cleanupUpload(uploadID)
		return "", err
	}
	return uploadID, nil
}

func handleWorkspaceChunk(argBytes json.RawMessage) (string, error) {
	var args workspaceChunkParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid workspace chunk params: %w", err)
	}
	if err := validateUploadID(args.UploadID); err != nil {
		return "", err
	}
	unlock := lockUpload(args.UploadID)
	defer unlock()
	if args.Seq < 0 {
		return "", fmt.Errorf("invalid chunk sequence: %d", args.Seq)
	}
	meta, err := loadUploadMeta(args.UploadID)
	if err != nil {
		return "", err
	}
	if args.Seq != meta.NextSeq {
		return "", fmt.Errorf("unexpected chunk sequence: got %d want %d", args.Seq, meta.NextSeq)
	}
	data, err := base64.StdEncoding.DecodeString(args.DataB64)
	if err != nil {
		return "", fmt.Errorf("decode chunk %d: %w", args.Seq, err)
	}
	if meta.Size+int64(len(data)) > maxWorkspaceUploadBytes() {
		_ = cleanupUpload(args.UploadID)
		return "", fmt.Errorf("upload exceeds max size: %d > %d", meta.Size+int64(len(data)), maxWorkspaceUploadBytes())
	}
	f, err := os.OpenFile(uploadArchivePath(args.UploadID), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		_ = cleanupUpload(args.UploadID)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = cleanupUpload(args.UploadID)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = cleanupUpload(args.UploadID)
		return "", err
	}
	meta.NextSeq++
	meta.Size += int64(len(data))
	if err := saveUploadMeta(args.UploadID, meta); err != nil {
		_ = cleanupUpload(args.UploadID)
		return "", err
	}
	return fmt.Sprintf("chunk %d accepted (%d bytes)", args.Seq, len(data)), nil
}

func lockUpload(uploadID string) func() {
	value, _ := uploadLocks.LoadOrStore(uploadID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return func() {
		mu.Unlock()
	}
}

func handleWorkspaceCommit(argBytes json.RawMessage) (string, error) {
	var args workspaceCommitParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid workspace commit params: %w", err)
	}
	if err := validateUploadID(args.UploadID); err != nil {
		return "", err
	}
	if err := validateSession(args.SessionID); err != nil {
		return "", err
	}
	commit, err := normalizeSHA256(args.Commit)
	if err != nil {
		return "", err
	}
	meta, err := loadUploadMeta(args.UploadID)
	if err != nil {
		return "", err
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = cleanupUpload(args.UploadID)
		}
	}()
	if meta.SessionID != args.SessionID {
		return "", fmt.Errorf("upload session mismatch: got %q want %q", args.SessionID, meta.SessionID)
	}
	if meta.Commit != commit {
		return "", fmt.Errorf("upload commit mismatch: got %q want %q", commit, meta.Commit)
	}
	archivePath := uploadArchivePath(args.UploadID)
	actual, err := fileSHA256Hex(archivePath)
	if err != nil {
		return "", err
	}
	if actual != commit {
		return "", fmt.Errorf("sha256 mismatch: got %s want %s", actual, commit)
	}
	dest, err := workspacePath(args.SessionID)
	if err != nil {
		return "", err
	}
	root := workspaceRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	tmpDest := filepath.Join(root, "."+args.SessionID+".tmp-"+randomHex(8))
	if err := os.MkdirAll(tmpDest, 0755); err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDest)
	if err := extractTarGz(archivePath, tmpDest, maxWorkspaceUploadBytes()); err != nil {
		return "", err
	}
	ready := filepath.Join(tmpDest, ".doops-ready")
	tmp := ready + ".tmp"
	if err := os.WriteFile(tmp, []byte(commit+"\n"), 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, ready); err != nil {
		return "", err
	}
	if err := replaceWorkspace(tmpDest, dest); err != nil {
		return "", err
	}
	if err := cleanupUpload(args.UploadID); err != nil {
		return "", err
	}
	cleanupOnFailure = false
	return fmt.Sprintf("workspace ready: %s", dest), nil
}

func replaceWorkspace(tmpDest, dest string) error {
	workspaceReplacementMu.Lock()
	defer workspaceReplacementMu.Unlock()
	if info, err := os.Lstat(dest); os.IsNotExist(err) {
		if err := os.Rename(tmpDest, dest); err != nil {
			return err
		}
	} else {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("workspace destination is not a directory")
		}
		// The old tree moves to tmpDest in the same atomic operation.
		if err := exchangeWorkspaceDirectories(tmpDest, dest); err != nil {
			return fmt.Errorf("atomic workspace replacement: %w", err)
		}
	}
	return nil
}

func extractTarGz(archivePath, dest string, maxBytes int64) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var extracted int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(os.PathSeparator)) || name == ".." {
			return fmt.Errorf("unsafe archive path: %s", hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size < 0 {
				return fmt.Errorf("invalid archive file size: %s", hdr.Name)
			}
			extracted += hdr.Size
			if maxBytes > 0 && extracted > maxBytes {
				return fmt.Errorf("extracted workspace exceeds max size: %d > %d", extracted, maxBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func uploadDir() string {
	return filepath.Join(os.TempDir(), "doops-uploads")
}

func uploadArchivePath(uploadID string) string {
	return filepath.Join(uploadDir(), uploadID+".tar.gz")
}

func uploadMetaPath(uploadID string) string {
	return filepath.Join(uploadDir(), uploadID+".json")
}

func saveUploadMeta(uploadID string, meta workspaceUploadMeta) error {
	if err := validateUploadID(uploadID); err != nil {
		return err
	}
	if err := validateSession(meta.SessionID); err != nil {
		return err
	}
	commit, err := normalizeSHA256(meta.Commit)
	if err != nil {
		return err
	}
	meta.Commit = commit
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(uploadDir(), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := uploadMetaPath(uploadID) + "." + randomHex(4) + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, uploadMetaPath(uploadID)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func loadUploadMeta(uploadID string) (workspaceUploadMeta, error) {
	if err := validateUploadID(uploadID); err != nil {
		return workspaceUploadMeta{}, err
	}
	data, err := os.ReadFile(uploadMetaPath(uploadID))
	if err != nil {
		return workspaceUploadMeta{}, fmt.Errorf("load upload metadata: %w", err)
	}
	var meta workspaceUploadMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return workspaceUploadMeta{}, fmt.Errorf("parse upload metadata: %w", err)
	}
	if err := validateSession(meta.SessionID); err != nil {
		return workspaceUploadMeta{}, err
	}
	commit, err := normalizeSHA256(meta.Commit)
	if err != nil {
		return workspaceUploadMeta{}, err
	}
	meta.Commit = commit
	return meta, nil
}

func cleanupUpload(uploadID string) error {
	if err := validateUploadID(uploadID); err != nil {
		return err
	}
	return errors.Join(removeIfExists(uploadArchivePath(uploadID)), removeIfExists(uploadMetaPath(uploadID)))
}

func cleanupExpiredUploads(maxAge time.Duration) error {
	if maxAge <= 0 {
		return nil
	}
	entries, err := os.ReadDir(uploadDir())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var joined error
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "upl_") && strings.HasSuffix(name, ".json") {
			uploadID := strings.TrimSuffix(name, ".json")
			if validateUploadID(uploadID) != nil {
				continue
			}
			createdAt, ok := uploadMetadataCreatedAt(uploadID, entry)
			if ok && now.Sub(createdAt) > maxAge {
				joined = errors.Join(joined, cleanupUpload(uploadID))
			}
			continue
		}
		if strings.HasPrefix(name, "upl_") && strings.HasSuffix(name, ".tar.gz") {
			uploadID := strings.TrimSuffix(name, ".tar.gz")
			if validateUploadID(uploadID) != nil {
				continue
			}
			if _, err := os.Stat(uploadMetaPath(uploadID)); err == nil {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				joined = errors.Join(joined, err)
				continue
			}
			if now.Sub(info.ModTime().UTC()) > maxAge {
				joined = errors.Join(joined, cleanupUpload(uploadID))
			}
		}
	}
	return joined
}

func uploadMetadataCreatedAt(uploadID string, entry os.DirEntry) (time.Time, bool) {
	meta, err := loadUploadMeta(uploadID)
	if err == nil && !meta.CreatedAt.IsZero() {
		return meta.CreatedAt.UTC(), true
	}
	info, err := entry.Info()
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime().UTC(), true
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func normalizeSHA256(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("commit must be a sha256 hex digest")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("commit must be a sha256 hex digest: %w", err)
	}
	return value, nil
}

func fileSHA256Hex(path string) (string, error) {
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

func validateUploadID(uploadID string) error {
	uploadID = strings.TrimSpace(uploadID)
	if !strings.HasPrefix(uploadID, "upl_") || strings.ContainsAny(uploadID, `/\.`) {
		return fmt.Errorf("unsafe upload id: %q", uploadID)
	}
	return nil
}
