package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// File-first inspection artifacts (SKILL_FIRST_INSPECTION_SPEC.md 3.1/3.2).
//
// Each run item materializes its real payload on disk under
// <dbDir>/artifacts/<date>/<run_id>/item_<id>/ and SQLite keeps only a light
// index (relative directory, byte count, sha256, truncation flag). The text
// columns stay empty for file-backed rows so the database no longer carries
// the bulk payload; legacy rows without a directory still read their text
// columns, which keeps upgrades lossless.
const (
	monitorArtifactMarkdownName = "artifact.md"
	monitorArtifactJSONName     = "artifact.json"
	monitorArtifactRawName      = "raw.txt"
)

// errMonitorArtifactStorageDisabled means no <dbDir>/artifacts root exists, so
// the caller must keep the payload in the SQLite text columns instead.
var errMonitorArtifactStorageDisabled = errors.New("monitor artifact storage disabled")

var monitorArtifactRunIDPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]*$")

// monitorArtifactContent is the sanitized payload of one run item ready for disk.
type monitorArtifactContent struct {
	Markdown string
	JSON     json.RawMessage
	Raw      string
}

// monitorArtifactRoot returns <dbDir>/artifacts, or "" when filesystem storage
// is disabled for this store.
func (s *GatewayStore) monitorArtifactRoot() string {
	if s == nil || s.dbDir == "" || s.dbDir == "." {
		return ""
	}
	return filepath.Join(s.dbDir, "artifacts")
}

func monitorArtifactDate(atMs int64) string {
	if atMs <= 0 {
		atMs = time.Now().UnixMilli()
	}
	return time.UnixMilli(atMs).UTC().Format("2006-01-02")
}

// cleanMonitorArtifactRelativeDir accepts only a normalized relative directory
// below the artifacts root, rejecting absolute paths and traversals.
func cleanMonitorArtifactRelativeDir(relDir string) (string, bool) {
	rel := filepath.ToSlash(strings.TrimSpace(relDir))
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(relDir) {
		return "", false
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	return rel, true
}

func monitorArtifactRelativeDir(runID string, itemID int64, atMs int64) (string, error) {
	if itemID <= 0 {
		return "", errors.New("invalid monitor artifact item id")
	}
	runID = strings.TrimSpace(runID)
	if !monitorArtifactRunIDPattern.MatchString(runID) || strings.Contains(runID, "..") {
		return "", errors.New("invalid monitor artifact run id")
	}
	return monitorArtifactDate(atMs) + "/" + runID + "/" + fmt.Sprintf("item_%d", itemID), nil
}

// prepareMonitorArtifactText redacts and caps one text payload, reporting
// whether the cap truncated it.
func prepareMonitorArtifactText(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	clean := sanitizeAndCapArtifact(trimmed)
	return clean, strings.HasSuffix(clean, "\n... [truncated]")
}

// prepareMonitorArtifactJSON keeps the artifact JSON valid. Oversized JSON is
// replaced by a truthful truncation envelope instead of a mid-object cut that
// would no longer parse.
func prepareMonitorArtifactJSON(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	compact, ok := compactValidJSON(redactSecretText(string(raw)))
	if !ok {
		compact, ok = compactValidJSON(string(raw))
	}
	if !ok {
		return nil, false
	}
	if len(compact) <= maxInspectionArtifactBytes {
		return compact, false
	}
	envelope, err := json.Marshal(map[string]any{
		"truncated":      true,
		"original_bytes": len(compact),
		"limit_bytes":    maxInspectionArtifactBytes,
	})
	if err != nil {
		return nil, true
	}
	return json.RawMessage(envelope), true
}

// prepareMonitorArtifactForItem sanitizes one item and reports truncation.
func prepareMonitorArtifactForItem(item monitorRunItemRecord) (monitorArtifactContent, bool) {
	markdown, markdownTruncated := prepareMonitorArtifactText(item.ArtifactMarkdown)
	raw, rawTruncated := prepareMonitorArtifactText(item.RawOutput)
	artifactJSON, jsonTruncated := prepareMonitorArtifactJSON(item.ArtifactJSON)
	if jsonTruncated && strings.TrimSpace(raw) == "" && len(item.ArtifactJSON) > 0 {
		// Keep the leading, redacted original JSON as raw evidence so an
		// oversized payload is truncated, never silently dropped.
		raw, rawTruncated = prepareMonitorArtifactText(string(item.ArtifactJSON))
	}
	return monitorArtifactContent{Markdown: markdown, JSON: artifactJSON, Raw: raw},
		markdownTruncated || rawTruncated || jsonTruncated
}

func monitorArtifactContentEmpty(content monitorArtifactContent) bool {
	return content.Markdown == "" && len(content.JSON) == 0 && content.Raw == ""
}

// writeMonitorArtifactFiles materializes one item directory atomically and
// returns its artifacts-relative path, total byte count and sha256.
func (s *GatewayStore) writeMonitorArtifactFiles(runID string, itemID int64, atMs int64, content monitorArtifactContent) (string, int64, string, error) {
	root := s.monitorArtifactRoot()
	if root == "" {
		return "", 0, "", errMonitorArtifactStorageDisabled
	}
	if monitorArtifactContentEmpty(content) {
		return "", 0, "", nil
	}
	relDir, err := monitorArtifactRelativeDir(runID, itemID, atMs)
	if err != nil {
		return "", 0, "", err
	}
	target := filepath.Join(root, filepath.FromSlash(relDir))
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", 0, "", err
	}
	tmpDir, err := os.MkdirTemp(parent, ".stage-item-*")
	if err != nil {
		return "", 0, "", err
	}
	defer os.RemoveAll(tmpDir)

	digest := sha256.New()
	var size int64
	write := func(name, body string) error {
		if body == "" {
			return nil
		}
		fmt.Fprintf(digest, "%s\x00%d\x00", name, len(body))
		digest.Write([]byte(body))
		size += int64(len(body))
		return os.WriteFile(filepath.Join(tmpDir, name), []byte(body), 0600)
	}
	if err := write(monitorArtifactMarkdownName, content.Markdown); err != nil {
		return "", 0, "", err
	}
	if err := write(monitorArtifactJSONName, string(content.JSON)); err != nil {
		return "", 0, "", err
	}
	if err := write(monitorArtifactRawName, content.Raw); err != nil {
		return "", 0, "", err
	}
	if err := os.RemoveAll(target); err != nil {
		return "", 0, "", err
	}
	if err := os.Rename(tmpDir, target); err != nil {
		return "", 0, "", err
	}
	return relDir, size, hex.EncodeToString(digest.Sum(nil)), nil
}

