package gatewaycmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/user/doops/agent/internal/server"
)

// storeAuditScrub is the CLI wrapper around server.RunAuditScrub. All security
// decisions live in the tested core; this file only parses flags and translates
// the returned exit code.
func storeAuditScrub(args []string) {
	fs := flag.NewFlagSet("store audit-scrub", flag.ContinueOnError)
	confirm := fs.String("confirm-database", "", "destination database name; must equal the DSN database exactly")
	backup := fs.String("backup", "", "JSONL pre-image path; created 0600 and written before the first change")
	dryRun := fs.Bool("dry-run", false, "print candidate counts only; issue no UPDATE and write nothing")
	rollback := fs.Bool("rollback", false, "restore rows from a JSONL pre-image in reverse order")
	actions := fs.String("actions", "proxy,pull,push", "comma-separated audit actions; only proxy, pull, push are accepted")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), storeAuditScrubUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(server.AuditScrubExitSuccess)
		}
		os.Exit(server.AuditScrubExitUsage)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n\n", fs.Args())
		fs.Usage()
		os.Exit(server.AuditScrubExitUsage)
	}
	code, err := server.RunAuditScrub(context.Background(), server.AuditScrubOptions{
		ConfirmDatabase: *confirm,
		BackupPath:      *backup,
		Actions:         *actions,
		DryRun:          *dryRun,
		Rollback:        *rollback,
	}, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "audit-scrub: %v\n", err)
	}
	os.Exit(code)
}

const storeAuditScrubUsage = `doops-gateway store audit-scrub -confirm-database <name> -backup <path>
  [-actions proxy,pull,push] [-dry-run | -rollback]

Rewrite historical audit URL material in an already-migrated MySQL database.
This is a MIGRATION tool, not a day-to-day command. Rehearse it with -dry-run
and a throwaway copy of the target before using it against production.

What it changes:
  - Only audit_events.command_summary and audit_events.error are rewritten.
  - command_summary keeps its method and path; the query becomes ?<redacted>.
  - error keeps every URL's scheme, host and path; userinfo, query and fragment
    are removed. Surrounding error text is preserved.
  - It never deletes audit rows and never clears either column. Only the actions
    proxy, pull and push are accepted; wildcards and unknown action names exit 2.
  - A malformed command summary or embedded URL fails closed: the batch is
    rolled back and the error names the row id and column.

Safety:
  - The destination DSN is never an argument and never printed. It is read from
    DOOPS_GATEWAY_MYSQL_DSN only, so it cannot leak through shell history, the
    process table or a log line.
  - -confirm-database must equal the database named by the DSN, character for
    character. A typo stops the run before anything is written.
  - -backup is required. It is a JSONL pre-image of every old value, created
    with mode 0600 before that batch is changed. Keep it until acceptance, then
    destroy it: it contains the old query strings. -rollback applies it in
    reverse order.
  - -dry-run starts a read-only transaction, prints per-column candidate counts
    and the estimated rewritten row count, and issues no UPDATE.

Exit codes:
  0  success
  1  runtime failure (connection, statement, fail-closed parse, post-check)
  2  usage or precondition failure (flags, database name, schema, backup)

The destination must already be a MySQL Gateway database with the native
audit_events table. Start the Gateway once with DOOPS_GATEWAY_STORE=mysql to
install it.
`
