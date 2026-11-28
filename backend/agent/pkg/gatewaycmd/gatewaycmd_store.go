package gatewaycmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/user/doops/agent/internal/server"
)

// store dispatches the store subcommands: SQLite to MySQL cutover, historical
// audit URL scrub, and point-in-time store backup.
func store(args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, storeMySQLCopyUsage)
		fmt.Fprint(os.Stderr, storeAuditScrubUsage)
		fmt.Fprint(os.Stderr, storeBackupUsage)
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	switch args[0] {
	case "mysql-copy":
		storeMySQLCopy(args[1:])
	case "audit-scrub":
		storeAuditScrub(args[1:])
	case "backup":
		storeBackup(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown store command: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, storeMySQLCopyUsage)
		fmt.Fprint(os.Stderr, storeAuditScrubUsage)
		fmt.Fprint(os.Stderr, storeBackupUsage)
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
}

const storeMySQLCopyUsage = `doops-gateway store mysql-copy -source <snapshot.db> -confirm-database <name>
  [-source-data-dir <dir> -target-data-dir <dir>] [-dry-run]

Move an offline SQLite snapshot of one Gateway database into an empty MySQL
database, and move the filesystem half of the store (managed artifacts and Skill
packages) when the data directories are given. This is a MIGRATION tool, not a
day-to-day command: it is meant for the one cutover from the SQLite backend to
the MySQL backend and for rehearsing it.

What it guarantees:
  - The source is the read-only snapshot gateway-sqlite-snapshot.py produced. It
    is opened with mode=ro and query_only=1, and PRAGMA integrity_check must
    return ok. The source database is never modified or deleted, so the SQLite
    file you took the snapshot from stays intact and remains the rollback.
  - The destination DSN is never an argument and never printed. It is read from
    DOOPS_GATEWAY_MYSQL_DSN only, so it cannot leak through shell history, the
    process table or a log line.
  - -confirm-database must equal the database named by the DSN, character for
    character. A typo stops the run before anything is written.
  - The destination is either an empty database (this command installs the
    native schema first) or one that already carries that schema, and it must
    hold no data. This command never overwrites, merges or clears existing rows,
    and it never resets a database that already holds data.
  - Tables the snapshot never had are accepted only while they are empty and
    are bucketed by one shared rule: this version's feature modules are
    native_schema_only, retired compatibility tables are
    legacy_destination_only. The dry run and the real run print the same names,
    buckets and counts. A single row in any of them is a precondition failure
    that names the table and the row count.
  - Rows move table by table inside one transaction. Any failure rolls the whole
    copy back and names the table that failed. Success is verified against the
    source: per-table row counts are compared again and a mismatch is a failure.
  - Self-referencing foreign keys (inventory_machines.host_asset_id) are handled
    data-aware in the same transaction: the column is written NULL first, then
    backfilled once every parent exists. The constraint stays in place and a real
    parent cycle is refused by row id. No manual ALTER TABLE is needed.
  - With -source-data-dir and -target-data-dir, the artifacts/ and skills/
    trees move with the rows. Every indexed artifact and Skill package must
    exist, is copied byte for byte, and is verified by size and sha256. A
    missing file rolls the whole cutover back and names the run and item. The
    source data directory is never modified or deleted.

Rehearse first: point the command at a fresh empty MySQL database, copy, start
the Gateway against it and check the console before the production window.

Flags:
  -source string             SQLite snapshot to copy from (required)
  -confirm-database string   Database name that must match the DSN database (required)
  -source-data-dir string    Source data directory holding artifacts/ and
                             skills/ (absolute; give both data-dir flags or
                             neither). The production cutover must give both:
                             a database-only copy leaves every artifact and
                             Skill package behind
  -target-data-dir string    Destination data directory (absolute, different,
                             and without artifacts/ or skills/)
  -dry-run                   Print the copy plan and write nothing at all: no
                             INSERT, no DDL, no file copy and no transaction
                             left behind. On
                             an empty database it also projects the schema the
                             real run would install and the verdict it would
                             reach, so the exit code already means "the cutover
                             would be accepted" or "it would be refused, here
                             is why"

The destination DSN comes from DOOPS_GATEWAY_MYSQL_DSN only:
  DOOPS_GATEWAY_MYSQL_DSN='root@tcp(127.0.0.1:3306)/doops_gateway?parseTime=true' \
    doops-gateway store mysql-copy -source gateway-snapshot.db -confirm-database doops_gateway -dry-run

Exit codes:
  0  success
  1  runtime failure (the copy ran and failed; the transaction was rolled back)
  2  usage or precondition failure; a -dry-run exits with the same code the
     real run would end with, so a rehearsal gate can trust it
`

func storeMySQLCopy(args []string) {
	fs := flag.NewFlagSet("store mysql-copy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	source := fs.String("source", "", "SQLite snapshot produced by gateway-sqlite-snapshot.py; opened read-only")
	confirm := fs.String("confirm-database", "", "Database name that must equal the database named by DOOPS_GATEWAY_MYSQL_DSN")
	sourceDataDir := fs.String("source-data-dir", "", "Absolute source data directory holding artifacts/ and skills/")
	targetDataDir := fs.String("target-data-dir", "", "Absolute destination data directory; must be empty")
	dryRun := fs.Bool("dry-run", false, "Print the copy plan and write nothing")
	fs.Usage = func() { fmt.Fprint(fs.Output(), storeMySQLCopyUsage) }
	switch err := fs.Parse(args); {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		return
	default:
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n\n", fs.Arg(0))
		fmt.Fprint(os.Stderr, storeMySQLCopyUsage)
		os.Exit(server.GatewayMySQLCopyExitUsage)
	}
	code, err := server.RunGatewayMySQLCopy(context.Background(), server.GatewayMySQLCopyOptions{
		Source:          *source,
		ConfirmDatabase: *confirm,
		SourceDataDir:   *sourceDataDir,
		TargetDataDir:   *targetDataDir,
		DryRun:          *dryRun,
	}, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(code)
}
