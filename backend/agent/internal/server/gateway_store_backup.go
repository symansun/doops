package server

import (
	"archive/tar"
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
)

// GatewayStoreBackupOptions controls `doops-gateway store backup`.
type GatewayStoreBackupOptions struct {
	DBPath                 string
	OutputDir              string
	COSPrefix              string
	ExcludeTables          []string
	SkipDataDir            bool
	KeepLocal              int  // 0 keeps all; >0 keeps newest N local bundles
	DeleteLocalAfterUpload bool // requires COSPrefix; deletes local bundle after verified COS upload
	KeepCOSDays            int  // 0 keeps all; >0 prunes COS objects under COSPrefix older than N days
}

type storeBackupManifest struct {
	Version    int                        `json:"version"`
	BackedUpAt string                     `json:"backed_up_at"`
	Backend    string                     `json:"backend"`
	DBArchive  storeBackupArchiveInfo     `json:"db_archive"`
	DataDir    *storeBackupDataDirSummary `json:"data_dir,omitempty"`
	Tables     []storeBackupTableSummary  `json:"tables"`
	Excluded   []string                   `json:"excluded_tables,omitempty"`
}

type storeBackupArchiveInfo struct {
	File   string `json:"file"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	COSKey string `json:"cos_key,omitempty"`
}

type storeBackupDataDirSummary struct {
	File   string `json:"file"`
	Files  int64  `json:"files"`
	Bytes  int64  `json:"archive_bytes"`
	SHA256 string `json:"sha256"`
	COSKey string `json:"cos_key,omitempty"`
}

type storeBackupTableSummary struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
	SHA256  string   `json:"sha256"`
}

// RunGatewayStoreBackup creates a point-in-time consistent database snapshot
// paired with the Gateway data directory (skills/ and artifacts/), writes a
// checksummed bundle at mode 0600, optionally uploads it to COS with HeadObject
// verification, and applies local and COS retention pruning.
func RunGatewayStoreBackup(ctx context.Context, opts GatewayStoreBackupOptions, out io.Writer) (int, error) {
	if out == nil {
		out = io.Discard
	}
	outputDir := strings.TrimSpace(opts.OutputDir)
	if outputDir == "" {
		return GatewayMySQLCopyExitUsage, errors.New("store_backup_output_required: -output-dir is required")
	}
	if opts.KeepCOSDays < 0 {
		return GatewayMySQLCopyExitUsage, errors.New("store_backup_keep_cos_invalid: -keep-cos-days must be >= 0")
	}
	cosPrefix, err := cleanCOSBackupPrefix(opts.COSPrefix)
	if err != nil {
		return GatewayMySQLCopyExitUsage, err
	}
	if opts.KeepLocal < 0 {
		return GatewayMySQLCopyExitUsage, errors.New("store_backup_keep_local_invalid: KeepLocal must be >= 0")
	}
	if opts.DeleteLocalAfterUpload && cosPrefix == "" {
		return GatewayMySQLCopyExitUsage, errors.New("store_backup_keep_local_unsafe: -keep-local=0 requires -cos-prefix so the backup is not discarded without remote storage")
	}
	if opts.KeepCOSDays > 0 && cosPrefix == "" {
		return GatewayMySQLCopyExitUsage, errors.New("store_backup_keep_cos_requires_prefix: -keep-cos-days requires -cos-prefix")
	}

	excluded := make(map[string]bool, len(opts.ExcludeTables))
	var excludedList []string
	for _, raw := range opts.ExcludeTables {
		name := strings.TrimSpace(raw)
		if name == "" || excluded[name] {
			continue
		}
		excluded[name] = true
		excludedList = append(excludedList, name)
	}
	sort.Strings(excludedList)

	store, err := OpenGatewayStore(opts.DBPath)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	defer store.Close()

	nowUTC := time.Now().UTC()
	stamp := nowUTC.Format("20060102T150405Z")
	dateDir := nowUTC.Format("2006-01-02")

	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return GatewayMySQLCopyExitRuntime, fmt.Errorf("store_backup_mkdir_failed: %w", err)
	}
	bundleDir := filepath.Join(outputDir, "gateway-backup-"+stamp)
	if err := os.MkdirAll(bundleDir, 0700); err != nil {
		return GatewayMySQLCopyExitRuntime, fmt.Errorf("store_backup_bundle_mkdir_failed: %w", err)
	}

	dbArchivePath := filepath.Join(bundleDir, "db-tables-"+stamp+".tar.gz")
	dbArchiveInfo, tables, totalRows, err := backupStoreTablesTarGz(ctx, store, dbArchivePath, excluded)
	if err != nil {
		return GatewayMySQLCopyExitRuntime, err
	}
	fmt.Fprintf(out, "db_backup: backend=%s tables=%d rows=%d bytes=%d sha256=%s file=%s\n",
		store.StoreBackend(), len(tables), totalRows, dbArchiveInfo.Bytes, dbArchiveInfo.SHA256, dbArchivePath)

	var dataDirSummary *storeBackupDataDirSummary
	var dataArchivePath string
	if !opts.SkipDataDir && strings.TrimSpace(store.StoreDataDir()) != "" {
		dataArchivePath = filepath.Join(bundleDir, "data-dir-"+stamp+".tar.gz")
		summary, err := archiveGatewayDataDir(store.StoreDataDir(), dataArchivePath)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		dataDirSummary = &summary
		fmt.Fprintf(out, "data_dir_backup: files=%d bytes=%d sha256=%s file=%s\n",
			summary.Files, summary.Bytes, summary.SHA256, dataArchivePath)
	}

	manifest := storeBackupManifest{
		Version:    1,
		BackedUpAt: formatTime(nowUTC),
		Backend:    store.StoreBackend(),
		DBArchive:  dbArchiveInfo,
		DataDir:    dataDirSummary,
		Tables:     tables,
		Excluded:   excludedList,
	}

	var cosStore cosBackupStore
	uploadedKeys := make(map[string]bool)
	if cosPrefix != "" {
		cosStore, err = openCOSBackupStoreFunc(ctx)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("store_backup_cos_open_failed: %w", err)
		}
		remoteBase := cosPrefix + "/" + dateDir + "/" + stamp
		dbKey := remoteBase + "/" + filepath.Base(dbArchivePath)
		if _, err := cosStore.UploadFile(ctx, dbKey, "application/gzip", dbArchivePath); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		uploadedKeys[dbKey] = true
		manifest.DBArchive.COSKey = dbKey

		dbSHAKey := dbKey + ".sha256"
		if _, err := cosStore.UploadFile(ctx, dbSHAKey, "text/plain; charset=utf-8", dbArchivePath+".sha256"); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		uploadedKeys[dbSHAKey] = true
		fmt.Fprintf(out, "cos_uploaded: bucket=%s key=%s bytes=%d verified=yes\n", cosStore.BucketName(), dbKey, dbArchiveInfo.Bytes)

		if dataDirSummary != nil {
			ddKey := remoteBase + "/" + filepath.Base(dataArchivePath)
			if _, err := cosStore.UploadFile(ctx, ddKey, "application/gzip", dataArchivePath); err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			uploadedKeys[ddKey] = true
			manifest.DataDir.COSKey = ddKey

			ddSHAKey := ddKey + ".sha256"
			if _, err := cosStore.UploadFile(ctx, ddSHAKey, "text/plain; charset=utf-8", dataArchivePath+".sha256"); err != nil {
				return GatewayMySQLCopyExitRuntime, err
			}
			uploadedKeys[ddSHAKey] = true
			fmt.Fprintf(out, "cos_uploaded: bucket=%s key=%s bytes=%d verified=yes\n", cosStore.BucketName(), ddKey, dataDirSummary.Bytes)
		}
	}

	manifestPath := filepath.Join(bundleDir, "manifest.json")
	if err := writeJSONFileAtomic0600(manifestPath, manifest); err != nil {
		return GatewayMySQLCopyExitRuntime, fmt.Errorf("store_backup_manifest_failed: %w", err)
	}
	if cosStore != nil {
		manifestKey := cosPrefix + "/" + dateDir + "/" + stamp + "/manifest.json"
		if _, err := cosStore.UploadFile(ctx, manifestKey, "application/json", manifestPath); err != nil {
			return GatewayMySQLCopyExitRuntime, err
		}
		uploadedKeys[manifestKey] = true
	}

	if opts.DeleteLocalAfterUpload || opts.KeepLocal > 0 {
		keep := opts.KeepLocal
		if opts.DeleteLocalAfterUpload {
			keep = 0
		}
		removed, err := pruneLocalBackupEntries(outputDir, "gateway-backup-", keep)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("store_backup_prune_local_failed: %w", err)
		}
		if removed > 0 {
			fmt.Fprintf(out, "local_pruned: removed=%d keep=%d\n", removed, keep)
		}
	}
	if cosStore != nil && opts.KeepCOSDays > 0 {
		pruneCutoff := nowUTC.Add(-time.Duration(opts.KeepCOSDays) * 24 * time.Hour)
		removed, err := pruneCOSBackupObjects(ctx, cosStore, cosPrefix, pruneCutoff, uploadedKeys)
		if err != nil {
			return GatewayMySQLCopyExitRuntime, fmt.Errorf("store_backup_prune_cos_failed: %w", err)
		}
		if removed > 0 {
			fmt.Fprintf(out, "cos_pruned: removed=%d keep_days=%d\n", removed, opts.KeepCOSDays)
		}
	}

	return GatewayMySQLCopyExitOK, nil
}

func backupStoreTablesTarGz(ctx context.Context, store *GatewayStore, destPath string, excluded map[string]bool) (storeBackupArchiveInfo, []storeBackupTableSummary, int64, error) {
	dialect := store.auditPurgeDialect()
	db := store.readDB()

	tableNames, err := listGatewayBaseTables(ctx, db, dialect)
	if err != nil {
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_list_tables_failed: %w", err)
	}

	txOpts := &sql.TxOptions{ReadOnly: true}
	if dialect == gatewayDialectMySQL {
		txOpts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := db.BeginTx(ctx, txOpts)
	if err != nil {
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_tx_failed: %w", err)
	}
	defer tx.Rollback()

	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_open_failed: %w", err)
	}
	cleanupTmp := func() {
		_ = f.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, hasher)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)

	var summaries []storeBackupTableSummary
	var totalRows int64
	now := time.Now().UTC()

	for _, table := range tableNames {
		if excluded[table] {
			continue
		}
		cols, err := tableColumnNamesTx(ctx, tx, dialect, table)
		if err != nil {
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_columns_failed: table=%s: %w", table, err)
		}
		if len(cols) == 0 {
			continue
		}
		quotedCols := make([]string, len(cols))
		for i, c := range cols {
			quotedCols[i] = gatewayIdentifierFor(dialect, c)
		}

		// Stream the table to a temporary file first so tar header gets exact size
		// without holding large tables in RAM.
		tableTmp, err := os.CreateTemp(filepath.Dir(destPath), ".table-"+table+"-")
		if err != nil {
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_table_tmp_failed: table=%s: %w", table, err)
		}
		tableTmpPath := tableTmp.Name()
		tableHasher := sha256.New()
		tableCW := &countingWriter{w: io.MultiWriter(tableTmp, tableHasher)}
		tableBW := bufio.NewWriterSize(tableCW, 128<<10)

		query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(quotedCols, ", "), gatewayIdentifierFor(dialect, table))
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			_ = tableTmp.Close()
			_ = os.Remove(tableTmpPath)
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_query_failed: table=%s: %w", table, err)
		}
		var rowCount int64
		for rows.Next() {
			rawVals := make([]any, len(cols))
			scanPtrs := make([]any, len(cols))
			for i := range rawVals {
				scanPtrs[i] = &rawVals[i]
			}
			if err := rows.Scan(scanPtrs...); err != nil {
				rows.Close()
				_ = tableTmp.Close()
				_ = os.Remove(tableTmpPath)
				cleanupTmp()
				return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_scan_failed: table=%s: %w", table, err)
			}
			rec := make(map[string]any, len(cols))
			for i, col := range cols {
				rec[col] = normalizeExportCell(rawVals[i])
			}
			line, err := json.Marshal(rec)
			if err != nil {
				rows.Close()
				_ = tableTmp.Close()
				_ = os.Remove(tableTmpPath)
				cleanupTmp()
				return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_marshal_failed: table=%s: %w", table, err)
			}
			if _, err := tableBW.Write(line); err != nil {
				rows.Close()
				_ = tableTmp.Close()
				_ = os.Remove(tableTmpPath)
				cleanupTmp()
				return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_write_failed: table=%s: %w", table, err)
			}
			if err := tableBW.WriteByte('\n'); err != nil {
				rows.Close()
				_ = tableTmp.Close()
				_ = os.Remove(tableTmpPath)
				cleanupTmp()
				return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_write_failed: table=%s: %w", table, err)
			}
			rowCount++
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil || closeErr != nil {
			_ = tableTmp.Close()
			_ = os.Remove(tableTmpPath)
			cleanupTmp()
			if rowsErr != nil {
				return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_rows_failed: table=%s: %w", table, rowsErr)
			}
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_rows_close_failed: table=%s: %w", table, closeErr)
		}
		if err := tableBW.Flush(); err != nil {
			_ = tableTmp.Close()
			_ = os.Remove(tableTmpPath)
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_flush_failed: table=%s: %w", table, err)
		}
		if _, err := tableTmp.Seek(0, io.SeekStart); err != nil {
			_ = tableTmp.Close()
			_ = os.Remove(tableTmpPath)
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_seek_failed: table=%s: %w", table, err)
		}
		hdr := &tar.Header{
			Name:    "tables/" + table + ".jsonl",
			Mode:    0600,
			Size:    tableCW.n,
			ModTime: now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = tableTmp.Close()
			_ = os.Remove(tableTmpPath)
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_tar_header_failed: table=%s: %w", table, err)
		}
		if _, err := io.Copy(tw, tableTmp); err != nil {
			_ = tableTmp.Close()
			_ = os.Remove(tableTmpPath)
			cleanupTmp()
			return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_tar_copy_failed: table=%s: %w", table, err)
		}
		_ = tableTmp.Close()
		_ = os.Remove(tableTmpPath)

		summaries = append(summaries, storeBackupTableSummary{
			Name:    table,
			Columns: cols,
			Rows:    rowCount,
			SHA256:  hex.EncodeToString(tableHasher.Sum(nil)),
		})
		totalRows += rowCount
	}

	if err := tw.Close(); err != nil {
		cleanupTmp()
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_tar_close_failed: %w", err)
	}
	if err := gz.Close(); err != nil {
		cleanupTmp()
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_gzip_close_failed: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanupTmp()
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_fsync_failed: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_file_close_failed: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return storeBackupArchiveInfo{}, nil, 0, fmt.Errorf("store_backup_rename_failed: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if err := writeSHA256Sidecar(destPath, digest); err != nil {
		return storeBackupArchiveInfo{}, nil, 0, err
	}
	return storeBackupArchiveInfo{
		File:   filepath.Base(destPath),
		Bytes:  cw.n,
		SHA256: digest,
	}, summaries, totalRows, nil
}

func listGatewayBaseTables(ctx context.Context, db *sql.DB, dialect gatewaySQLDialect) ([]string, error) {
	rows, err := db.QueryContext(ctx, gatewayListBaseTablesSQLFor(dialect))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func tableColumnNamesTx(ctx context.Context, tx *sql.Tx, dialect gatewaySQLDialect, table string) ([]string, error) {
	query, args := gatewayTableColumnsSQLFor(dialect, table)
	rows, err := tx.QueryContext(ctx, query, args...)
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

func archiveGatewayDataDir(dataDir, destPath string) (storeBackupDataDirSummary, error) {
	tmpPath := destPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_open_failed: %w", err)
	}
	cleanupTmp := func() {
		_ = f.Close()
		_ = os.Remove(tmpPath)
	}
	hasher := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, hasher)}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)

	var fileCount int64
	for _, sub := range []string{"skills", "artifacts"} {
		root := filepath.Join(dataDir, sub)
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			cleanupTmp()
			return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_stat_failed: %s: %w", root, err)
		}
		err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(dataDir, p)
			if err != nil {
				return err
			}
			srcFile, err := os.Open(p)
			if err != nil {
				return err
			}
			defer srcFile.Close()
			hdr := &tar.Header{
				Name:    filepath.ToSlash(rel),
				Mode:    int64(info.Mode().Perm()),
				Size:    info.Size(),
				ModTime: info.ModTime().UTC(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if _, err := io.CopyN(tw, srcFile, info.Size()); err != nil {
				return err
			}
			fileCount++
			return nil
		})
		if err != nil {
			cleanupTmp()
			return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_walk_failed: %s: %w", root, err)
		}
	}

	if err := tw.Close(); err != nil {
		cleanupTmp()
		return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_tar_close_failed: %w", err)
	}
	if err := gz.Close(); err != nil {
		cleanupTmp()
		return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_gzip_close_failed: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanupTmp()
		return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_fsync_failed: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_close_failed: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return storeBackupDataDirSummary{}, fmt.Errorf("data_dir_backup_rename_failed: %w", err)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if err := writeSHA256Sidecar(destPath, digest); err != nil {
		return storeBackupDataDirSummary{}, err
	}
	return storeBackupDataDirSummary{
		File:   filepath.Base(destPath),
		Files:  fileCount,
		Bytes:  cw.n,
		SHA256: digest,
	}, nil
}
