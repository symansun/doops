package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const HistoryTableName = "_dosql_schema_history"

// Revision mirrors Atlas's production revision model: tracks exact Applied/Total
// statement progress so MySQL implicit-commit failures can be resumed accurately.
type Revision struct {
	Version     int64  `json:"version"`
	Description string `json:"description"`
	Type        string `json:"type"` // "execute" | "baseline"
	Checksum    string `json:"checksum"`
	ReleaseTag  string `json:"release_tag,omitempty"`
	Applied     int    `json:"applied"`
	Total       int    `json:"total"`
	Status      string `json:"status"` // "running" | "succeeded" | "failed"
	Error       string `json:"error,omitempty"`
	AppliedAt   string `json:"applied_at"`
	DurationMS  int64  `json:"duration_ms"`
}

type StatusReport struct {
	CurrentVersion int64           `json:"current_version"`
	Applied        []Revision      `json:"applied"`
	Pending        []MigrationFile `json:"pending"`
	Dirty          *Revision       `json:"dirty,omitempty"`
}

type MigrateResult struct {
	BaselineSet int64      `json:"baseline_set,omitempty"`
	Executed    []Revision `json:"executed"`
	Skipped     int        `json:"skipped"`
	Current     int64      `json:"current_version"`
}

// SQLHistoryStore manages the in-database _dosql_schema_history table (Goose + Atlas style).
type SQLHistoryStore struct {
	DB     *sql.DB
	Engine string
}

func (s *SQLHistoryStore) EnsureTable(ctx context.Context) error {
	var ddl string
	switch s.Engine {
	case "mysql":
		ddl = `CREATE TABLE IF NOT EXISTS _dosql_schema_history (
			version BIGINT NOT NULL PRIMARY KEY,
			description VARCHAR(255) NOT NULL,
			type VARCHAR(32) NOT NULL,
			checksum VARCHAR(128) NOT NULL,
			release_tag VARCHAR(128) NOT NULL DEFAULT '',
			applied INT NOT NULL DEFAULT 0,
			total INT NOT NULL DEFAULT 0,
			status VARCHAR(32) NOT NULL,
			error_text TEXT,
			applied_at VARCHAR(64) NOT NULL,
			duration_ms BIGINT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	default: // postgres, sqlite
		ddl = `CREATE TABLE IF NOT EXISTS _dosql_schema_history (
			version BIGINT NOT NULL PRIMARY KEY,
			description TEXT NOT NULL,
			type TEXT NOT NULL,
			checksum TEXT NOT NULL,
			release_tag TEXT NOT NULL DEFAULT '',
			applied INTEGER NOT NULL DEFAULT 0,
			total INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			error_text TEXT NOT NULL DEFAULT '',
			applied_at TEXT NOT NULL,
			duration_ms BIGINT NOT NULL DEFAULT 0
		)`
	}
	_, err := s.DB.ExecContext(ctx, ddl)
	return err
}

// TableExists checks whether _dosql_schema_history exists using a strictly
// read-only catalog query (no DDL, safe on read-only credentials and replicas).
func (s *SQLHistoryStore) TableExists(ctx context.Context) (bool, error) {
	switch s.Engine {
	case "mysql":
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, HistoryTableName).Scan(&count); err != nil {
			return false, err
		}
		return count > 0, nil
	case "postgres":
		var exists bool
		if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`, HistoryTableName).Scan(&exists); err != nil {
			return false, err
		}
		return exists, nil
	default: // sqlite
		var count int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, HistoryTableName).Scan(&count); err != nil {
			return false, err
		}
		return count > 0, nil
	}
}

