package gatewaycmd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/internal/server"
)

func TestGatewayAuditPurgeCLI(t *testing.T) {
	for _, failure := range []string{"", "insert", "finish"} {
		name := failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "gateway.db")
			store, err := server.OpenGatewayStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			cutoff := time.Date(2026, 11, 10, 8, 0, 0, 0, time.FixedZone("offset", 8*60*60))
			old := cutoff.Add(-time.Hour)
			deletedIDs := map[int64]bool{}
			for _, fixture := range []struct {
				status    string
				action    server.GatewayAction
				startedAt time.Time
				operation string
				tail      string
				deleted   bool
			}{
				{"success", server.ActionExec, old, "", "", true},
				{"error", server.ActionExec, old, "", "", true},
				{"resolved_manually", server.ActionExec, old, "", "", true},
				{"running", server.ActionExec, old, "", "", false},
				{"executing", server.ActionExec, old, "", "", false},
				{"outcome_unknown", server.ActionExec, old, "", "", false},
				{"accepted", server.ActionAgentUpgrade, old, "pending", `{"result":{"job":"pending-job","outcome":"accepted"}}`, false},
				{"observing", server.ActionAgentUpgrade, old, "pending", `{"result":{"job":"pending-job","outcome":"running"}}`, false},
				{"success", server.ActionExec, cutoff, "", "", false},
				{"success", server.ActionExec, cutoff.Add(time.Hour), "", "", false},
				{"success", "audit:purge", old, "", `{"deleted":0}`, false},
				{"accepted", server.ActionAgentUpgrade, old, "completed", `{"result":{"job":"completed-job","outcome":"accepted"}}`, true},
				{"observing", server.ActionAgentUpgrade, old, "completed", `{"result":{"job":"completed-job","outcome":"running"}}`, true},
				{"success", server.ActionAgentUpgrade, old, "completed", `{"result":{"job":"completed-job","outcome":"succeeded"}}`, true},
			} {
				id, err := store.StartAudit(server.AuditEvent{
					Action: fixture.action, StartedAt: fixture.startedAt,
					Cluster: "synthetic", Instance: "local", CorrelationID: fixture.operation,
					CommandSummary: "synthetic purge fixture",
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.FinishAudit(id, server.AuditFinish{
					Status: fixture.status, Tail: fixture.tail, EndedAt: fixture.startedAt.Add(time.Second),
				}); err != nil {
					t.Fatal(err)
				}
				if fixture.deleted {
					deletedIDs[id] = true
				}
			}
			originals, err := store.ListAudit(100)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				db, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				trigger := `CREATE TRIGGER fail_purge_receipt BEFORE INSERT ON audit_events
					WHEN NEW.action = 'audit:purge' BEGIN SELECT RAISE(FAIL, 'synthetic receipt unavailable'); END`
				if failure == "finish" {
					trigger = `CREATE TRIGGER fail_purge_receipt BEFORE UPDATE OF status ON audit_events
						WHEN OLD.action = 'audit:purge' BEGIN SELECT RAISE(FAIL, 'synthetic receipt unavailable'); END`
				}
				if _, err := db.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, commandErr := runGatewayAuditPurgeCLI(t, dbPath, cutoff)
			store, err = server.OpenGatewayStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			events, err := store.ListAudit(100)
			if err != nil {
				t.Fatal(err)
			}
			if failure != "" {
				var exitErr *exec.ExitError
				if !errors.As(commandErr, &exitErr) || exitErr.ExitCode() != 1 || stdout != "" || !strings.Contains(stderr, "synthetic receipt unavailable") {
					t.Errorf("receipt failure: error=%v stdout=%q stderr=%q", commandErr, stdout, stderr)
				}
				if !reflect.DeepEqual(events, originals) {
					t.Errorf("failed receipt changed original audit events: got %d, want %d", len(events), len(originals))
				}
				return
			}
			wantOutput := fmt.Sprintf("deleted=%d before=%s\n", len(deletedIDs), cutoff.UTC().Format(time.RFC3339))
			if commandErr != nil || stderr != "" || stdout != wantOutput {
				t.Fatalf("purge: error=%v stdout=%q stderr=%q, want %q", commandErr, stdout, stderr, wantOutput)
			}
			if len(events) != len(originals)-len(deletedIDs)+1 {
				t.Fatalf("purge must retain protected events and add one receipt: got %d, want %d", len(events), len(originals)-len(deletedIDs)+1)
			}
			byID := map[int64]server.AuditEvent{}
			for _, event := range events {
				byID[event.ID] = event
			}
			for _, original := range originals {
				actual, exists := byID[original.ID]
				if deletedIDs[original.ID] {
					if exists {
						t.Errorf("eligible event %d was retained", original.ID)
					}
				} else if !exists || !reflect.DeepEqual(actual, original) {
					t.Errorf("protected event %d (%s) changed or disappeared", original.ID, original.Status)
				}
				delete(byID, original.ID)
			}
			if len(byID) != 1 {
				t.Fatalf("want exactly one new receipt, got %#v", byID)
			}
			for _, receipt := range byID {
				assertGatewayAuditPurgeReceipt(t, receipt, cutoff, int64(len(deletedIDs)))
			}
			stdout, stderr, commandErr = runGatewayAuditPurgeCLI(t, dbPath, cutoff)
			wantOutput = "deleted=0 before=" + cutoff.UTC().Format(time.RFC3339) + "\n"
			if commandErr != nil || stderr != "" || stdout != wantOutput {
				t.Fatalf("repeat purge: error=%v stdout=%q stderr=%q, want %q", commandErr, stdout, stderr, wantOutput)
			}
			repeated, err := store.ListAudit(100)
			if err != nil {
				t.Fatal(err)
			}
			if len(repeated) != len(events)+1 || !reflect.DeepEqual(repeated[1:], events) {
				t.Fatal("zero-deletion purge did not preserve existing events and append exactly one receipt")
			}
			assertGatewayAuditPurgeReceipt(t, repeated[0], cutoff, 0)
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var remoteIdentities int
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM users) + (SELECT COUNT(*) FROM tokens) + (SELECT COUNT(*) FROM grants)`).Scan(&remoteIdentities); err != nil {
				t.Fatal(err)
			}
			if remoteIdentities != 0 {
				t.Fatalf("local purge created remote identity or authority: %d rows", remoteIdentities)
			}
		})
	}
}

func assertGatewayAuditPurgeReceipt(t *testing.T, receipt server.AuditEvent, cutoff time.Time, deleted int64) {
	t.Helper()
	if receipt.Action != "audit:purge" || receipt.Status != "success" || receipt.Error != "" || receipt.StartedAt.IsZero() || receipt.EndedAt.Before(receipt.StartedAt) {
		t.Fatalf("invalid purge receipt: %#v", receipt)
	}
	if receipt.UserID != fmt.Sprintf("local:uid:%d", os.Geteuid()) || receipt.TokenID != "" {
		t.Errorf("receipt must identify the effective local OS operator without a remote token: user=%q token=%q", receipt.UserID, receipt.TokenID)
	}
	if receipt.CommandSummary != "audit:purge before="+cutoff.UTC().Format(time.RFC3339Nano) || receipt.Session != "audit-purge" {
		t.Errorf("receipt command metadata: %#v", receipt)
	}
	var evidence struct {
		Before  string `json:"before"`
		Deleted int64  `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(receipt.Tail), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Before != cutoff.UTC().Format(time.RFC3339Nano) || evidence.Deleted != deleted {
		t.Errorf("receipt evidence=%#v, want before=%s deleted=%d", evidence, cutoff.UTC().Format(time.RFC3339Nano), deleted)
	}
}

func runGatewayAuditPurgeCLI(t *testing.T, dbPath string, cutoff time.Time) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGatewayAuditPurgeHelperProcess$", "--", "audit", "purge", "-db", dbPath, "-before", cutoff.Format(time.RFC3339))
	command.Env = append(os.Environ(), "DOOPS_GATEWAY_AUDIT_PURGE_HELPER=1", "USER=synthetic-remote-admin", "LOGNAME=synthetic-remote-admin", "SUDO_USER=synthetic-remote-admin")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("local audit purge did not exit promptly: %v", ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

func TestGatewayAuditPurgeHelperProcess(t *testing.T) {
	if os.Getenv("DOOPS_GATEWAY_AUDIT_PURGE_HELPER") != "1" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			Run(os.Args[index+1:])
			os.Exit(0)
		}
	}
	t.Fatal("missing helper command")
}
