package gatewaycmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/user/doops/agent/internal/server"
)

const auditExportUsage = `doops-gateway audit export (-output-dir <dir> | -output <file.jsonl.gz>)
  [-before <RFC3339> | -older-than <duration> | -all]
  [-include-unresolved] [-include-platform-events]
  [-cos-prefix <prefix>] [-purge -confirm-database <name>] [-reclaim]
  [-keep-local <N>] [-keep-cos-days <N>] [-db <path>]

Stream eligible audit_events (plus linked agent_resource_audit metadata, and
optionally terminal platform_events) into compressed JSONL archives with SHA-256
sidecars. When -cos-prefix is set, uploads to Tencent COS / S3 (using
DOOPS_COS_* environment variables) and verifies byte size with HeadObject.

Fail-closed guarantees:
  - -purge runs ONLY after local archive fsync, SHA-256 sidecar write, and (if
    configured) verified COS upload all succeed.
  - -purge deletes strictly rows covered by the verified export (id <= max_id
    and started_at_ms < cutoff) in bounded 5000-row batches and records an
    audit:purge receipt containing the archive SHA-256 and COS key.
  - On MySQL, -purge or -reclaim requires -confirm-database matching the
    database named in DOOPS_GATEWAY_MYSQL_DSN.
  - -reclaim runs OPTIMIZE TABLE on MySQL (or WAL checkpoint + VACUUM on SQLite)
    so freed InnoDB / SQLite pages are actually returned to disk.

Exit codes:
  0  success
  1  runtime failure (database, filesystem, COS upload, purge, or reclaim error)
  2  usage or precondition failure
`

func auditExport(args []string) {
	fs := flag.NewFlagSet("audit export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path (ignored when DOOPS_GATEWAY_STORE=mysql)")
	confirmDB := fs.String("confirm-database", "", "MySQL database name confirmation (required on MySQL with -purge or -reclaim)")
	outputDir := fs.String("output-dir", "", "Directory to write timestamped archive bundle and manifest.json")
	outputPath := fs.String("output", "", "Single .jsonl.gz output path (mutually exclusive with -output-dir)")
	beforeStr := fs.String("before", "", "Export/purge rows strictly before this RFC3339 timestamp")
	olderThan := fs.Duration("older-than", 0, "Export/purge rows strictly older than this duration (for example 168h for 7 days)")
	all := fs.Bool("all", false, "Use current UTC time as cutoff (export/purge all eligible historical rows)")
	includeUnresolved := fs.Bool("include-unresolved", false, "Also export/purge historical outcome_unknown and non-running rows (never live running/executing rows)")
	includePlatformEvents := fs.Bool("include-platform-events", false, "Also export (and purge/reclaim when requested) terminal platform_events older than cutoff")
	cosPrefix := fs.String("cos-prefix", "", "Optional COS key prefix (for example backups/gateway/audit) using DOOPS_COS_* env")
	purge := fs.Bool("purge", false, "Delete exported rows only after local archive and COS upload verification succeed")
	reclaim := fs.Bool("reclaim", false, "Reclaim freed storage (OPTIMIZE TABLE on MySQL; WAL checkpoint + VACUUM on SQLite)")
	keepLocal := fs.Int("keep-local", -1, "Local archive bundles to keep in -output-dir (-1 keeps all; 0 deletes local bundle after verified COS upload)")
	keepCOSDays := fs.Int("keep-cos-days", 0, "Prune COS objects under -cos-prefix older than this many days (0 keeps all)")
	keepDocDays := fs.Int("keep-doc-days", 0, "Prune local File-First audit document logs (<dataDir>/audit/{records,docs}) older than this many days (0 keeps all)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), auditExportUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(server.GatewayMySQLCopyExitOK)
		}
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n\n", fs.Args())
		fs.Usage()
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	cutoffSpecs := 0
	var beforePtr *time.Time
	if strings.TrimSpace(*beforeStr) != "" {
		cutoffSpecs++
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*beforeStr))
		if err != nil {
			fmt.Fprintf(os.Stderr, "audit export: invalid -before timestamp: %v\n", err)
			os.Exit(server.GatewayMySQLCopyExitUsage)
		}
		utc := parsed.UTC()
		beforePtr = &utc
	}
	if *olderThan > 0 {
		cutoffSpecs++
	}
	if *all {
		cutoffSpecs++
	}
	if cutoffSpecs > 1 {
		fmt.Fprintln(os.Stderr, "audit export: specify at most one of -before, -older-than, or -all")
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}

	keepLocalVal := 0
	deleteLocalAfterUpload := false
	if *keepLocal == 0 {
		deleteLocalAfterUpload = true
	} else if *keepLocal > 0 {
		keepLocalVal = *keepLocal
	} else if *keepLocal < -1 {
		fmt.Fprintln(os.Stderr, "audit export: -keep-local must be -1 (keep all), 0 (delete after COS upload), or a positive count")
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}

	code, err := server.RunGatewayAuditExport(context.Background(), server.GatewayAuditExportOptions{
		DBPath:                 *dbPath,
		ConfirmDatabase:        *confirmDB,
		OutputDir:              *outputDir,
		OutputPath:             *outputPath,
		Before:                 beforePtr,
		OlderThan:              *olderThan,
		All:                    *all,
		IncludeUnresolved:      *includeUnresolved,
		IncludePlatformEvents:  *includePlatformEvents,
		COSPrefix:              *cosPrefix,
		Purge:                  *purge,
		Reclaim:                *reclaim,
		KeepLocal:              keepLocalVal,
		DeleteLocalAfterUpload: deleteLocalAfterUpload,
		KeepCOSDays:            *keepCOSDays,
		KeepDocDays:            *keepDocDays,
	}, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit export: %v\n", err)
	}
	os.Exit(code)
}

