package gatewaycmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/internal/server"
)

func TestGatewayBackupAndExportHelperProcess(t *testing.T) {
	if os.Getenv("DOOPS_GATEWAY_BACKUP_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			Run(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func runGatewayBackupHelper(t *testing.T, args ...string) (string, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmdArgs := append([]string{"-test.run=^TestGatewayBackupAndExportHelperProcess$", "--"}, args...)
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Env = append(os.Environ(), "DOOPS_GATEWAY_BACKUP_HELPER=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestGatewayBackupAndExportCLI(t *testing.T) {
	tmpRoot := t.TempDir()
	dbPath := filepath.Join(tmpRoot, "gateway.db")
	store, err := server.OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour)
	id, err := store.StartAudit(server.AuditEvent{
		Action:         server.ActionExec,
		Cluster:        "cli",
		Instance:       "local",
		CommandSummary: "cli-export-test",
		StartedAt:      old,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAudit(id, server.AuditFinish{Status: "success", Tail: "done", EndedAt: old.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// 1. Help smoke tests
	for _, args := range [][]string{
		{"audit", "export", "-h"},
		{"store", "backup", "-h"},
	} {
		out, err := runGatewayBackupHelper(t, args...)
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
		if len(strings.TrimSpace(out)) == 0 {
			t.Fatalf("%v produced empty help", args)
		}
	}

	// 2. Real audit export + purge + reclaim
	auditOutDir := filepath.Join(t.TempDir(), "audit-out")
	out, err := runGatewayBackupHelper(t, "audit", "export",
		"-db", dbPath,
		"-output-dir", auditOutDir,
		"-older-than", "1h",
		"-purge",
		"-reclaim",
	)
	if err != nil {
		t.Fatalf("audit export failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "audit_export: rows=1") || !strings.Contains(out, "audit_purged: deleted=1") || !strings.Contains(out, "storage_reclaimed:") {
		t.Fatalf("unexpected audit export output:\n%s", out)
	}

	// 3. Real store backup
	backupOutDir := filepath.Join(t.TempDir(), "backup-out")
	out, err = runGatewayBackupHelper(t, "store", "backup",
		"-db", dbPath,
		"-output-dir", backupOutDir,
		"-exclude-tables", "audit_events",
	)
	if err != nil {
		t.Fatalf("store backup failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "db_backup: backend=sqlite") {
		t.Fatalf("unexpected store backup output:\n%s", out)
	}
	entries, err := os.ReadDir(backupOutDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 backup bundle in %s, got %d (err=%v)", backupOutDir, len(entries), err)
	}
}
