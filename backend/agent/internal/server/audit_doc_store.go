package server

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	auditStorageEnv       = "DOOPS_AUDIT_STORAGE"
	auditStorageModeDB    = "db"
	auditStorageModeFile  = "file"
	auditDocPointerPrefix = `{"$audit_doc":`
	auditDocMaxScanBytes  = 1 << 20 // 1 MiB per JSONL audit record line
)

var auditDocDateFilePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.jsonl$`)
var auditDocDateDirPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type auditDocPointer struct {
	Doc    string `json:"$audit_doc"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type auditDocRecord struct {
	ID              int64                 `json:"id"`
	UserID          string                `json:"user_id,omitempty"`
	TokenID         string                `json:"token_id,omitempty"`
	Cluster         string                `json:"cluster,omitempty"`
	Instance        string                `json:"instance,omitempty"`
	CredentialID    string                `json:"credential_id,omitempty"`
	VersionID       string                `json:"credential_version_id,omitempty"`
	GrantID         string                `json:"credential_grant_id,omitempty"`
	Scope           *CredentialGrantScope `json:"scope,omitempty"`
	ResourceVersion string                `json:"resource_version,omitempty"`
	Digest          string                `json:"digest,omitempty"`
	CorrelationID   string                `json:"correlation_id,omitempty"`
	Action          string                `json:"action"`
	Session         string                `json:"session,omitempty"`
	CommandSummary  string                `json:"command_summary,omitempty"`
	Status          string                `json:"status"`
	Error           string                `json:"error,omitempty"`
	Tail            string                `json:"tail,omitempty"`
	BytesIn         int64                 `json:"bytes_in,omitempty"`
	BytesOut        int64                 `json:"bytes_out,omitempty"`
	Model           string                `json:"model,omitempty"`
	InputTokens     int64                 `json:"input_tokens,omitempty"`
	OutputTokens    int64                 `json:"output_tokens,omitempty"`
	TotalTokens     int64                 `json:"total_tokens,omitempty"`
	StartedAtMs     int64                 `json:"started_at_ms"`
	StartedAt       string                `json:"started_at"`
	EndedAt         string                `json:"ended_at,omitempty"`
}

// AuditStorageMode reports whether audit payloads stay inline in the database
// ("db", the default) or are written File-First as documents under
// <dataDir>/audit/ ("file").
func (s *GatewayStore) AuditStorageMode() string {
	if s != nil && s.auditDocMode == auditStorageModeFile {
		return auditStorageModeFile
	}
	return auditStorageModeDB
}

// AuditDocDir returns the File-First audit document root (<dataDir>/audit)
// when DOOPS_AUDIT_STORAGE=file is active, or "" in db mode.
func (s *GatewayStore) AuditDocDir() string {
	if s == nil || s.auditDocMode != auditStorageModeFile {
		return ""
	}
	return s.auditDocRoot()
}

func (s *GatewayStore) auditDocStoreEnabled() bool {
	return s != nil && s.auditDocMode == auditStorageModeFile && s.auditDocRoot() != ""
}

func (s *GatewayStore) auditDocRoot() string {
	if s == nil || s.dbDir == "" || s.dbDir == "." {
		return ""
	}
	return filepath.Join(s.dbDir, "audit")
}

func gatewayAuditStorageMode() (string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv(auditStorageEnv))); mode {
	case "", auditStorageModeDB:
		return auditStorageModeDB, nil
	case auditStorageModeFile:
		return auditStorageModeFile, nil
	default:
		return "", fmt.Errorf("gateway_audit_storage_invalid: %s=%q is not supported (allowed: db, file)", auditStorageEnv, mode)
	}
}

// initAuditStorage validates DOOPS_AUDIT_STORAGE and, in file mode, ensures
// <dataDir>/audit/{docs,records} exist with mode 0700 and are writable. Fails
// closed on any invalid setting or unwritable directory.
func (s *GatewayStore) initAuditStorage() error {
	mode, err := gatewayAuditStorageMode()
	if err != nil {
		return err
	}
	s.auditDocMode = mode
	if mode != auditStorageModeFile {
		return nil
	}
	root := s.auditDocRoot()
	if root == "" {
		return errors.New("gateway_audit_doc_dir_unwritable: audit file storage requires a store data directory")
	}
	for _, sub := range []string{"docs", "records"} {
		dir := filepath.Join(root, sub)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("gateway_audit_doc_dir_unwritable: %s: %w", dir, err)
		}
		probe, err := os.CreateTemp(dir, ".doops-audit-write-probe-")
		if err != nil {
			return fmt.Errorf("gateway_audit_doc_dir_unwritable: %s: %w", dir, err)
		}
		probePath := probe.Name()
		closeErr := probe.Close()
		removeErr := os.Remove(probePath)
		if closeErr != nil {
			return fmt.Errorf("gateway_audit_doc_dir_unwritable: %s: %w", dir, closeErr)
		}
		if removeErr != nil {
			return fmt.Errorf("gateway_audit_doc_dir_unwritable: %s: %w", probePath, removeErr)
		}
	}
	s.auditInFlight = make(map[int64]AuditEvent)
	log.Printf("gateway audit storage: mode=file dir=%s", root)
	return nil
}

