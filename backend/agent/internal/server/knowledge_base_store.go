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
	maxKnowledgeBaseDocumentBytes = int64(1 << 20)
	maxKnowledgeBaseImageBytes    = int64(8 << 20)
	knowledgeBaseTrashDir         = ".trash"
)

type knowledgeBaseEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Kind     string `json:"kind"`
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
		kind := "document"
		if knowledgeBaseImageName(name) {
			kind = "image"
		}
		entries = append(entries, knowledgeBaseEntry{
			Name:     name,
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
			Kind:     kind,
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
// checkKnowledgeBaseDocumentSize keeps one saved file within the size the read
// path can return. Screenshots get a larger bound than text documents; there is
// no collection-wide quota.
func checkKnowledgeBaseDocumentSize(name string, size int64) error {
	limit := maxKnowledgeBaseDocumentBytes
	if knowledgeBaseImageName(name) {
		limit = maxKnowledgeBaseImageBytes
	}
	if size > limit {
		return fmt.Errorf(
			"knowledge base file is %d bytes, over the %d-byte limit for this kind of file",
			size, limit,
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
