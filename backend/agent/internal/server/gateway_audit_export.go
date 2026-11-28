package server

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	auditExportPageSize    = 2000
	platformEventBatchSize = 2000
)

// GatewayAuditExportOptions controls `doops-gateway audit export`.
type GatewayAuditExportOptions struct {
	DBPath                 string
	ConfirmDatabase        string
	OutputDir              string
	OutputPath             string
	Before                 *time.Time
	OlderThan              time.Duration
	All                    bool
	IncludeUnresolved      bool
	IncludePlatformEvents  bool
	COSPrefix              string
	Purge                  bool
	Reclaim                bool
	KeepLocal              int  // 0 keeps all; >0 keeps newest N bundles
	DeleteLocalAfterUpload bool // requires COSPrefix; deletes local bundle after verified COS upload
	KeepCOSDays            int  // 0 keeps all; >0 prunes COS objects under COSPrefix older than N days
	KeepDocDays            int  // 0 keeps all; >0 prunes local File-First audit docs/records older than N days
}

type auditExportManifest struct {
	Version               int                         `json:"version"`
	ExportedAt            string                      `json:"exported_at"`
	Backend               string                      `json:"backend"`
	CutoffBefore          string                      `json:"cutoff_before"`
	IncludeUnresolved     bool                        `json:"include_unresolved"`
	IncludePlatformEvents bool                        `json:"include_platform_events"`
	Audit                 auditExportSectionSummary   `json:"audit_events"`
	PlatformEvents        *platformEventExportSummary `json:"platform_events,omitempty"`
}

type auditExportSectionSummary struct {
	File        string `json:"file"`
	Rows        int64  `json:"rows"`
	MinID       int64  `json:"min_id"`
	MaxID       int64  `json:"max_id"`
	Bytes       int64  `json:"bytes"`
	SHA256      string `json:"sha256"`
	ResourceMap int64  `json:"resource_audit_rows"`
	COSKey      string `json:"cos_key,omitempty"`
}

type platformEventExportSummary struct {
	File          string `json:"file"`
	Rows          int64  `json:"rows"`
	MaxOccurredMs int64  `json:"max_occurred_at_ms"`
	MaxID         string `json:"max_id"`
	Bytes         int64  `json:"bytes"`
	SHA256        string `json:"sha256"`
	COSKey        string `json:"cos_key,omitempty"`
}

type auditExportPurgeReceiptTail struct {
	Before            string `json:"before"`
	Deleted           int64  `json:"deleted"`
	Batches           int    `json:"batches"`
	ArchiveSHA256     string `json:"archive_sha256,omitempty"`
	COSKey            string `json:"cos_key,omitempty"`
	IncludeUnresolved bool   `json:"include_unresolved,omitempty"`
}

