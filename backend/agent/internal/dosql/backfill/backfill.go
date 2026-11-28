package backfill

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

// Options configures a chunked primary-key cursor backfill/data-cleanup job so
// large tables can be updated in small transactions without long locks or
// replication lag spikes.
type Options struct {
	Engine       string
	Table        string
	KeyColumn    string
	SetClause    string // e.g. "status_v2 = status"
	WhereClause  string // optional extra filter, e.g. "status_v2 IS NULL"
	BatchSize    int64  // default 1000
	Throttle     time.Duration
	StartAfterID int64 // optional resume cursor
	DryRun       bool
}

type Result struct {
	Table         string `json:"table"`
	KeyColumn     string `json:"key_column"`
	Batches       int    `json:"batches"`
	TotalAffected int64  `json:"total_affected"`
	LastKey       int64  `json:"last_key"`
	MinKey        int64  `json:"min_key"`
	MaxKey        int64  `json:"max_key"`
	DryRun        bool   `json:"dry_run,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
}

func RunIntKeyBackfill(ctx context.Context, db *sql.DB, opts Options) (*Result, error) {
	if opts.Table == "" || opts.KeyColumn == "" || strings.TrimSpace(opts.SetClause) == "" {
		return nil, fmt.Errorf("dosql_backfill_invalid: table, key_column, and set_clause are required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	started := time.Now()
	qBounds := fmt.Sprintf("SELECT MIN(%s), MAX(%s) FROM %s", quoteID(opts.Engine, opts.KeyColumn), quoteID(opts.Engine, opts.KeyColumn), quoteID(opts.Engine, opts.Table))
	var minK, maxK sql.NullInt64
	if err := db.QueryRowContext(ctx, qBounds).Scan(&minK, &maxK); err != nil {
		return nil, err
	}
	res := &Result{Table: opts.Table, KeyColumn: opts.KeyColumn, DryRun: opts.DryRun}
	if !minK.Valid || !maxK.Valid {
		res.DurationMS = time.Since(started).Milliseconds()
		return res, nil
	}
	res.MinKey = minK.Int64
	res.MaxKey = maxK.Int64

	var cursor int64
	hasLowerBound := false
	if opts.StartAfterID != 0 {
		cursor = opts.StartAfterID
		hasLowerBound = true
	} else if minK.Int64 > math.MinInt64 {
		cursor = minK.Int64 - 1
		hasLowerBound = true
	}

	p1, p2, p3 := "?", "?", "?"
	if opts.Engine == "postgres" {
		p1, p2, p3 = "$1", "$2", "$3"
	}
	qTable := quoteID(opts.Engine, opts.Table)
	qKey := quoteID(opts.Engine, opts.KeyColumn)
	extra := ""
	if strings.TrimSpace(opts.WhereClause) != "" {
		extra = " AND (" + strings.TrimSpace(opts.WhereClause) + ")"
	}
	// Resolve the upper key via keyset LIMIT so sparse/Snowflake integer keys
	// step by actual existing rows rather than spinning empty numeric ranges.
	batchUpperBoundedSQL := fmt.Sprintf(
		"SELECT MAX(_k) FROM (SELECT %s AS _k FROM %s WHERE %s > %s AND %s <= %s ORDER BY %s ASC LIMIT %s) AS _dosql_batch",
		qKey, qTable, qKey, p1, qKey, p2, qKey, p3,
	)
	batchUpperInitialSQL := fmt.Sprintf(
		"SELECT MAX(_k) FROM (SELECT %s AS _k FROM %s WHERE %s <= %s ORDER BY %s ASC LIMIT %s) AS _dosql_batch",
		qKey, qTable, qKey, p1, qKey, p2,
	)
	updateBoundedSQL := fmt.Sprintf("UPDATE %s SET %s WHERE %s > %s AND %s <= %s%s",
		qTable, strings.TrimSpace(opts.SetClause), qKey, p1, qKey, p2, extra)
	updateInitialSQL := fmt.Sprintf("UPDATE %s SET %s WHERE %s <= %s%s",
		qTable, strings.TrimSpace(opts.SetClause), qKey, p1, extra)

	for !hasLowerBound || cursor < maxK.Int64 {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		var upper sql.NullInt64
		var boundErr error
		if hasLowerBound {
			boundErr = db.QueryRowContext(ctx, batchUpperBoundedSQL, cursor, maxK.Int64, opts.BatchSize).Scan(&upper)
		} else {
			boundErr = db.QueryRowContext(ctx, batchUpperInitialSQL, maxK.Int64, opts.BatchSize).Scan(&upper)
		}
		if boundErr != nil {
			return res, fmt.Errorf("dosql_backfill_batch_bounds_failed: %w", boundErr)
		}
		if !upper.Valid {
			break
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return res, err
		}
		var outcome sql.Result
		if hasLowerBound {
			outcome, err = tx.ExecContext(ctx, updateBoundedSQL, cursor, upper.Int64)
		} else {
			outcome, err = tx.ExecContext(ctx, updateInitialSQL, upper.Int64)
		}
		if err != nil {
			_ = tx.Rollback()
			return res, fmt.Errorf("dosql_backfill_batch_failed (cursor=%d..%d): %w", cursor, upper.Int64, err)
		}
		affected, _ := outcome.RowsAffected()
		if opts.DryRun {
			_ = tx.Rollback()
		} else {
			if err := tx.Commit(); err != nil {
				return res, err
			}
		}
		res.Batches++
		res.TotalAffected += affected
		res.LastKey = upper.Int64
		cursor = upper.Int64
		hasLowerBound = true
		if opts.Throttle > 0 && cursor < maxK.Int64 {
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(opts.Throttle):
			}
		}
	}
	res.DurationMS = time.Since(started).Milliseconds()
	return res, nil
}

func quoteID(engine, name string) string {
	if engine == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