const storeBackupUsage = `doops-gateway store backup -output-dir <dir>
  [-exclude-tables t1,t2] [-skip-data-dir]
  [-cos-prefix <prefix>] [-keep-local <N>] [-keep-cos-days <N>] [-db <path>]

Create a consistent point-in-time snapshot of all Gateway database tables paired
with the filesystem data directory (skills/ and artifacts/), write a checksummed
bundle at mode 0600, optionally upload to Tencent COS / S3 with multipart
streaming and HeadObject verification, and prune expired local and remote copies.

What it guarantees:
  - MySQL reads run inside one REPEATABLE READ read-only transaction (no write
    locks) and skip virtual/stored generated columns.
  - Database tables and data directory (skills/ + artifacts/) are bundled as a
    matched pair with per-table row counts and SHA-256 digests in manifest.json.
  - No DSN, password, or COS secret is ever written to output or manifest.json.
  - -keep-local=0 requires -cos-prefix and deletes the local bundle only after
    every uploaded object passes HeadObject byte-size verification.

Exit codes:
  0  success
  1  runtime failure
  2  usage or precondition failure
`

func storeBackup(args []string) {
	fs := flag.NewFlagSet("store backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path (ignored when DOOPS_GATEWAY_STORE=mysql)")
	outputDir := fs.String("output-dir", "", "Directory to write timestamped backup bundle (required)")
	excludeTables := fs.String("exclude-tables", "", "Optional comma-separated table names to exclude (for example audit_events,platform_events)")
	skipDataDir := fs.Bool("skip-data-dir", false, "Skip archiving skills/ and artifacts/ data directory")
	cosPrefix := fs.String("cos-prefix", "", "Optional COS key prefix (for example backups/gateway/daily) using DOOPS_COS_* env")
	keepLocal := fs.Int("keep-local", -1, "Local backup bundles to keep in -output-dir (-1 keeps all; 0 deletes local bundle after verified COS upload)")
	keepCOSDays := fs.Int("keep-cos-days", 0, "Prune COS objects under -cos-prefix older than this many days (0 keeps all)")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), storeBackupUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(server.GatewayMySQLCopyExitOK)
		}
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n\n", fs.Args())
		fs.Usage()
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	var excluded []string
	if strings.TrimSpace(*excludeTables) != "" {
		for _, part := range strings.Split(*excludeTables, ",") {
			if name := strings.TrimSpace(part); name != "" {
				excluded = append(excluded, name)
			}
		}
	}
	keepLocalVal := 0
	deleteLocalAfterUpload := false
	if *keepLocal == 0 {
		deleteLocalAfterUpload = true
	} else if *keepLocal > 0 {
		keepLocalVal = *keepLocal
	} else if *keepLocal < -1 {
		fmt.Fprintln(os.Stderr, "store backup: -keep-local must be -1 (keep all), 0 (delete after COS upload), or a positive count")
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	code, err := server.RunGatewayStoreBackup(context.Background(), server.GatewayStoreBackupOptions{
		DBPath:                 *dbPath,
		OutputDir:              *outputDir,
		COSPrefix:              *cosPrefix,
		ExcludeTables:          excluded,
		SkipDataDir:            *skipDataDir,
		KeepLocal:              keepLocalVal,
		DeleteLocalAfterUpload: deleteLocalAfterUpload,
		KeepCOSDays:            *keepCOSDays,
	}, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store backup: %v\n", err)
	}
	os.Exit(code)
}