// RunGatewayAuditExport exports eligible audit_events (and optionally terminal
// platform_events) into compressed JSONL archives, verifies integrity, uploads
// to COS when configured, and only then purges exported rows and reclaims space.
func RunGatewayAuditExport(ctx context.Context, opts GatewayAuditExportOptions, out io.Writer) (int, error) {
	if out == nil {
		out = io.Discard
	}
	nowUTC := time.Now().UTC()
	outputDir := strings.TrimSpace(opts.OutputDir)
	outputPath := strings.TrimSpace(opts.OutputPath)
	if outputDir != "" && outputPath != "" {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_output_conflict: specify either -output-dir or -output, not both")
	}
	if outputDir == "" && outputPath == "" {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_output_required: -output-dir or -output is required")
	}
	if opts.IncludePlatformEvents && outputPath != "" {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_platform_requires_dir: -include-platform-events requires -output-dir")
	}
	if opts.OlderThan < 0 {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_older_than_invalid: -older-than must be positive")
	}
	if opts.KeepCOSDays < 0 {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_keep_cos_invalid: -keep-cos-days must be >= 0")
	}
	if opts.KeepDocDays < 0 {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_keep_doc_invalid: -keep-doc-days must be >= 0")
	}
	cosPrefix, err := cleanCOSBackupPrefix(opts.COSPrefix)
	if err != nil {
		return GatewayMySQLCopyExitUsage, err
	}
	if opts.KeepLocal < 0 {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_keep_local_invalid: KeepLocal must be >= 0")
	}
	if opts.DeleteLocalAfterUpload && cosPrefix == "" {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_keep_local_unsafe: -keep-local=0 requires -cos-prefix so the archive is not discarded without remote storage")
	}
	if opts.KeepCOSDays > 0 && cosPrefix == "" {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_keep_cos_requires_prefix: -keep-cos-days requires -cos-prefix")
	}

	cutoff, hasExplicitCutoff := resolveAuditExportCutoff(nowUTC, opts)
	if opts.Purge && !hasExplicitCutoff {
		return GatewayMySQLCopyExitUsage, errors.New("audit_export_cutoff_required: -purge requires one of -before, -older-than, or -all")
	}
	if opts.Reclaim && !opts.Purge && !hasExplicitCutoff {
		cutoff = nowUTC
	}

	store, err := OpenGatewayStore(opts.DBPath)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	defer store.Close()

	if store.StoreBackend() == "mysql" && (opts.Purge || opts.Reclaim) {
		if err := confirmGatewayMySQLDatabase(opts.ConfirmDatabase); err != nil {
			return GatewayMySQLCopyExitUsage, err
		}
	}

	stamp := nowUTC.Format("20060102T150405Z")
	dateDir := nowUTC.Format("2006-01-02")
	var bundleDir string
	var auditArchivePath string
	var platformArchivePath string
	var manifestPath string
	if outputDir != "" {
		if err := os.MkdirAll(outputDir, 0700); err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_mkdir_failed: %w", err)
		}
		bundleDir = filepath.Join(outputDir, "audit-export-"+stamp)
		if err := os.MkdirAll(bundleDir, 0700); err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_bundle_mkdir_failed: %w", err)
		}
		auditArchivePath = filepath.Join(bundleDir, "audit-events-"+stamp+".jsonl.gz")
		platformArchivePath = filepath.Join(bundleDir, "platform-events-"+stamp+".jsonl.gz")
		manifestPath = filepath.Join(bundleDir, "manifest.json")
	} else {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_mkdir_failed: %w", err)
		}
		auditArchivePath = outputPath
	}

	auditSummary, tailDocPaths, err := exportAuditEventsGzip(ctx, store, auditArchivePath, cutoff, opts.IncludeUnresolved)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	fmt.Fprintf(out, "audit_export: rows=%d min_id=%d max_id=%d resource_audit=%d bytes=%d sha256=%s file=%s\n",
		auditSummary.Rows, auditSummary.MinID, auditSummary.MaxID, auditSummary.ResourceMap, auditSummary.Bytes, auditSummary.SHA256, auditArchivePath)

	var platformSummary *platformEventExportSummary
	if opts.IncludePlatformEvents {
		ps, err := exportPlatformEventsGzip(ctx, store, platformArchivePath, cutoff)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		platformSummary = &ps
		fmt.Fprintf(out, "platform_events_export: rows=%d bytes=%d sha256=%s file=%s\n",
			ps.Rows, ps.Bytes, ps.SHA256, platformArchivePath)
	}

	manifest := auditExportManifest{
		Version:               1,
		ExportedAt:            formatTime(nowUTC),
		Backend:               store.StoreBackend(),
		CutoffBefore:          formatTime(cutoff),
		IncludeUnresolved:     opts.IncludeUnresolved,
		IncludePlatformEvents: opts.IncludePlatformEvents,
		Audit:                 auditSummary,
		PlatformEvents:        platformSummary,
	}

	var cosStore cosBackupStore
	uploadedKeys := make(map[string]bool)
	if cosPrefix != "" {
		cosStore, err = openCOSBackupStoreFunc(ctx)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_cos_open_failed: %w", err)
		}
		remoteBase := cosPrefix + "/" + dateDir + "/" + stamp
		auditKey := remoteBase + "/" + filepath.Base(auditArchivePath)
		if _, err := cosStore.UploadFile(ctx, auditKey, "application/gzip", auditArchivePath); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		uploadedKeys[auditKey] = true
		manifest.Audit.COSKey = auditKey

		auditSHAKey := auditKey + ".sha256"
		if _, err := cosStore.UploadFile(ctx, auditSHAKey, "text/plain; charset=utf-8", auditArchivePath+".sha256"); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		uploadedKeys[auditSHAKey] = true
		fmt.Fprintf(out, "cos_uploaded: bucket=%s key=%s bytes=%d verified=yes\n", cosStore.BucketName(), auditKey, auditSummary.Bytes)

		if platformSummary != nil {
			peKey := remoteBase + "/" + filepath.Base(platformArchivePath)
			if _, err := cosStore.UploadFile(ctx, peKey, "application/gzip", platformArchivePath); err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			uploadedKeys[peKey] = true
			manifest.PlatformEvents.COSKey = peKey

			peSHAKey := peKey + ".sha256"
			if _, err := cosStore.UploadFile(ctx, peSHAKey, "text/plain; charset=utf-8", platformArchivePath+".sha256"); err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			uploadedKeys[peSHAKey] = true
			fmt.Fprintf(out, "cos_uploaded: bucket=%s key=%s bytes=%d verified=yes\n", cosStore.BucketName(), peKey, platformSummary.Bytes)
		}
	}

	if manifestPath != "" {
		if err := writeJSONFileAtomic0600(manifestPath, manifest); err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_manifest_failed: %w", err)
		}
		if cosStore != nil {
			manifestKey := cosPrefix + "/" + dateDir + "/" + stamp + "/manifest.json"
			if _, err := cosStore.UploadFile(ctx, manifestKey, "application/json", manifestPath); err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			uploadedKeys[manifestKey] = true
		}
	}

	// Fail-closed purge: only runs after archive + sha256 + verified COS upload succeeded.
	var auditDeleted int64
	var auditBatches int
	var platformDeleted int64
	var deliveriesDeleted int64
	if opts.Purge {
		if auditSummary.MaxID > 0 {
			del, batches, receiptID, err := purgeExportedAuditRows(ctx, store, cutoff, auditSummary.MaxID, opts.IncludeUnresolved, manifest.Audit.SHA256, manifest.Audit.COSKey)
			if err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			auditDeleted = del
			auditBatches = batches
			for _, rel := range tailDocPaths {
				store.removeAuditTailDoc(rel)
			}
			fmt.Fprintf(out, "audit_purged: deleted=%d batches=%d receipt_id=%d max_id=%d before=%s\n",
				auditDeleted, auditBatches, receiptID, auditSummary.MaxID, cutoff.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(out, "audit_purged: deleted=0 batches=0 before=%s\n", cutoff.UTC().Format(time.RFC3339))
		}
		if platformSummary != nil && platformSummary.Rows > 0 {
			peDel, wdDel, err := purgeExportedPlatformEvents(ctx, store, platformSummary.MaxOccurredMs, platformSummary.MaxID)
			if err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			platformDeleted = peDel
			deliveriesDeleted = wdDel
			fmt.Fprintf(out, "platform_events_purged: events=%d deliveries=%d before=%s\n",
				platformDeleted, deliveriesDeleted, cutoff.UTC().Format(time.RFC3339))
		}
	}

	if opts.Reclaim {
		tables := []string{"audit_events", "agent_resource_audit"}
		if opts.IncludePlatformEvents {
			tables = append(tables, "platform_events", "webhook_deliveries")
		}
		if err := store.ReclaimTables(ctx, tables...); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		fmt.Fprintf(out, "storage_reclaimed: backend=%s tables=%s\n", store.StoreBackend(), strings.Join(tables, ","))
	}

	// Post-verification retention pruning (local and COS).
	if outputDir != "" && (opts.DeleteLocalAfterUpload || opts.KeepLocal > 0) {
		keep := opts.KeepLocal
		if opts.DeleteLocalAfterUpload {
			keep = 0
		}
		removed, err := pruneLocalBackupEntries(outputDir, "audit-export-", keep)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_prune_local_failed: %w", err)
		}
		if removed > 0 {
			fmt.Fprintf(out, "local_pruned: removed=%d keep=%d\n", removed, keep)
		}
	}
	if cosStore != nil && opts.KeepCOSDays > 0 {
		pruneCutoff := nowUTC.Add(-time.Duration(opts.KeepCOSDays) * 24 * time.Hour)
		removed, err := pruneCOSBackupObjects(ctx, cosStore, cosPrefix, pruneCutoff, uploadedKeys)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_prune_cos_failed: %w", err)
		}
		if removed > 0 {
			fmt.Fprintf(out, "cos_pruned: removed=%d keep_days=%d\n", removed, opts.KeepCOSDays)
		}
	}
	if opts.KeepDocDays > 0 && store.auditDocStoreEnabled() {
		removed, err := store.pruneExpiredAuditDocs(opts.KeepDocDays, nowUTC)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("audit_export_prune_docs_failed: %w", err)
		}
		if removed > 0 {
			fmt.Fprintf(out, "audit_docs_pruned: removed=%d keep_doc_days=%d\n", removed, opts.KeepDocDays)
		}
	}

	return GatewayMySQLCopyExitOK, nil
}

