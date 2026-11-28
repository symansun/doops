package recorder

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// StatementRecord captures every statement executed through the unified DoSql
// channel so development changes can be audited and packed into versioned files.
type StatementRecord struct {
	Seq          int64  `json:"seq"`
	Timestamp    string `json:"timestamp"`
	SessionID    string `json:"session_id,omitempty"`
	OperationID  string `json:"operation_id"`
	Version      int64  `json:"version,omitempty"`
	Engine       string `json:"engine"`
	Database     string `json:"database"`
	Credential   string `json:"credential,omitempty"`
	ReleaseTag   string `json:"release_tag,omitempty"`
	Mode         string `json:"mode"`
	Kind         string `json:"kind"`
	SegmentIndex int    `json:"segment_index,omitempty"`
	Atomic       bool   `json:"atomic,omitempty"`
	Statement    string `json:"statement"`
	StmtHash     string `json:"stmt_hash"`
	AffectedRows int64  `json:"affected_rows,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	PrePosition  string `json:"pre_position,omitempty"`
	PostPosition string `json:"post_position,omitempty"`
}

type QueryFilter struct {
	SessionID     string
	Database      string
	OnlyMutations bool
	OnlySucceeded bool
	Limit         int
}

// Store persists append-only per-database statement history under the Agent's
// persistent state root with advisory file locking.
type Store struct {
	Path string
}

// CanonicalAddress normalizes single or comma-separated host:port endpoints
// (lowercased host, canonical IP form, sorted order) so version journals,
// statement history, and SchemaIR snapshots hash identical database identities.
func CanonicalAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	normalized := make([]string, 0, len(parts))
	for _, ep := range parts {
		ep = strings.TrimSpace(ep)
		if ep == "" {
			continue
		}
		if host, port, err := net.SplitHostPort(ep); err == nil && host != "" {
			if num, pErr := strconv.Atoi(port); pErr == nil && num >= 1 && num <= 65535 {
				host = strings.ToLower(host)
				if ip := net.ParseIP(host); ip != nil {
					host = ip.String()
				}
				normalized = append(normalized, net.JoinHostPort(host, strconv.Itoa(num)))
				continue
			}
		}
		normalized = append(normalized, strings.ToLower(ep))
	}
	sort.Strings(normalized)
	return strings.Join(normalized, ",")
}

func NewStore(stateRoot, engine, address, database string) *Store {
	identity, _ := json.Marshal([]string{
		strings.ToLower(strings.TrimSpace(engine)),
		CanonicalAddress(address),
		strings.TrimSpace(database),
	})
	sum := sha256.Sum256(identity)
	return &Store{
		Path: filepath.Join(stateRoot, "history", hex.EncodeToString(sum[:])+".jsonl"),
	}
}

func (s *Store) Append(records ...StatementRecord) error {
	if s == nil || len(records) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)

	lastSeq, err := lastSequence(file)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, rec := range records {
		lastSeq++
		rec.Seq = lastSeq
		if rec.Timestamp == "" {
			rec.Timestamp = now
		}
		if rec.StmtHash == "" {
			sum := sha256.Sum256([]byte(rec.Statement))
			rec.StmtHash = hex.EncodeToString(sum[:])
		}
		encoded, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(encoded, '\n')); err != nil {
			return err
		}
	}
	return file.Sync()
}

func (s *Store) List(filter QueryFilter) ([]StatementRecord, error) {
	if s == nil {
		return nil, nil
	}
	file, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []StatementRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH); err != nil {
		return nil, err
	}
	defer unix.Flock(int(file.Fd()), unix.LOCK_UN)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var out []StatementRecord
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec StatementRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if filter.SessionID != "" && rec.SessionID != filter.SessionID {
			continue
		}
		if filter.Database != "" && rec.Database != filter.Database {
			continue
		}
		if filter.OnlyMutations && rec.Kind == "read" {
			continue
		}
		if filter.OnlySucceeded && rec.Status != "succeeded" {
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[len(out)-filter.Limit:]
	}
	return out, nil
}

func lastSequence(file *os.File) (int64, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return 0, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	var last int64
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var head struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(line, &head) == nil && head.Seq > last {
			last = head.Seq
		}
	}
	return last, scanner.Err()
}