// ListRevisions reads applied migration revisions without creating or mutating
// _dosql_schema_history. When the table does not exist yet it returns nil, nil.
func (s *SQLHistoryStore) ListRevisions(ctx context.Context) ([]Revision, error) {
	exists, err := s.TableExists(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT version, description, type, checksum, release_tag, applied, total, status, COALESCE(error_text,''), applied_at, duration_ms FROM _dosql_schema_history ORDER BY version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.Version, &r.Description, &r.Type, &r.Checksum, &r.ReleaseTag, &r.Applied, &r.Total, &r.Status, &r.Error, &r.AppliedAt, &r.DurationMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLHistoryStore) UpsertRevision(ctx context.Context, r Revision) error {
	if r.AppliedAt == "" {
		r.AppliedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if s.Engine == "mysql" {
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO _dosql_schema_history (version, description, type, checksum, release_tag, applied, total, status, error_text, applied_at, duration_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE applied=VALUES(applied), total=VALUES(total), status=VALUES(status), error_text=VALUES(error_text), applied_at=VALUES(applied_at), duration_ms=VALUES(duration_ms)`,
			r.Version, r.Description, r.Type, r.Checksum, r.ReleaseTag, r.Applied, r.Total, r.Status, r.Error, r.AppliedAt, r.DurationMS)
		return err
	}
	if s.Engine == "postgres" {
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO _dosql_schema_history (version, description, type, checksum, release_tag, applied, total, status, error_text, applied_at, duration_ms)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (version) DO UPDATE SET applied=EXCLUDED.applied, total=EXCLUDED.total, status=EXCLUDED.status, error_text=EXCLUDED.error_text, applied_at=EXCLUDED.applied_at, duration_ms=EXCLUDED.duration_ms`,
			r.Version, r.Description, r.Type, r.Checksum, r.ReleaseTag, r.Applied, r.Total, r.Status, r.Error, r.AppliedAt, r.DurationMS)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO _dosql_schema_history (version, description, type, checksum, release_tag, applied, total, status, error_text, applied_at, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (version) DO UPDATE SET applied=excluded.applied, total=excluded.total, status=excluded.status, error_text=excluded.error_text, applied_at=excluded.applied_at, duration_ms=excluded.duration_ms`,
		r.Version, r.Description, r.Type, r.Checksum, r.ReleaseTag, r.Applied, r.Total, r.Status, r.Error, r.AppliedAt, r.DurationMS)
	return err
}

// ComputeStatus compares database revisions against local migration files,
// detecting checksum tampering, dirty/unfinished revisions, and pending files.
func ComputeStatus(applied []Revision, files []MigrationFile) (*StatusReport, error) {
	rep := &StatusReport{Applied: applied}
	byVer := map[int64]Revision{}
	var baselineVersion int64
	for _, r := range applied {
		byVer[r.Version] = r
		if r.Version > rep.CurrentVersion {
			rep.CurrentVersion = r.Version
		}
		if r.Type == "baseline" && r.Version > baselineVersion {
			baselineVersion = r.Version
		}
		if r.Status != "succeeded" {
			copyRev := r
			rep.Dirty = &copyRev
		}
	}
	for _, f := range files {
		if prev, ok := byVer[f.Version]; ok {
			if prev.Type == "execute" && prev.Checksum != "" && prev.Checksum != f.Checksum {
				return nil, fmt.Errorf("dosql_migrate_checksum_mismatch: version %d (%s) was modified after execution (db=%s, file=%s)", f.Version, f.FileName, prev.Checksum, f.Checksum)
			}
			continue
		}
		if baselineVersion > 0 && f.Version <= baselineVersion {
			// Versions <= baseline remain absorbed by the baseline even after
			// subsequent migrations advance CurrentVersion.
			continue
		}
		rep.Pending = append(rep.Pending, f)
	}
	return rep, nil
}

// ExecFunc executes one migration file's content (starting at startStmtIndex for --resume)
// and returns (appliedStatements, totalStatements, error).
type ExecFunc func(ctx context.Context, file MigrationFile, startStmtIndex int) (int, int, error)

// Apply runs pending migrations in strict version order, supporting --baseline
// (adopting existing legacy databases) and --resume (continuing after partial DDL).
func Apply(ctx context.Context, store *SQLHistoryStore, files []MigrationFile, baselineVersion int64, releaseTag string, resume bool, execFn ExecFunc) (*MigrateResult, error) {
	if err := store.EnsureTable(ctx); err != nil {
		return nil, err
	}
	applied, err := store.ListRevisions(ctx)
	if err != nil {
		return nil, err
	}
	res := &MigrateResult{}

	// Baseline onboarding for existing databases with no history yet.
	if baselineVersion > 0 {
		if len(applied) > 0 {
			return nil, errors.New("dosql_migrate_baseline_not_empty: history table already has revisions")
		}
		base := Revision{
			Version:     baselineVersion,
			Description: "baseline",
			Type:        "baseline",
			Checksum:    "baseline",
			ReleaseTag:  releaseTag,
			Status:      "succeeded",
		}
		if err := store.UpsertRevision(ctx, base); err != nil {
			return nil, err
		}
		res.BaselineSet = baselineVersion
		res.Current = baselineVersion
		applied = append(applied, base)
	}

	status, err := ComputeStatus(applied, files)
	if err != nil {
		return nil, err
	}
	res.Current = status.CurrentVersion
	res.Skipped = len(files) - len(status.Pending)

	if status.Dirty != nil {
		if !resume {
			return res, fmt.Errorf("dosql_migrate_dirty_version: version %d failed at statement %d/%d (%s); fix the issue and rerun with --resume", status.Dirty.Version, status.Dirty.Applied, status.Dirty.Total, status.Dirty.Error)
		}
		// Find matching file for dirty version and resume from Applied statement index.
		var dirtyFile *MigrationFile
		for i := range files {
			if files[i].Version == status.Dirty.Version {
				dirtyFile = &files[i]
				break
			}
		}
		if dirtyFile == nil {
			return res, fmt.Errorf("dosql_migrate_dirty_file_missing: cannot resume version %d", status.Dirty.Version)
		}
		started := time.Now()
		done, total, execErr := execFn(ctx, *dirtyFile, status.Dirty.Applied)
		rev := *status.Dirty
		rev.Applied = status.Dirty.Applied + done
		rev.Total = total
		rev.DurationMS = time.Since(started).Milliseconds()
		rev.ReleaseTag = releaseTag
		if execErr != nil {
			rev.Status = "failed"
			rev.Error = execErr.Error()
			_ = store.UpsertRevision(ctx, rev)
			return res, execErr
		}
		rev.Status = "succeeded"
		rev.Error = ""
		if err := store.UpsertRevision(ctx, rev); err != nil {
			return res, err
		}
		res.Executed = append(res.Executed, rev)
	}

	for _, f := range status.Pending {
		started := time.Now()
		rev := Revision{
			Version:     f.Version,
			Description: f.Description,
			Type:        "execute",
			Checksum:    f.Checksum,
			ReleaseTag:  releaseTag,
			Status:      "running",
		}
		if err := store.UpsertRevision(ctx, rev); err != nil {
			return res, err
		}
		done, total, execErr := execFn(ctx, f, 0)
		rev.Applied = done
		rev.Total = total
		rev.DurationMS = time.Since(started).Milliseconds()
		if execErr != nil {
			rev.Status = "failed"
			rev.Error = execErr.Error()
			_ = store.UpsertRevision(ctx, rev)
			return res, fmt.Errorf("dosql_migrate_failed: version %d (%s) failed at %d/%d: %w", f.Version, f.FileName, done, total, execErr)
		}
		rev.Status = "succeeded"
		if err := store.UpsertRevision(ctx, rev); err != nil {
			return res, err
		}
		res.Executed = append(res.Executed, rev)
		res.Current = f.Version
	}
	return res, nil
}