// auditActionExternalizesTail keeps small SQL-inspected control-plane tails
// (upgrade evidence checked by audit_purge.go and maintenance receipts) inline
// in audit_events.tail while externalizing all operational output tails.
func auditActionExternalizesTail(action GatewayAction) bool {
	switch string(action) {
	case string(ActionAgentUpgrade), "audit:purge", "audit:scrub":
		return false
	default:
		return true
	}
}

func cleanAuditDocRelPath(raw string) (string, bool) {
	rel := filepath.ToSlash(strings.TrimSpace(raw))
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") || strings.Contains(rel, "\x00") || filepath.IsAbs(raw) {
		return "", false
	}
	parts := strings.Split(rel, "/")
	if len(parts) != 2 {
		return "", false
	}
	if !auditDocDateDirPattern.MatchString(parts[0]) {
		return "", false
	}
	file := parts[1]
	if file == "" || file == "." || file == ".." || !strings.HasPrefix(file, "audit_") || !strings.HasSuffix(file, ".txt") {
		return "", false
	}
	return rel, true
}

func parseAuditDocPointer(raw string) (auditDocPointer, bool) {
	if !strings.HasPrefix(raw, auditDocPointerPrefix) {
		return auditDocPointer{}, false
	}
	var ptr auditDocPointer
	if err := json.Unmarshal([]byte(raw), &ptr); err != nil {
		return auditDocPointer{}, false
	}
	clean, ok := cleanAuditDocRelPath(ptr.Doc)
	if !ok {
		return auditDocPointer{}, false
	}
	ptr.Doc = clean
	return ptr, true
}

