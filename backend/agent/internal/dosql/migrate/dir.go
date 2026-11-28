package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/user/doops/agent/internal/dosql/recorder"
)

const SumFileName = "dosql.sum"

// MigrationFile represents one numbered migration script (Flyway/Atlas style):
// V001__add_order_status.sql or V002__backfill_tags.mongo.json
type MigrationFile struct {
	Version     int64  `json:"version"`
	VersionTag  string `json:"version_tag"`
	Description string `json:"description"`
	EngineHint  string `json:"engine_hint"` // "sql" | "mongodb"
	FileName    string `json:"file_name"`
	Checksum    string `json:"checksum"`
	Content     string `json:"content,omitempty"`
}

var migrationFileRe = regexp.MustCompile(`^V(\d+)__([A-Za-z0-9._-]+?)\.(sql|mongo\.json)$`)

// LoadDir scans a migration directory, sorts files by numeric version, validates
// uniqueness, and verifies dosql.sum when present (preventing merge conflicts or
// silent tampering with already-released migrations).
func LoadDir(dir string) ([]MigrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("dosql_migrate_dir_unreadable: %w", err)
	}
	var files []MigrationFile
	seenVersion := map[int64]string{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == SumFileName {
			continue
		}
		m := migrationFileRe.FindStringSubmatch(entry.Name())
		if len(m) != 4 {
			continue
		}
		ver, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || ver <= 0 {
			return nil, fmt.Errorf("dosql_migrate_version_invalid: %s", entry.Name())
		}
		if prev, dup := seenVersion[ver]; dup {
			return nil, fmt.Errorf("dosql_migrate_version_duplicate: %s and %s share version %d", prev, entry.Name(), ver)
		}
		seenVersion[ver] = entry.Name()
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		engineHint := "sql"
		if m[3] == "mongo.json" {
			engineHint = "mongodb"
		}
		sum := sha256.Sum256(raw)
		files = append(files, MigrationFile{
			Version:     ver,
			VersionTag:  "V" + m[1],
			Description: m[2],
			EngineHint:  engineHint,
			FileName:    entry.Name(),
			Checksum:    "sha256:" + hex.EncodeToString(sum[:]),
			Content:     string(raw),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	if err := VerifySumFile(dir, files); err != nil {
		return nil, err
	}
	return files, nil
}

// WriteSumFile writes the Atlas-style dosql.sum integrity manifest atomically.
func WriteSumFile(dir string, files []MigrationFile) error {
	var b strings.Builder
	h := sha256.New()
	for _, f := range files {
		line := fmt.Sprintf("%s %s\n", f.FileName, f.Checksum)
		h.Write([]byte(line))
		b.WriteString(line)
	}
	header := fmt.Sprintf("h1:%s\n", hex.EncodeToString(h.Sum(nil)))
	return writeFileAtomic(filepath.Join(dir, SumFileName), []byte(header+b.String()), 0644)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-dosql-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// VerifySumFile verifies dosql.sum if it exists in dir.
func VerifySumFile(dir string, files []MigrationFile) error {
	raw, err := os.ReadFile(filepath.Join(dir, SumFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var expected strings.Builder
	h := sha256.New()
	for _, f := range files {
		line := fmt.Sprintf("%s %s\n", f.FileName, f.Checksum)
		h.Write([]byte(line))
		expected.WriteString(line)
	}
	want := fmt.Sprintf("h1:%s\n%s", hex.EncodeToString(h.Sum(nil)), expected.String())
	if strings.TrimSpace(string(raw)) != strings.TrimSpace(want) {
		return errors.New("dosql_migrate_sum_mismatch: migration directory does not match dosql.sum; re-verify or re-pack")
	}
	return nil
}

// PackFromHistory turns automatically recorded, succeeded mutating statements
// from a session into the next numbered migration file (V00N__desc.sql or .mongo.json)
// and updates dosql.sum. Deduplicates identical consecutive statements.
func PackFromHistory(dir, description, engine string, records []recorder.StatementRecord) (*MigrationFile, error) {
	desc := sanitizeSlug(description)
	if desc == "" {
		return nil, errors.New("dosql_pack_description_required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	existing, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	var nextVer int64 = 1
	if len(existing) > 0 {
		nextVer = existing[len(existing)-1].Version + 1
	}

	var stmts []string
	lastHash := ""
	for _, rec := range records {
		if !isPackableHistoryRecord(rec) {
			continue
		}
		s := strings.TrimSpace(rec.Statement)
		if s == "" || (rec.StmtHash != "" && rec.StmtHash == lastHash) {
			continue
		}
		lastHash = rec.StmtHash
		stmts = append(stmts, s)
	}
	if len(stmts) == 0 {
		return nil, errors.New("dosql_pack_no_mutations: no succeeded mutating statements found in history")
	}

	ext := "sql"
	engineHint := "sql"
	var content strings.Builder
	if strings.EqualFold(engine, "mongodb") {
		ext = "mongo.json"
		engineHint = "mongodb"
		content.WriteString("[\n  ")
		content.WriteString(strings.Join(stmts, ",\n  "))
		content.WriteString("\n]\n")
	} else {
		content.WriteString(fmt.Sprintf("-- Packed by DoSql (version V%03d, %d statements)\n", nextVer, len(stmts)))
		for _, s := range stmts {
			if !strings.HasSuffix(s, ";") {
				s += ";"
			}
			content.WriteString(s + "\n")
		}
	}

	fileName := fmt.Sprintf("V%03d__%s.%s", nextVer, desc, ext)
	fullPath := filepath.Join(dir, fileName)
	raw := []byte(content.String())
	if err := writeFileAtomic(fullPath, raw, 0644); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	packed := MigrationFile{
		Version:     nextVer,
		VersionTag:  fmt.Sprintf("V%03d", nextVer),
		Description: desc,
		EngineHint:  engineHint,
		FileName:    fileName,
		Checksum:    "sha256:" + hex.EncodeToString(sum[:]),
		Content:     string(raw),
	}
	all := append(existing, packed)
	if err := WriteSumFile(dir, all); err != nil {
		return nil, err
	}
	return &packed, nil
}

// isPackableHistoryRecord accepts only committed mutating apply statements,
// excluding reads, dry-run rollbacks, backfill audit entries, and prior migrate replays.
func isPackableHistoryRecord(rec recorder.StatementRecord) bool {
	if rec.Status != "succeeded" {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(rec.Kind))
	if kind == "read" || kind == "backfill" {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(rec.Mode))
	if mode != "" && mode != "apply" {
		return false
	}
	return true
}

func sanitizeSlug(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_-")
}