func resolveAuditExportCutoff(nowUTC time.Time, opts GatewayAuditExportOptions) (time.Time, bool) {
	if opts.Before != nil {
		return opts.Before.UTC(), true
	}
	if opts.OlderThan > 0 {
		return nowUTC.Add(-opts.OlderThan), true
	}
	if opts.All {
		return nowUTC, true
	}
	return nowUTC, false
}

func confirmGatewayMySQLDatabase(confirm string) error {
	trimmed := strings.TrimSpace(confirm)
	if trimmed == "" {
		return errors.New("confirm_database_required: -confirm-database <name> is required for destructive MySQL maintenance and must equal the database named by DOOPS_GATEWAY_MYSQL_DSN")
	}
	dsn := strings.TrimSpace(os.Getenv(gatewayMySQLCopyDSNEnv))
	if dsn == "" {
		return errors.New("gateway_mysql_dsn_missing: DOOPS_GATEWAY_MYSQL_DSN is required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return errors.New("gateway_mysql_dsn_invalid: DOOPS_GATEWAY_MYSQL_DSN is invalid (value is never echoed)")
	}
	if cfg.DBName != trimmed {
		return fmt.Errorf("confirm_database_mismatch: -confirm-database=%q does not match DSN database %q", trimmed, cfg.DBName)
	}
	return nil
}

func exportAuditEventsGzip(ctx context.Context, store *GatewayStore, destPath string, cutoff time.Time, includeUnresolved bool) (auditExportSectionSummary, []string, error) {
	db := store.readDB()
	cols, err := tableColumnNames(ctx, db, store.auditPurgeDialect(), "audit_events")
	if err != nil {
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_columns_failed: %w", err)
	}
	idColIdx := -1
	quotedCols := make([]string, len(cols))
	dialect := store.auditPurgeDialect()
	for i, c := range cols {
		if c == "id" {
			idColIdx = i
		}
		quotedCols[i] = gatewayIdentifierFor(dialect, c)
	}
	if idColIdx < 0 {
		return auditExportSectionSummary{}, nil, errors.New("audit_export_missing_id_column: audit_events has no id column")
	}

	hasResourceAudit := tableExists(ctx, db, dialect, "agent_resource_audit")
	statusPredicate := `status NOT IN ('running', 'executing', 'outcome_unknown')`
	if includeUnresolved {
		statusPredicate = `status NOT IN ('running', 'executing')`
	}
	pageSQL := fmt.Sprintf(
		`SELECT %s FROM audit_events WHERE id > ? AND started_at_ms < ? AND action != 'audit:purge' AND %s ORDER BY id ASC LIMIT ?`,
		strings.Join(quotedCols, ", "),
		statusPredicate,
	)

	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_open_failed: %w", err)
	}
	cleanupTmp := func() {
		_ = f.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, hasher)}
	gz := gzip.NewWriter(cw)
	bw := bufio.NewWriterSize(gz, 256<<10)

	cutoffMs := cutoff.UTC().UnixMilli()
	var lastID, minID, maxID, totalRows, resourceAuditCount int64
	var tailDocPaths []string
	docStoreEnabled := store.auditDocStoreEnabled()
	dateSeen := make(map[string]map[int64]bool)

	type rawAuditRecord struct {
		id     int64
		fields map[string]any
	}

	for {
		rows, err := db.QueryContext(ctx, pageSQL, lastID, cutoffMs, auditExportPageSize)
		if err != nil {
			cleanupTmp()
			return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_query_failed: %w", err)
		}
		batch := make([]rawAuditRecord, 0, auditExportPageSize)
		batchIDs := make([]int64, 0, auditExportPageSize)
		for rows.Next() {
			rawVals := make([]any, len(cols))
			scanPtrs := make([]any, len(cols))
			for i := range rawVals {
				scanPtrs[i] = &rawVals[i]
			}
			if err := rows.Scan(scanPtrs...); err != nil {
				rows.Close()
				cleanupTmp()
				return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_scan_failed: %w", err)
			}
			fields := make(map[string]any, len(cols)+1)
			var rowID int64
			for i, col := range cols {
				v := normalizeExportCell(rawVals[i])
				if col == "tail" {
					if rawTail, ok := v.(string); ok {
						if ptr, isPtr := parseAuditDocPointer(rawTail); isPtr {
							tailDocPaths = append(tailDocPaths, ptr.Doc)
							v = store.hydrateAuditTail(rawTail)
						}
					}
				}
				fields[col] = v
				if i == idColIdx {
					rowID = exportInt64(rawVals[i])
				}
			}
			batch = append(batch, rawAuditRecord{id: rowID, fields: fields})
			batchIDs = append(batchIDs, rowID)
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil {
			cleanupTmp()
			return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_rows_failed: %w", rowsErr)
		}
		if closeErr != nil {
			cleanupTmp()
			return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_rows_close_failed: %w", closeErr)
		}
		if len(batch) == 0 {
			break
		}

		var resMap map[int64]map[string]any
		if hasResourceAudit {
			resMap, err = fetchResourceAuditForBatch(ctx, db, batchIDs)
			if err != nil {
				cleanupTmp()
				return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_resource_audit_failed: %w", err)
			}
		}

		for _, rec := range batch {
			if ra, ok := resMap[rec.id]; ok {
				rec.fields["_resource_audit"] = ra
				resourceAuditCount++
			}
			if docStoreEnabled {
				if err := ensureExportedRowInAuditDocLog(store, dateSeen, rec.id, rec.fields); err != nil {
					cleanupTmp()
					return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_doc_sync_failed: id=%d: %w", rec.id, err)
				}
			}
			line, err := json.Marshal(rec.fields)
			if err != nil {
				cleanupTmp()
				return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_marshal_failed: id=%d: %w", rec.id, err)
			}
			if _, err := bw.Write(line); err != nil {
				cleanupTmp()
				return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_write_failed: %w", err)
			}
			if err := bw.WriteByte('\n'); err != nil {
				cleanupTmp()
				return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_write_failed: %w", err)
			}
			if minID == 0 || rec.id < minID {
				minID = rec.id
			}
			if rec.id > maxID {
				maxID = rec.id
			}
			lastID = rec.id
			totalRows++
		}
		if len(batch) < auditExportPageSize {
			break
		}
	}

	if err := bw.Flush(); err != nil {
		cleanupTmp()
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_flush_failed: %w", err)
	}
	if err := gz.Close(); err != nil {
		cleanupTmp()
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_gzip_close_failed: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanupTmp()
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_fsync_failed: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_close_failed: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return auditExportSectionSummary{}, nil, fmt.Errorf("audit_export_rename_failed: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if err := writeSHA256Sidecar(destPath, digest); err != nil {
		return auditExportSectionSummary{}, nil, err
	}
	return auditExportSectionSummary{
		File:        filepath.Base(destPath),
		Rows:        totalRows,
		MinID:       minID,
		MaxID:       maxID,
		Bytes:       cw.n,
		SHA256:      digest,
		ResourceMap: resourceAuditCount,
	}, tailDocPaths, nil
}

// ensureExportedRowInAuditDocLog backfills any DB audit row that bypassed
// FinishAudit (such as tx-committed credential/inventory rows or rows written
// before DOOPS_AUDIT_STORAGE=file was enabled) into <dataDir>/audit/records/
// before DB purge removes it.
func ensureExportedRowInAuditDocLog(store *GatewayStore, dateSeen map[string]map[int64]bool, id int64, fields map[string]any) error {
	if id <= 0 {
		return nil
	}
	startedMs := exportInt64(fields["started_at_ms"])
	var startedAt time.Time
	if s, ok := fields["started_at"].(string); ok && s != "" {
		startedAt, _ = parseTime(s)
	}
	if startedAt.IsZero() && startedMs > 0 {
		startedAt = time.UnixMilli(startedMs).UTC()
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	dateKey := startedAt.UTC().Format("2006-01-02")
	seen, loaded := dateSeen[dateKey]
	if !loaded {
		seen = store.readAuditDocDateFileIDs(dateKey)
		dateSeen[dateKey] = seen
	}
	if seen[id] {
		return nil
	}
	ev := AuditEvent{
		ID:              id,
		UserID:          exportString(fields["user_id"]),
		TokenID:         exportString(fields["token_id"]),
		Cluster:         exportString(fields["cluster"]),
		Instance:        exportString(fields["instance"]),
		CredentialID:    exportString(fields["credential_id"]),
		VersionID:       exportString(fields["credential_version_id"]),
		GrantID:         exportString(fields["credential_grant_id"]),
		ResourceVersion: exportString(fields["resource_version"]),
		Digest:          exportString(fields["digest"]),
		CorrelationID:   exportString(fields["correlation_id"]),
		Action:          GatewayAction(exportString(fields["action"])),
		Session:         exportString(fields["session"]),
		CommandSummary:  exportString(fields["command_summary"]),
		Status:          exportString(fields["status"]),
		Error:           exportString(fields["error"]),
		Tail:            exportString(fields["tail"]),
		BytesIn:         exportInt64(fields["bytes_in"]),
		BytesOut:        exportInt64(fields["bytes_out"]),
		Model:           exportString(fields["model"]),
		InputTokens:     exportInt64(fields["input_tokens"]),
		OutputTokens:    exportInt64(fields["output_tokens"]),
		TotalTokens:     exportInt64(fields["total_tokens"]),
		StartedAt:       startedAt.UTC(),
	}
	if rawScope := exportString(fields["scope_json"]); rawScope != "" {
		var scope CredentialGrantScope
		if err := json.Unmarshal([]byte(rawScope), &scope); err == nil {
			ev.Scope = &scope
		}
	}
	if ended := exportString(fields["ended_at"]); ended != "" {
		ev.EndedAt, _ = parseTime(ended)
	}
	if err := store.appendAuditDocRecord(ev); err != nil {
		return err
	}
	seen[id] = true
	return nil
}

func exportString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func fetchResourceAuditForBatch(ctx context.Context, db *sql.DB, ids []int64) (map[int64]map[string]any, error) {
	out := make(map[int64]map[string]any)
	for start := 0; start < len(ids); start += 400 {
		end := start + 400
		if end > len(ids) {
			end = len(ids)
		}
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		rows, err := db.QueryContext(ctx,
			`SELECT audit_id, manifest_revision, resource_refs_json FROM agent_resource_audit WHERE audit_id IN (`+gatewayInPlaceholders(len(args))+`)`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var auditID int64
			var rev int64
			var refs string
			if err := rows.Scan(&auditID, &rev, &refs); err != nil {
				rows.Close()
				return nil, err
			}
			out[auditID] = map[string]any{
				"manifest_revision":  rev,
				"resource_refs_json": refs,
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func exportPlatformEventsGzip(ctx context.Context, store *GatewayStore, destPath string, cutoff time.Time) (platformEventExportSummary, error) {
	db := store.readDB()
	dialect := store.auditPurgeDialect()
	if !tableExists(ctx, db, dialect, "platform_events") {
		return platformEventExportSummary{File: filepath.Base(destPath)}, nil
	}
	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return platformEventExportSummary{}, fmt.Errorf("platform_events_export_open_failed: %w", err)
	}
	cleanupTmp := func() {
		_ = f.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, hasher)}
	gz := gzip.NewWriter(cw)
	bw := bufio.NewWriterSize(gz, 256<<10)

	cutoffMs := cutoff.UTC().UnixMilli()
	var lastOccurredMs int64 = -1
	var lastID string
	var totalRows int64

	const pageSQL = `SELECT ev.id, ev.type, ev.occurred_at_ms, ev.occurred_at, ev.actor_user_id, ev.cluster, ev.instance, ev.correlation_id, ev.body_json, ev.dedupe_key, ev.created_at
		FROM platform_events ev
		WHERE (ev.occurred_at_ms > ? OR (ev.occurred_at_ms = ? AND ev.id > ?))
		  AND ev.occurred_at_ms < ?
		  AND NOT EXISTS (SELECT 1 FROM webhook_deliveries d WHERE d.event_id = ev.id AND d.status IN ('pending','delivering'))
		ORDER BY ev.occurred_at_ms ASC, ev.id ASC
		LIMIT ?`

	for {
		rows, err := db.QueryContext(ctx, pageSQL, lastOccurredMs, lastOccurredMs, lastID, cutoffMs, auditExportPageSize)
		if err != nil {
			cleanupTmp()
			return platformEventExportSummary{}, fmt.Errorf("platform_events_export_query_failed: %w", err)
		}
		batchCount := 0
		for rows.Next() {
			var (
				id, evType, occurredAt, actorUserID, cluster, instance, corrID, bodyJSON, createdAt string
				occurredAtMs                                                                        int64
				dedupeKey                                                                           sql.NullString
			)
			if err := rows.Scan(&id, &evType, &occurredAtMs, &occurredAt, &actorUserID, &cluster, &instance, &corrID, &bodyJSON, &dedupeKey, &createdAt); err != nil {
				rows.Close()
				cleanupTmp()
				return platformEventExportSummary{}, fmt.Errorf("platform_events_export_scan_failed: %w", err)
			}
			rec := map[string]any{
				"id":             id,
				"type":           evType,
				"occurred_at_ms": occurredAtMs,
				"occurred_at":    occurredAt,
				"actor_user_id":  actorUserID,
				"cluster":        cluster,
				"instance":       instance,
				"correlation_id": corrID,
				"body_json":      bodyJSON,
				"created_at":     createdAt,
			}
			if dedupeKey.Valid {
				rec["dedupe_key"] = dedupeKey.String
			} else {
				rec["dedupe_key"] = nil
			}
			line, err := json.Marshal(rec)
			if err != nil {
				rows.Close()
				cleanupTmp()
				return platformEventExportSummary{}, fmt.Errorf("platform_events_export_marshal_failed: %w", err)
			}
			if _, err := bw.Write(line); err != nil {
				rows.Close()
				cleanupTmp()
				return platformEventExportSummary{}, fmt.Errorf("platform_events_export_write_failed: %w", err)
			}
			if err := bw.WriteByte('\n'); err != nil {
				rows.Close()
				cleanupTmp()
				return platformEventExportSummary{}, fmt.Errorf("platform_events_export_write_failed: %w", err)
			}
			lastOccurredMs = occurredAtMs
			lastID = id
			totalRows++
			batchCount++
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil {
			cleanupTmp()
			return platformEventExportSummary{}, fmt.Errorf("platform_events_export_rows_failed: %w", rowsErr)
		}
		if closeErr != nil {
			cleanupTmp()
			return platformEventExportSummary{}, fmt.Errorf("platform_events_export_close_failed: %w", closeErr)
		}
		if batchCount < auditExportPageSize {
			break
		}
	}

	if err := bw.Flush(); err != nil {
		cleanupTmp()
		return platformEventExportSummary{}, fmt.Errorf("platform_events_export_flush_failed: %w", err)
	}
	if err := gz.Close(); err != nil {
		cleanupTmp()
		return platformEventExportSummary{}, fmt.Errorf("platform_events_export_gzip_failed: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanupTmp()
		return platformEventExportSummary{}, fmt.Errorf("platform_events_export_fsync_failed: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return platformEventExportSummary{}, fmt.Errorf("platform_events_export_file_close_failed: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return platformEventExportSummary{}, fmt.Errorf("platform_events_export_rename_failed: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if err := writeSHA256Sidecar(destPath, digest); err != nil {
		return platformEventExportSummary{}, err
	}
	return platformEventExportSummary{
		File:          filepath.Base(destPath),
		Rows:          totalRows,
		MaxOccurredMs: lastOccurredMs,
		MaxID:         lastID,
		Bytes:         cw.n,
		SHA256:        digest,
	}, nil
}

// purgeExportedAuditRows deletes only rows that were guaranteed to be covered by
// the just-verified export (id <= maxExportedID and started_at_ms < cutoffMs),
// in bounded AuditPurgeBatchSize transactions with a persistent audit:purge
// receipt recording the archive SHA-256 and COS key.
func purgeExportedAuditRows(ctx context.Context, store *GatewayStore, cutoff time.Time, maxExportedID int64, includeUnresolved bool, archiveSHA256, cosKey string) (int64, int, int64, error) {
	actor := fmt.Sprintf("local:uid:%d", os.Geteuid())
	beforeStr := formatTime(cutoff.UTC())
	tailMeta := auditExportPurgeReceiptTail{
		Before:            beforeStr,
		ArchiveSHA256:     archiveSHA256,
		COSKey:            cosKey,
		IncludeUnresolved: includeUnresolved,
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	nowUTC := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `INSERT INTO audit_events
		(user_id, token_id, action, session, command_summary, status, error, tail, ended_at, scope_json, started_at_ms, started_at)
		VALUES (?, '', 'audit:purge', 'audit-export', ?, 'running', '', '', '', '', ?, ?)`,
		actor, "audit:export-purge before="+beforeStr, nowUTC.UnixMilli(), formatTime(nowUTC))
	if err != nil {
		_ = tx.Rollback()
		return 0, 0, 0, err
	}
	receiptID, err := res.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return 0, 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}

	initialTail, _ := json.Marshal(tailMeta)
	if err := store.FinishAudit(receiptID, AuditFinish{
		Status:  "success",
		Tail:    string(initialTail),
		EndedAt: time.Now().UTC(),
	}); err != nil {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM audit_events WHERE id = ? AND action = 'audit:purge' AND status IN ('running', 'executing')`, receiptID)
		return 0, 0, receiptID, err
	}

	failReceipt := func(cause error) error {
		encoded, _ := json.Marshal(tailMeta)
		_, _ = store.db.ExecContext(ctx, `UPDATE audit_events SET status = 'error', error = 'audit_purge_failed', tail = ?, ended_at = ? WHERE id = ? AND action = 'audit:purge'`,
			string(encoded), formatTime(time.Now().UTC()), receiptID)
		return cause
	}

	dialect := store.auditPurgeDialect()
	cutoffMs := cutoff.UTC().UnixMilli()
	var totalDeleted int64
	batches := 0

	for {
		btx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return totalDeleted, batches, receiptID, failReceipt(err)
		}
		deleted, complete, err := drainExportedAuditBatchTx(ctx, btx, cutoffMs, maxExportedID, includeUnresolved, dialect)
		if err != nil {
			_ = btx.Rollback()
			return totalDeleted, batches, receiptID, failReceipt(err)
		}
		nextMeta := tailMeta
		nextMeta.Deleted += deleted
		if deleted > 0 {
			nextMeta.Batches++
		}
		encoded, err := json.Marshal(nextMeta)
		if err != nil {
			_ = btx.Rollback()
			return totalDeleted, batches, receiptID, failReceipt(err)
		}
		if _, err := btx.ExecContext(ctx, `UPDATE audit_events SET tail = ? WHERE id = ? AND action = 'audit:purge'`, string(encoded), receiptID); err != nil {
			_ = btx.Rollback()
			return totalDeleted, batches, receiptID, failReceipt(err)
		}
		if err := btx.Commit(); err != nil {
			return totalDeleted, batches, receiptID, failReceipt(err)
		}
		tailMeta = nextMeta
		totalDeleted = tailMeta.Deleted
		batches = tailMeta.Batches
		if complete {
			return totalDeleted, batches, receiptID, nil
		}
		auditPurgePause()
	}
}

func drainExportedAuditBatchTx(ctx context.Context, tx *sql.Tx, cutoffMs, maxExportedID int64, includeUnresolved bool, dialect gatewaySQLDialect) (int64, bool, error) {
	if includeUnresolved {
		return deleteAuditIDBatchTx(ctx, tx,
			`SELECT id FROM audit_events
			 WHERE id <= ?
			   AND started_at_ms < ?
			   AND action != 'audit:purge'
			   AND status NOT IN ('running', 'executing')
			 ORDER BY id ASC
			 LIMIT ?`,
			maxExportedID, cutoffMs, AuditPurgeBatchSize,
		)
	}

	// Standard retention rules, strictly bounded by id <= maxExportedID.
	batch, complete, err := deleteAuditIDBatchTx(ctx, tx,
		`SELECT id FROM audit_events
		 WHERE id <= ?
		   AND started_at_ms < ?
		   AND action NOT IN ('agent:upgrade', 'audit:purge')
		   AND status NOT IN (`+activeAuditStatusSQL+`)
		 ORDER BY id ASC
		 LIMIT ?`,
		maxExportedID, cutoffMs, AuditPurgeBatchSize,
	)
	if err != nil || !complete {
		return batch, false, err
	}

	var upgradeQuery string
	if dialect == gatewayDialectMySQL {
		upgradeQuery = `DELETE target FROM audit_events AS target JOIN (
			SELECT victim.id AS id FROM audit_events AS victim
			LEFT JOIN (
				SELECT cluster, instance, correlation_id,
					JSON_UNQUOTE(json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job')) AS job,
					MAX(id) AS id
				FROM audit_events
				WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
					AND JSON_UNQUOTE(json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome')) IN ('succeeded', 'failed')
				GROUP BY cluster, instance, correlation_id, job
			) AS terminal
				ON terminal.id > victim.id
				AND terminal.cluster = victim.cluster AND terminal.instance = victim.instance
				AND terminal.correlation_id = victim.correlation_id AND terminal.correlation_id != ''
				AND terminal.job = JSON_UNQUOTE(json_extract(CASE WHEN json_valid(victim.tail) THEN victim.tail ELSE '{}' END, '$.result.job'))
			WHERE victim.id <= ?
				AND victim.started_at_ms < ?
				AND victim.action = 'agent:upgrade'
				AND victim.status NOT IN (` + activeAuditStatusSQL + `)
				AND (victim.status NOT IN ('accepted', 'observing') OR terminal.id IS NOT NULL)
			ORDER BY victim.id ASC
			LIMIT 18446744073709551615
		) AS batch ON batch.id = target.id`
	} else {
		upgradeQuery = `WITH terminal AS (
			SELECT cluster, instance, correlation_id,
				json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.job') AS job,
				MAX(id) AS id
			FROM audit_events
			WHERE action = 'agent:upgrade' AND status IN ('success', 'error')
				AND json_extract(CASE WHEN json_valid(tail) THEN tail ELSE '{}' END, '$.result.outcome') IN ('succeeded', 'failed')
			GROUP BY cluster, instance, correlation_id, job
		)
		DELETE FROM audit_events WHERE id IN (
			SELECT audit_events.id FROM audit_events
			WHERE id <= ?
				AND started_at_ms < ?
				AND action = 'agent:upgrade'
				AND status NOT IN (` + activeAuditStatusSQL + `)
				AND (status NOT IN ('accepted', 'observing') OR EXISTS (
					SELECT 1 FROM terminal WHERE terminal.id > audit_events.id
						AND terminal.cluster = audit_events.cluster AND terminal.instance = audit_events.instance
						AND terminal.correlation_id = audit_events.correlation_id AND terminal.correlation_id != ''
						AND terminal.job = json_extract(CASE WHEN json_valid(audit_events.tail) THEN audit_events.tail ELSE '{}' END, '$.result.job')
				))
		)`
	}
	uRes, err := tx.ExecContext(ctx, upgradeQuery, maxExportedID, cutoffMs)
	if err != nil {
		return 0, false, err
	}
	upgrades, err := uRes.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	return batch + upgrades, true, nil
}

// deleteAuditIDBatchTx selects up to limit matching audit_events ids and
// deletes them in 400-id chunks within the same transaction, avoiding
// dialect-specific DELETE ... WHERE id IN (SELECT ... LIMIT ?) incompatibilities.
func deleteAuditIDBatchTx(ctx context.Context, tx *sql.Tx, selectSQL string, maxExportedID, cutoffMs int64, limit int) (int64, bool, error) {
	rows, err := tx.QueryContext(ctx, selectSQL, maxExportedID, cutoffMs, limit)
	if err != nil {
		return 0, false, err
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, false, err
		}
		ids = append(ids, id)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil {
		return 0, false, rowsErr
	}
	if closeErr != nil {
		return 0, false, closeErr
	}
	if len(ids) == 0 {
		return 0, true, nil
	}
	var deleted int64
	for start := 0; start < len(ids); start += 400 {
		end := start + 400
		if end > len(ids) {
			end = len(ids)
		}
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE id IN (`+gatewayInPlaceholders(len(args))+`)`, args...)
		if err != nil {
			return deleted, false, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return deleted, false, err
		}
		deleted += n
	}
	return deleted, len(ids) < limit, nil
}

func purgeExportedPlatformEvents(ctx context.Context, store *GatewayStore, maxOccurredMs int64, maxEventID string) (int64, int64, error) {
	var totalEvents, totalDeliveries int64
	for {
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return totalEvents, totalDeliveries, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT ev.id FROM platform_events ev
			WHERE (ev.occurred_at_ms < ? OR (ev.occurred_at_ms = ? AND ev.id <= ?))
			  AND NOT EXISTS (SELECT 1 FROM webhook_deliveries d WHERE d.event_id = ev.id AND d.status IN ('pending','delivering'))
			ORDER BY ev.occurred_at_ms ASC, ev.id ASC
			LIMIT ?`, maxOccurredMs, maxOccurredMs, maxEventID, platformEventBatchSize)
		if err != nil {
			_ = tx.Rollback()
			return totalEvents, totalDeliveries, err
		}
		ids := make([]string, 0, platformEventBatchSize)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				_ = tx.Rollback()
				return totalEvents, totalDeliveries, err
			}
			ids = append(ids, id)
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil {
			_ = tx.Rollback()
			return totalEvents, totalDeliveries, rowsErr
		}
		if closeErr != nil {
			_ = tx.Rollback()
			return totalEvents, totalDeliveries, closeErr
		}
		if len(ids) == 0 {
			_ = tx.Rollback()
			return totalEvents, totalDeliveries, nil
		}
		for start := 0; start < len(ids); start += 400 {
			end := start + 400
			if end > len(ids) {
				end = len(ids)
			}
			args := make([]any, 0, end-start)
			for _, id := range ids[start:end] {
				args = append(args, id)
			}
			ph := gatewayInPlaceholders(len(args))
			res, err := tx.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE event_id IN (`+ph+`)`, args...)
			if err != nil {
				_ = tx.Rollback()
				return totalEvents, totalDeliveries, err
			}
			n, _ := res.RowsAffected()
			totalDeliveries += n
			res, err = tx.ExecContext(ctx, `DELETE FROM platform_events WHERE id IN (`+ph+`)`, args...)
			if err != nil {
				_ = tx.Rollback()
				return totalEvents, totalDeliveries, err
			}
			n, _ = res.RowsAffected()
			totalEvents += n
		}
		if err := tx.Commit(); err != nil {
			return totalEvents, totalDeliveries, err
		}
		if len(ids) < platformEventBatchSize {
			return totalEvents, totalDeliveries, nil
		}
		auditPurgePause()
	}
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func tableExists(ctx context.Context, db *sql.DB, dialect gatewaySQLDialect, name string) bool {
	var count int
	if err := db.QueryRowContext(ctx, gatewayExistingTableCountSQL(dialect, 1), name).Scan(&count); err != nil {
		return false
	}
	return count == 1
}

// tableColumnNames returns non-generated column names in ordinal order.
func tableColumnNames(ctx context.Context, db *sql.DB, dialect gatewaySQLDialect, table string) ([]string, error) {
	query, args := gatewayTableColumnsSQLFor(dialect, table)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	if dialect == gatewayDialectMySQL {
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			cols = append(cols, name)
		}
		return cols, rows.Err()
	}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func normalizeExportCell(v any) any {
	switch val := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(val)
	case time.Time:
		return formatTime(val.UTC())
	default:
		return val
	}
}

func exportInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case uint64:
		return int64(val)
	case []byte:
		var n int64
		_, _ = fmt.Sscan(string(val), &n)
		return n
	default:
		return 0
	}
}

func writeSHA256Sidecar(archivePath, digest string) error {
	content := fmt.Sprintf("%s  %s\n", digest, filepath.Base(archivePath))
	tmp := archivePath + ".sha256.tmp"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return fmt.Errorf("backup_sha256_write_failed: %w", err)
	}
	if err := os.Rename(tmp, archivePath+".sha256"); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup_sha256_rename_failed: %w", err)
	}
	return nil
}

func writeJSONFileAtomic0600(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(encoded); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// pruneLocalBackupEntries deletes oldest directories/files matching prefix under
// rootDir so at most keep newest entries remain. Entries are sorted by name
// (timestamp suffix gives lexicographical chronological order).
func pruneLocalBackupEntries(rootDir, prefix string, keep int) (int, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return 0, err
	}
	var matching []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			matching = append(matching, entry.Name())
		}
	}
	sort.Strings(matching)
	if len(matching) <= keep {
		return 0, nil
	}
	removeCount := len(matching) - keep
	removed := 0
	for _, name := range matching[:removeCount] {
		if err := os.RemoveAll(filepath.Join(rootDir, name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