func (s *GatewayStore) writeAuditTailDoc(id int64, startedAt time.Time, tail string) (string, string, error) {
	root := s.auditDocRoot()
	if root == "" {
		return "", "", errors.New("gateway_audit_doc_dir_unwritable: missing audit document root")
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	dateDir := startedAt.UTC().Format("2006-01-02")
	relPath := fmt.Sprintf("%s/audit_%d.txt", dateDir, id)
	targetDir := filepath.Join(root, "docs", dateDir)
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", "", fmt.Errorf("audit_doc_mkdir_failed: %w", err)
	}
	targetPath := filepath.Join(targetDir, fmt.Sprintf("audit_%d.txt", id))
	tmp, err := os.CreateTemp(targetDir, ".audit-tail-*")
	if err != nil {
		return "", "", fmt.Errorf("audit_doc_temp_failed: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	data := []byte(tail)
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", "", fmt.Errorf("audit_doc_write_failed: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", "", fmt.Errorf("audit_doc_fsync_failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("audit_doc_close_failed: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("audit_doc_chmod_failed: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("audit_doc_rename_failed: %w", err)
	}
	sum := sha256.Sum256(data)
	ptrBytes, err := json.Marshal(auditDocPointer{
		Doc:    relPath,
		Bytes:  int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		_ = os.Remove(targetPath)
		return "", "", err
	}
	return string(ptrBytes), relPath, nil
}

func (s *GatewayStore) removeAuditTailDoc(relPath string) {
	root := s.auditDocRoot()
	if root == "" {
		return
	}
	clean, ok := cleanAuditDocRelPath(relPath)
	if !ok {
		return
	}
	full := filepath.Join(root, "docs", filepath.FromSlash(clean))
	_ = os.Remove(full)
	_ = os.Remove(filepath.Dir(full))
}

func (s *GatewayStore) hydrateAuditTail(raw string) string {
	ptr, ok := parseAuditDocPointer(raw)
	if !ok {
		return raw
	}
	root := s.auditDocRoot()
	if root != "" {
		full := filepath.Join(root, "docs", filepath.FromSlash(ptr.Doc))
		if data, err := os.ReadFile(full); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("[audit document archived: %s (%d bytes)]", ptr.Doc, ptr.Bytes)
}

func (s *GatewayStore) rememberInFlightAudit(ev AuditEvent) {
	if s == nil || !s.auditDocStoreEnabled() || ev.ID <= 0 {
		return
	}
	s.auditDocMu.Lock()
	defer s.auditDocMu.Unlock()
	if s.auditInFlight == nil {
		s.auditInFlight = make(map[int64]AuditEvent)
	}
	// Bound in-flight map against leaked entries if callers skip FinishAudit.
	if len(s.auditInFlight) >= 4096 {
		cutoff := time.Now().UTC().Add(-24 * time.Hour)
		for k, v := range s.auditInFlight {
			if v.StartedAt.Before(cutoff) {
				delete(s.auditInFlight, k)
			}
		}
	}
	s.auditInFlight[ev.ID] = ev
}

func (s *GatewayStore) takeInFlightAudit(id int64) (AuditEvent, bool) {
	if s == nil || id <= 0 {
		return AuditEvent{}, false
	}
	s.auditDocMu.Lock()
	defer s.auditDocMu.Unlock()
	ev, ok := s.auditInFlight[id]
	if ok {
		delete(s.auditInFlight, id)
	}
	return ev, ok
}

func (s *GatewayStore) loadAuditEventByID(id int64) (AuditEvent, error) {
	var ev AuditEvent
	var action, started, ended, scopeJSON string
	err := s.readDB().QueryRow(`SELECT id, user_id, token_id, cluster, instance, credential_id,
		credential_version_id, credential_grant_id, scope_json,
		resource_version, digest, correlation_id, action, session,
		command_summary, status, error, tail, bytes_in, bytes_out,
		model, input_tokens, output_tokens, total_tokens, started_at, ended_at
		FROM audit_events WHERE id = ?`, id).Scan(
		&ev.ID, &ev.UserID, &ev.TokenID, &ev.Cluster, &ev.Instance,
		&ev.CredentialID, &ev.VersionID, &ev.GrantID, &scopeJSON,
		&ev.ResourceVersion, &ev.Digest, &ev.CorrelationID,
		&action, &ev.Session, &ev.CommandSummary, &ev.Status, &ev.Error, &ev.Tail,
		&ev.BytesIn, &ev.BytesOut, &ev.Model, &ev.InputTokens, &ev.OutputTokens, &ev.TotalTokens,
		&started, &ended,
	)
	if err != nil {
		return AuditEvent{}, err
	}
	if scopeJSON != "" {
		var scope CredentialGrantScope
		if err := json.Unmarshal([]byte(scopeJSON), &scope); err == nil {
			ev.Scope = &scope
		}
	}
	ev.Action = GatewayAction(action)
	ev.StartedAt, _ = parseTime(started)
	if ended != "" {
		ev.EndedAt, _ = parseTime(ended)
	}
	return ev, nil
}

func auditEventToDocRecord(ev AuditEvent) auditDocRecord {
	started := ev.StartedAt.UTC()
	if started.IsZero() {
		started = time.Now().UTC()
	}
	endedStr := ""
	if !ev.EndedAt.IsZero() {
		endedStr = formatTime(ev.EndedAt.UTC())
	}
	return auditDocRecord{
		ID:              ev.ID,
		UserID:          ev.UserID,
		TokenID:         ev.TokenID,
		Cluster:         ev.Cluster,
		Instance:        ev.Instance,
		CredentialID:    ev.CredentialID,
		VersionID:       ev.VersionID,
		GrantID:         ev.GrantID,
		Scope:           ev.Scope,
		ResourceVersion: ev.ResourceVersion,
		Digest:          ev.Digest,
		CorrelationID:   ev.CorrelationID,
		Action:          string(ev.Action),
		Session:         ev.Session,
		CommandSummary:  ev.CommandSummary,
		Status:          ev.Status,
		Error:           ev.Error,
		Tail:            ev.Tail,
		BytesIn:         ev.BytesIn,
		BytesOut:        ev.BytesOut,
		Model:           ev.Model,
		InputTokens:     ev.InputTokens,
		OutputTokens:    ev.OutputTokens,
		TotalTokens:     ev.TotalTokens,
		StartedAtMs:     started.UnixMilli(),
		StartedAt:       formatTime(started),
		EndedAt:         endedStr,
	}
}

func docRecordToAuditEvent(rec auditDocRecord) AuditEvent {
	ev := AuditEvent{
		ID:              rec.ID,
		UserID:          rec.UserID,
		TokenID:         rec.TokenID,
		Cluster:         rec.Cluster,
		Instance:        rec.Instance,
		CredentialID:    rec.CredentialID,
		VersionID:       rec.VersionID,
		GrantID:         rec.GrantID,
		Scope:           rec.Scope,
		ResourceVersion: rec.ResourceVersion,
		Digest:          rec.Digest,
		CorrelationID:   rec.CorrelationID,
		Action:          GatewayAction(rec.Action),
		Session:         rec.Session,
		CommandSummary:  rec.CommandSummary,
		Status:          rec.Status,
		Error:           rec.Error,
		Tail:            rec.Tail,
		BytesIn:         rec.BytesIn,
		BytesOut:        rec.BytesOut,
		Model:           rec.Model,
		InputTokens:     rec.InputTokens,
		OutputTokens:    rec.OutputTokens,
		TotalTokens:     rec.TotalTokens,
	}
	if rec.StartedAt != "" {
		ev.StartedAt, _ = parseTime(rec.StartedAt)
	} else if rec.StartedAtMs > 0 {
		ev.StartedAt = time.UnixMilli(rec.StartedAtMs).UTC()
	}
	if rec.EndedAt != "" {
		ev.EndedAt, _ = parseTime(rec.EndedAt)
	}
	return ev
}

// appendAuditDocRecord appends a single completed audit document line to
// <dataDir>/audit/records/YYYY-MM-DD.jsonl (mode 0600).
func (s *GatewayStore) appendAuditDocRecord(ev AuditEvent) error {
	if !s.auditDocStoreEnabled() {
		return nil
	}
	rec := auditEventToDocRecord(ev)
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("audit_doc_marshal_failed: %w", err)
	}
	line = append(line, '\n')
	dateName := time.UnixMilli(rec.StartedAtMs).UTC().Format("2006-01-02") + ".jsonl"
	recordsDir := filepath.Join(s.auditDocRoot(), "records")
	target := filepath.Join(recordsDir, dateName)

	s.auditDocMu.Lock()
	defer s.auditDocMu.Unlock()
	if err := os.MkdirAll(recordsDir, 0700); err != nil {
		return fmt.Errorf("audit_doc_records_mkdir_failed: %w", err)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("audit_doc_records_open_failed: %w", err)
	}
	_, writeErr := f.Write(line)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("audit_doc_records_write_failed: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("audit_doc_records_close_failed: %w", closeErr)
	}
	return nil
}

// queryAuditDocRecords reads historical File-First audit documents under
// <dataDir>/audit/records/YYYY-MM-DD.jsonl in reverse chronological order so
// ListAuditFiltered seamlessly serves records purged from the hot DB index.
func (s *GatewayStore) queryAuditDocRecords(filter AuditFilter, limit int, seenIDs map[int64]bool, ownedCreds map[string]bool) ([]AuditEvent, error) {
	if !s.auditDocStoreEnabled() || limit <= 0 {
		return nil, nil
	}
	recordsDir := filepath.Join(s.auditDocRoot(), "records")
	entries, err := os.ReadDir(recordsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	sinceDate := ""
	if !filter.Since.IsZero() {
		sinceDate = filter.Since.UTC().Format("2006-01-02")
	}
	untilDate := ""
	if !filter.Until.IsZero() {
		untilDate = filter.Until.UTC().Format("2006-01-02")
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !auditDocDateFilePattern.MatchString(name) {
			continue
		}
		datePart := strings.TrimSuffix(name, ".jsonl")
		if sinceDate != "" && datePart < sinceDate {
			continue
		}
		if untilDate != "" && datePart > untilDate {
			continue
		}
		files = append(files, name)
	}
	// Newest day first.
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var out []AuditEvent
	for _, name := range files {
		if len(out) >= limit {
			break
		}
		dayEvents, err := readAuditDocFileFiltered(filepath.Join(recordsDir, name), filter, seenIDs, ownedCreds)
		if err != nil {
			continue
		}
		// Within a day file, newest records are at the end; sort descending by ID.
		sort.Slice(dayEvents, func(i, j int) bool { return dayEvents[i].ID > dayEvents[j].ID })
		for _, ev := range dayEvents {
			if seenIDs[ev.ID] {
				continue
			}
			seenIDs[ev.ID] = true
			out = append(out, ev)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func readAuditDocFileFiltered(path string, filter AuditFilter, seenIDs map[int64]bool, ownedCreds map[string]bool) ([]AuditEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), auditDocMaxScanBytes)
	var matches []AuditEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec auditDocRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if seenIDs[rec.ID] {
			continue
		}
		if !auditDocMatchesFilter(rec, filter, ownedCreds) {
			continue
		}
		matches = append(matches, docRecordToAuditEvent(rec))
	}
	return matches, scanner.Err()
}

func auditDocMatchesFilter(rec auditDocRecord, filter AuditFilter, ownedCreds map[string]bool) bool {
	if filter.BeforeID > 0 && rec.ID >= filter.BeforeID {
		return false
	}
	if v := strings.TrimSpace(filter.CorrelationID); v != "" && rec.CorrelationID != v {
		return false
	}
	if !filter.Since.IsZero() && rec.StartedAtMs < filter.Since.UTC().UnixMilli() {
		return false
	}
	if !filter.Until.IsZero() && rec.StartedAtMs >= filter.Until.UTC().UnixMilli() {
		return false
	}
	if filter.HideUpgradeObserve && rec.Action == string(ActionAgentUpgrade) && rec.Status == "observing" {
		return false
	}
	if v := strings.TrimSpace(filter.UserID); v != "" && rec.UserID != v {
		return false
	}
	if v := strings.TrimSpace(filter.Cluster); v != "" && rec.Cluster != v {
		return false
	}
	if v := strings.TrimSpace(filter.Instance); v != "" && rec.Instance != v {
		return false
	}
	if v := strings.TrimSpace(filter.Session); v != "" && rec.Session != v {
		return false
	}
	if v := strings.TrimSpace(string(filter.Action)); v != "" && rec.Action != v {
		return false
	}
	if v := strings.TrimSpace(filter.Status); v != "" && rec.Status != v {
		return false
	}
	if v := strings.TrimSpace(filter.VisibleToUser); v != "" {
		if rec.UserID != v && !ownedCreds[rec.CredentialID] {
			return false
		}
	}
	return true
}

func (s *GatewayStore) loadOwnedCredentialIDs(ownerID string) map[string]bool {
	out := make(map[string]bool)
	rows, err := s.readDB().Query(`SELECT id FROM credentials WHERE owner_id = ?`, ownerID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out[id] = true
		}
	}
	return out
}

// readAuditDocDateFileIDs returns the set of audit event IDs already recorded
// in <dataDir>/audit/records/<date>.jsonl so backfill sync stays idempotent.
func (s *GatewayStore) readAuditDocDateFileIDs(date string) map[int64]bool {
	ids := make(map[int64]bool)
	root := s.auditDocRoot()
	if root == "" {
		return ids
	}
	f, err := os.Open(filepath.Join(root, "records", date+".jsonl"))
	if err != nil {
		return ids
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), auditDocMaxScanBytes)
	for scanner.Scan() {
		var probe struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &probe); err == nil && probe.ID > 0 {
			ids[probe.ID] = true
		}
	}
	return ids
}

// pruneExpiredAuditDocs removes local <dataDir>/audit/records/YYYY-MM-DD.jsonl
// files and <dataDir>/audit/docs/YYYY-MM-DD directories strictly older than
// keepDays days before nowUTC.
func (s *GatewayStore) pruneExpiredAuditDocs(keepDays int, nowUTC time.Time) (int, error) {
	if keepDays <= 0 || s.auditDocRoot() == "" {
		return 0, nil
	}
	cutoffDate := nowUTC.UTC().AddDate(0, 0, -keepDays).Format("2006-01-02")
	removed := 0

	recordsDir := filepath.Join(s.auditDocRoot(), "records")
	if entries, err := os.ReadDir(recordsDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !auditDocDateFilePattern.MatchString(name) {
				continue
			}
			if strings.TrimSuffix(name, ".jsonl") < cutoffDate {
				if err := os.Remove(filepath.Join(recordsDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return removed, err
				}
				removed++
			}
		}
	}

	docsDir := filepath.Join(s.auditDocRoot(), "docs")
	if entries, err := os.ReadDir(docsDir); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || !auditDocDateDirPattern.MatchString(name) {
				continue
			}
			if name < cutoffDate {
				if err := os.RemoveAll(filepath.Join(docsDir, name)); err != nil {
					return removed, err
				}
				removed++
			}
		}
	}
	return removed, nil
}
