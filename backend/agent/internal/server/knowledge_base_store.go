package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// The knowledge base is a flat directory of Markdown documents on the target.
// These bounds keep one Agent from filling its node with documents that no
// session can read back.
const (
	defaultKnowledgeBaseMaxDocuments = 500
	defaultKnowledgeBaseMaxBytes     = int64(256 << 20)
	maxKnowledgeBaseDocumentBytes    = int64(1 << 20)
	knowledgeBaseTrashDir            = ".trash"
)

func knowledgeBaseMaxDocuments() int64 {
	return positiveEnvInt64("DOOPS_KB_MAX_DOCUMENTS", defaultKnowledgeBaseMaxDocuments)
}

func knowledgeBaseMaxBytes() int64 {
	return positiveEnvInt64("DOOPS_KB_MAX_BYTES", defaultKnowledgeBaseMaxBytes)
}

type knowledgeBaseEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// listKnowledgeBaseDocuments returns the documents in the knowledge base root,
// newest modification first. Hidden entries and the trash directory are not
// documents.
func listKnowledgeBaseDocuments(sessionID string) (string, []knowledgeBaseEntry, error) {
	if err := validateSession(sessionID); err != nil {
		return "", nil, err
	}
	rootPath, err := knowledgeBaseRoot()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		return "", nil, err
	}
	dirEntries, err := os.ReadDir(rootPath)
	if err != nil {
		return "", nil, err
	}
	entries := make([]knowledgeBaseEntry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || strings.HasPrefix(name, ".") || !safeKnowledgeBaseName(name) {
			continue
		}
		info, err := dirEntry.Info()
		if err != nil {
			continue
		}
		entries = append(entries, knowledgeBaseEntry{
			Name:     name,
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Modified != entries[j].Modified {
			return entries[i].Modified > entries[j].Modified
		}
		return entries[i].Name < entries[j].Name
	})
	if entries == nil {
		entries = []knowledgeBaseEntry{}
	}
	return rootPath, entries, nil
}

// deleteKnowledgeBaseDocument moves a document into the trash instead of
// unlinking it, so an operator can recover a document deleted by mistake. The
// trash directory is collected on the same schedule as the session areas.
func deleteKnowledgeBaseDocument(sessionID, rawPath string) (string, error) {
	if err := validateSession(sessionID); err != nil {
		return "", err
	}
	rootPath, err := knowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	root, _, err := openKnowledgeBaseRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	name, err := knowledgeBaseFileName(rootPath, rawPath)
	if err != nil {
		return "", err
	}
	if err := rejectKnowledgeBaseSymlink(root, name, rawPath); err != nil {
		return "", err
	}
	if _, err := root.Stat(name); err != nil {
		return "", fmt.Errorf("knowledge base document %s does not exist", name)
	}
	trashPath := filepath.Join(rootPath, knowledgeBaseTrashDir)
	if err := os.MkdirAll(trashPath, 0o700); err != nil {
		return "", err
	}
	trashed := filepath.Join(knowledgeBaseTrashDir, fmt.Sprintf("%d-%s", time.Now().UTC().UnixNano(), name))
	if err := os.Rename(filepath.Join(rootPath, name), filepath.Join(rootPath, trashed)); err != nil {
		return "", err
	}
	return trashed, nil
}

// knowledgeBaseUsage counts live documents and their bytes. The trash is not
// live content and does not count against the quota.
func knowledgeBaseUsage(rootPath string) (int, int64, error) {
	dirEntries, err := os.ReadDir(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	count := 0
	var total int64
	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || strings.HasPrefix(name, ".") || !safeKnowledgeBaseName(name) {
			continue
		}
		info, err := dirEntry.Info()
		if err != nil {
			continue
		}
		count++
		total += info.Size()
	}
	return count, total, nil
}

// enforceKnowledgeBaseQuota rejects a save that would exceed the document count
// or total size bound. Rewriting an existing document only has to respect the
// total-size bound.
func enforceKnowledgeBaseQuota(rootPath, name string, contentBytes int64) error {
	if contentBytes > maxKnowledgeBaseDocumentBytes {
		return fmt.Errorf(
			"knowledge base document is %d bytes, over the %d-byte limit: split it into smaller documents before saving",
			contentBytes, maxKnowledgeBaseDocumentBytes,
		)
	}
	count, total, err := knowledgeBaseUsage(rootPath)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(rootPath, name)); statErr == nil {
		total -= int64(0)
	} else if int64(count) >= knowledgeBaseMaxDocuments() {
		return fmt.Errorf(
			"knowledge base is full: %d documents, limit %d; delete a document before creating another",
			count, knowledgeBaseMaxDocuments(),
		)
	}
	if total+contentBytes > knowledgeBaseMaxBytes() {
		return fmt.Errorf(
			"knowledge base is full: %d bytes in use, limit %d; delete documents before adding more",
			total, knowledgeBaseMaxBytes(),
		)
	}
	return nil
}

// knowledgeBaseOnVolume reports whether the knowledge base root is a separate
// mount point from its parent directory. It detects "this data is on a bounded
// volume" but cannot prove persistence: an emptyDir also has its own device.
func knowledgeBaseOnVolume(rootPath string) bool {
	rootInfo, err := os.Stat(rootPath)
	if err != nil {
		return false
	}
	parentInfo, err := os.Stat(filepath.Dir(rootPath))
	if err != nil {
		return false
	}
	rootStat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return rootStat.Dev != parentStat.Dev
}

// pruneKnowledgeBaseTrash removes trashed documents older than maxAge so a
// delete does not become permanent growth.
func pruneKnowledgeBaseTrash(maxAge time.Duration) {
	rootPath, err := knowledgeBaseRoot()
	if err != nil {
		return
	}
	trashPath := filepath.Join(rootPath, knowledgeBaseTrashDir)
	dirEntries, err := os.ReadDir(trashPath)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, dirEntry := range dirEntries {
		info, err := dirEntry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(trashPath, dirEntry.Name())); err != nil {
			continue
		}
	}
}