// readMonitorArtifactFiles loads a file-backed artifact, returning false when
// the directory has no readable payload (missing/deleted files).
func (s *GatewayStore) readMonitorArtifactFiles(relDir string) (monitorArtifactContent, bool) {
	var content monitorArtifactContent
	root := s.monitorArtifactRoot()
	if root == "" {
		return content, false
	}
	clean, ok := cleanMonitorArtifactRelativeDir(relDir)
	if !ok {
		return content, false
	}
	dir := filepath.Join(root, filepath.FromSlash(clean))
	found := false
	if body, err := os.ReadFile(filepath.Join(dir, monitorArtifactMarkdownName)); err == nil {
		content.Markdown = string(body)
		found = true
	}
	if body, err := os.ReadFile(filepath.Join(dir, monitorArtifactJSONName)); err == nil {
		if compact, valid := compactValidJSON(string(body)); valid {
			content.JSON = compact
			found = true
		}
	}
	if body, err := os.ReadFile(filepath.Join(dir, monitorArtifactRawName)); err == nil {
		content.Raw = string(body)
		found = true
	}
	return content, found
}

// removeMonitorArtifactDir deletes one item directory plus its now-empty run
// and date parents.
func (s *GatewayStore) removeMonitorArtifactDir(relDir string) {
	_ = s.removeMonitorArtifactDirErr(relDir)
}

// removeMonitorArtifactDirErr is the retention path: it reports why a payload
// directory survived so the scan can log it instead of failing silently.
func (s *GatewayStore) removeMonitorArtifactDirErr(relDir string) error {
	root := s.monitorArtifactRoot()
	if root == "" {
		return errMonitorArtifactStorageDisabled
	}
	clean, ok := cleanMonitorArtifactRelativeDir(relDir)
	if !ok {
		return errors.New("invalid monitor artifact directory")
	}
	dir := filepath.Join(root, filepath.FromSlash(clean))
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// os.Remove only succeeds for empty directories, so shared run/date dirs
	// are kept while any sibling item still exists.
	_ = os.Remove(filepath.Dir(dir))
	_ = os.Remove(filepath.Dir(filepath.Dir(dir)))
	return nil
}
