package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// redeliverAuditRow is the audit shape a failed reconnect redelivery must leave.
type redeliverAuditRow struct {
	ID             int64
	UserID         string
	Cluster        string
	Instance       string
	CredentialID   string
	Action         string
	Status         string
	Error          string
	CommandSummary string
	Tail           string
}

func lastLogLineContaining(logs, needle string) string {
	line := ""
	for _, candidate := range strings.Split(logs, "\n") {
		if strings.Contains(candidate, needle) {
			line = candidate
		}
	}
	return strings.TrimSpace(line)
}

func redeliverAuditRows(t *testing.T, f *executionLifecycleFixture) []redeliverAuditRow {
	t.Helper()
	rows, err := f.store.db.Query("SELECT id, user_id, cluster, instance, credential_id, action, status, error, command_summary, tail FROM audit_events WHERE action = ? ORDER BY id", "credential:redeliver")
	if err != nil {
		t.Fatalf("query redelivery audit rows: %v", err)
	}
	defer rows.Close()
	var out []redeliverAuditRow
	for rows.Next() {
		var row redeliverAuditRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.Cluster, &row.Instance, &row.CredentialID,
			&row.Action, &row.Status, &row.Error, &row.CommandSummary, &row.Tail); err != nil {
			t.Fatalf("scan redelivery audit row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate redelivery audit rows: %v", err)
	}
	return out
}

func promoteRegistryAuth(t *testing.T, f *executionLifecycleFixture) (string, CredentialVersion, CredentialGrant) {
	t.Helper()
	base, version, grant := registryAuthCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify", registryAuthVerifyBody(version.ID), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote", "{\"version_id\":\""+version.ID+"\"}", http.StatusOK)
	return base, version, grant
}

// Given a target with an active registry credential and a connected Agent,
// When the Agent reconnects and the redelivery fails,
// Then the failure leaves exactly one audit event and never a payload.
func TestRegistryAuthRedeliveryFailureLeavesOneAuditRow(t *testing.T) {
	f, authfile := registryAuthLifecycleFixture(t)
	promoteRegistryAuth(t, f)

	logs := captureGatewayLogs(t)
	// A read-only authfile directory makes the Agent's own authfile write fail,
	// which is the only failure a reconnect delivery can hit: the Secret is not
	// touched on this path.
	authDir := filepath.Dir(authfile)
	if err := os.Chmod(authDir, 0o500); err != nil {
		t.Fatalf("make the authfile directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(authDir, 0o700) })
	stop := tunnelE2EStartAgent(t, f, "dev", "local") // a real reconnect replaces the first Agent
	defer stop()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(logs.String(), "registry auth delivery:") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	line := lastLogLineContaining(logs.String(), "registry auth delivery:")
	if line == "" {
		t.Fatalf("no reconnect delivery log line; captured log:\n%s", logs.String())
	}
	if !strings.Contains(line, "[gateway] registry auth delivery: dev/local category=") {
		t.Fatalf("unexpected reconnect delivery log line: %q", line)
	}

	rows := redeliverAuditRows(t, f)
	if len(rows) != 1 {
		t.Fatalf("audit_events rows for credential:redeliver = %d, want exactly 1; gateway log line: %q", len(rows), line)
	}
	row := rows[0]
	if row.Status != "error" || row.Error == "" {
		t.Fatalf("redelivery audit row is not a stable error: %#v", row)
	}
	if !strings.Contains(line, "category="+row.Error) {
		t.Fatalf("audit error %q does not match the logged category: %q", row.Error, line)
	}
	if row.Cluster != "dev" || row.Instance != "local" {
		t.Fatalf("redelivery audit row lost its target: %#v", row)
	}
	if row.UserID != "system" {
		t.Fatalf("redelivery audit actor = %q, want the system actor", row.UserID)
	}
	encoded := strings.Join([]string{row.CommandSummary, row.Tail, row.Error}, " ")
	if strings.Contains(encoded, registryAuthCanary) {
		t.Fatalf("registry payload leaked into the redelivery audit row: %#v", row)
	}
}

// Given a target with an active model provider and a connected Agent,
// When the reconnect redelivery of that provider fails,
// Then the same audit action records it, with the model kind in the summary.
func TestModelProviderRedeliveryFailureLeavesOneAuditRow(t *testing.T) {
	f, _ := modelLifecycleSetup(t, nil)
	base, version, _ := createModelProviderCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify", modelVerifyBody(version.ID, "dev", "local"), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote", "{\"version_id\":\""+version.ID+"\"}", http.StatusOK)

	logs := captureGatewayLogs(t)
	probeModelProvider = func(context.Context, modelProviderPayload) error { return errors.New("model_unavailable") }
	stop := tunnelE2EStartAgent(t, f, "dev", "local")
	defer stop()

	requireEventually(t, 10*time.Second, func() bool {
		return strings.Contains(logs.String(), "model provider delivery:")
	})
	line := lastLogLineContaining(logs.String(), "model provider delivery:")

	rows := redeliverAuditRows(t, f)
	if len(rows) != 1 {
		t.Fatalf("audit_events rows for credential:redeliver = %d, want exactly 1; gateway log line: %q", len(rows), line)
	}
	if rows[0].Error != "model_unavailable" || rows[0].Status != "error" {
		t.Fatalf("model redelivery audit row = %#v, want error model_unavailable", rows[0])
	}
	if !strings.Contains(rows[0].CommandSummary, "model provider") {
		t.Fatalf("model redelivery summary does not name the delivery kind: %#v", rows[0])
	}
	if strings.Contains(rows[0].CommandSummary+rows[0].Tail+rows[0].Error, modelLifecycleCanary) {
		t.Fatalf("model payload leaked into the redelivery audit row: %#v", rows[0])
	}
}

// Given a successful reconnect redelivery,
// When nothing fails,
// Then no audit event is written: success is the normal state and would flood
// the audit trail on every reconnect.
func TestRedeliverySuccessLeavesNoAuditRow(t *testing.T) {
	f, _ := registryAuthLifecycleFixture(t)
	promoteRegistryAuth(t, f)

	// The fixture's Agent is connected; a successful redelivery of the
	// promoted credential must stay silent.
	if category := f.hub.deliverRegistryAuthFilesTo(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("successful redelivery category = %q", category)
	}
	if rows := redeliverAuditRows(t, f); len(rows) != 0 {
		t.Fatalf("successful redelivery wrote %d audit rows, want 0: %#v", len(rows), rows)
	}
}

// Given an unenrolled session or an Agent that predates the authfile field,
// When credential delivery is skipped,
// Then the skip stays in the log only: audit records delivery failures, not
// the normal or intentionally skipped cases.
func TestRedeliverySkipsLeaveNoAuditRow(t *testing.T) {
	f, _ := registryAuthLifecycleFixture(t)
	promoteRegistryAuth(t, f)

	older := &GatewayAgent{Key: "dev/local", Cluster: "dev", Instance: "local", RegistryAuthfile: false, TokenID: "token"}
	f.hub.deliverAgentCredentials(older)
	unenrolled := &GatewayAgent{Key: "dev/unenrolled", Cluster: "dev", Instance: "unenrolled", RegistryAuthfile: true}
	f.hub.deliverAgentCredentials(unenrolled)
	if rows := redeliverAuditRows(t, f); len(rows) != 0 {
		t.Fatalf("skipped redelivery wrote %d audit rows, want 0: %#v", len(rows), rows)
	}
}
