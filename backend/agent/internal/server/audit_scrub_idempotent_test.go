package server

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

// TestAuditScrubRerunAndDoubleRollbackAreIdempotentOnMySQL pins the MySQL
// zero-affected-rows rule for the scrub path: writing the value a row already
// holds reports 0 changed rows, which must not be reported as a vanished row.
func TestAuditScrubRerunAndDoubleRollbackAreIdempotentOnMySQL(t *testing.T) {
	db := openAuditScrubMySQL(t)
	auditScrubSeedRow(t, db, auditScrubSeed{Action: "proxy", Summary: "GET /v1/proxy/https/example.com/api?token=alpha"})
	auditScrubSeedRow(t, db, auditScrubSeed{
		Action:  "proxy",
		Summary: "GET /v1/foo",
		Error:   "upstream error: Get \"https://example.com/api?token=alpha\": dial tcp 10.0.0.1:443: timeout",
	})
	ctx := context.Background()
	dir := t.TempDir()
	original := auditScrubContentHash(t, db)

	first := filepath.Join(dir, "first.jsonl")
	var out bytes.Buffer
	code, err := RunAuditScrub(ctx, AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      first,
		Actions:         "proxy",
	}, &out)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("first scrub code=%d err=%v output=%s", code, err, out.String())
	}
	scrubbed := auditScrubContentHash(t, db)
	if scrubbed == original {
		t.Fatalf("first scrub left every row unchanged")
	}

	// The scheme and path survive the scrub, so a second run still selects the
	// same rows and writes values they already hold.
	second := filepath.Join(dir, "second.jsonl")
	out.Reset()
	code, err = RunAuditScrub(ctx, AuditScrubOptions{
		ConfirmDatabase: auditScrubDatabaseName(t, db),
		BackupPath:      second,
		Actions:         "proxy",
	}, &out)
	if err != nil || code != AuditScrubExitSuccess {
		t.Fatalf("second scrub code=%d err=%v output=%s", code, err, out.String())
	}
	if got := auditScrubContentHash(t, db); got != scrubbed {
		t.Fatalf("second scrub changed rows: %s -> %s", scrubbed, got)
	}

	// Restoring the same pre-images twice writes the stored value on the second
	// pass; that is success, not "the row is gone".
	for pass := 1; pass <= 2; pass++ {
		out.Reset()
		code, err = RunAuditScrub(ctx, AuditScrubOptions{
			ConfirmDatabase: auditScrubDatabaseName(t, db),
			BackupPath:      first,
			Rollback:        true,
		}, &out)
		if err != nil || code != AuditScrubExitSuccess {
			t.Fatalf("rollback pass %d code=%d err=%v output=%s", pass, code, err, out.String())
		}
		if got := auditScrubContentHash(t, db); got != original {
			t.Fatalf("rollback pass %d did not restore the pre-image: %s want %s", pass, got, original)
		}
	}
}
