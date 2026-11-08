package server

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The version journal is the per-database change history of direct DoSql. Every
// real apply takes the next version number BEFORE it touches the database
// ("begin") and records its outcome afterwards ("end"). A begin without an end
// is a crash or a lost record and is reported as unfinished, never hidden.
// Entries are hash chained, so edits or deletions in the middle are detected.
const (
	doSQLJournalBegin     = "begin"
	doSQLJournalEnd       = "end"
	doSQLJournalStmtLimit = 2000
)

var doSQLJournalNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

type doSQLVersionEntry struct {
	Version         int64    `json:"version"`
	Event           string   `json:"event"`
	OperationID     string   `json:"operation_id"`
	Credential      string   `json:"credential"`
	Fingerprint     string   `json:"statement_fingerprint"`
	Statements      []string `json:"statements,omitempty"`
	Status          string   `json:"status,omitempty"`
	Committed       bool     `json:"committed,omitempty"`
	ImplicitCommits int      `json:"implicit_commits,omitempty"`
	Error           string   `json:"error,omitempty"`
	RecordedAt      string   `json:"recorded_at"`
	PrevHash        string   `json:"prev_hash"`
	Hash            string   `json:"hash"`
}

type doSQLVersionSummary struct {
	Version     int64
	OperationID string
	Status      string // outcome status, or "unfinished" when no end was recorded
	Committed   bool
}

func doSQLVersionJournalPath(target doSQLTarget) (string, error) {
	engine := strings.ToLower(strings.TrimSpace(target.Engine))
	if engine == "" || target.Address == "" || target.Database == "" {
		return "", errors.New("dosql_version_identity_required: engine, endpoint and database are required")
	}
	endpoints := strings.Split(target.Address, ",")
	for i, endpoint := range endpoints {
		host, port, err := net.SplitHostPort(endpoint)
		number, portErr := strconv.Atoi(port)
		if err != nil || host == "" || portErr != nil || number < 1 || number > 65535 {
			return "", errors.New("dosql_version_identity_required: endpoint must include an explicit numeric port; external drivers must declare it in connection_uri")
		}
		host = strings.ToLower(host)
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
		endpoints[i] = net.JoinHostPort(host, strconv.Itoa(number))
	}
	sort.Strings(endpoints)
	address := strings.Join(endpoints, ",")
	// Address is the credential-free endpoint from the connection parser. Never
	// hash a DSN, username, credential ID or version: rotations keep this identity.
	identity, _ := json.Marshal([]string{engine, address, target.Database})
	sum := sha256.Sum256(identity)
	root := filepath.Join(filepath.Dir(workspaceRoot()), ".doops-dosql-versions")
	path := filepath.Join(root, "v2", hex.EncodeToString(sum[:])+".jsonl")
	legacyName := doSQLJournalNameUnsafe.ReplaceAllString(engine+"__"+target.Database, "_")
	legacyPath := filepath.Join(root, legacyName+".jsonl")
	if _, err := os.Lstat(legacyPath); err == nil {
		return "", fmt.Errorf("dosql_version_journal_migration_required: %q has no endpoint identity; stop SQL writes and back it up, verify that every entry belongs to this endpoint before moving it unchanged to %q (never overwrite an existing journal); if ownership is unknown or mixed, archive it separately and explicitly start a new history", legacyPath, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("dosql_version_journal_unavailable")
	}
	return path, nil
}

func doSQLEntryHash(entry doSQLVersionEntry) string {
	entry.Hash = ""
	encoded, _ := json.Marshal(entry)
	sum := sha256.Sum256(append([]byte(entry.PrevHash+"\n"), encoded...))
	return hex.EncodeToString(sum[:])
}

// withDoSQLJournal holds an exclusive file lock for the whole read-verify-append
// cycle, so concurrent applies on one database get distinct, ordered versions.
func withDoSQLJournal(path string, fn func(file *os.File, entries []doSQLVersionEntry) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("dosql_version_journal_unavailable")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return errors.New("dosql_version_journal_unavailable")
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return errors.New("dosql_version_journal_unavailable")
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
	entries, err := readDoSQLJournal(file)
	if err != nil {
		return err
	}
	return fn(file, entries)
}

func readDoSQLJournal(file *os.File) ([]doSQLVersionEntry, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return nil, errors.New("dosql_version_journal_unavailable")
	}
	var entries []doSQLVersionEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	prev := ""
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry doSQLVersionEntry
		if err := json.Unmarshal(line, &entry); err != nil || entry.PrevHash != prev || entry.Hash != doSQLEntryHash(entry) {
			return nil, fmt.Errorf("dosql_version_journal_corrupt: entry %d breaks the hash chain", len(entries)+1)
		}
		prev = entry.Hash
		entries = append(entries, entry)
	}
	if scanner.Err() != nil {
		return nil, errors.New("dosql_version_journal_corrupt: unreadable entry")
	}
	return entries, nil
}

func appendDoSQLJournal(file *os.File, entries []doSQLVersionEntry, entry doSQLVersionEntry) error {
	if len(entries) > 0 {
		entry.PrevHash = entries[len(entries)-1].Hash
	}
	entry.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	entry.Hash = doSQLEntryHash(entry)
	encoded, err := json.Marshal(entry)
	if err != nil {
		return errors.New("dosql_version_journal_unavailable")
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return errors.New("dosql_version_journal_unavailable")
	}
	return file.Sync()
}

func doSQLJournalStatements(statements []string) []string {
	out := make([]string, 0, len(statements))
	for _, statement := range statements {
		statement = sanitizeSecretText(statement)
		if len(statement) > doSQLJournalStmtLimit {
			statement = statement[:doSQLJournalStmtLimit] + "...[truncated]"
		}
		out = append(out, statement)
	}
	return out
}

// beginDoSQLVersion reserves the next version for a real apply. The caller must
// not execute when this fails: an unrecorded change is not allowed.
func beginDoSQLVersion(path, operationID, credential, fingerprint string, statements []string) (int64, error) {
	var version int64
	err := withDoSQLJournal(path, func(file *os.File, entries []doSQLVersionEntry) error {
		for _, entry := range entries {
			if entry.Version > version {
				version = entry.Version
			}
		}
		version++
		return appendDoSQLJournal(file, entries, doSQLVersionEntry{
			Version: version, Event: doSQLJournalBegin, OperationID: operationID, Credential: credential,
			Fingerprint: fingerprint, Statements: doSQLJournalStatements(statements),
		})
	})
	return version, err
}

func endDoSQLVersion(path string, version int64, operationID, credential, fingerprint string, result doSQLDirectResult, execErr error) error {
	entry := doSQLVersionEntry{
		Version: version, Event: doSQLJournalEnd, OperationID: operationID, Credential: credential,
		Fingerprint: fingerprint, Status: result.Status, Committed: result.Committed, ImplicitCommits: result.ImplicitCommits,
	}
	if execErr != nil {
		entry.Status = "failed"
		entry.Error = sanitizeSecretText(execErr.Error())
		if len(entry.Error) > doSQLJournalStmtLimit {
			entry.Error = entry.Error[:doSQLJournalStmtLimit]
		}
	}
	return withDoSQLJournal(path, func(file *os.File, entries []doSQLVersionEntry) error {
		return appendDoSQLJournal(file, entries, entry)
	})
}

// listDoSQLVersions verifies the chain and returns one summary per version.
func listDoSQLVersions(path string) ([]doSQLVersionSummary, error) {
	var summaries []doSQLVersionSummary
	err := withDoSQLJournal(path, func(_ *os.File, entries []doSQLVersionEntry) error {
		index := map[int64]int{}
		for _, entry := range entries {
			position, seen := index[entry.Version]
			if !seen {
				index[entry.Version] = len(summaries)
				summaries = append(summaries, doSQLVersionSummary{Version: entry.Version, OperationID: entry.OperationID, Status: "unfinished"})
				position = index[entry.Version]
			}
			if entry.Event == doSQLJournalEnd {
				summaries[position].Status = entry.Status
				summaries[position].Committed = entry.Committed
			}
		}
		return nil
	})
	return summaries, err
}

func doSQLVersionUnfinished(summaries []doSQLVersionSummary) []int64 {
	var versions []int64
	for _, summary := range summaries {
		if strings.EqualFold(summary.Status, "unfinished") {
			versions = append(versions, summary.Version)
		}
	}
	return versions
}
