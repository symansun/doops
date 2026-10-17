package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAdminOperationsCLI(t *testing.T) {
	var requests atomic.Int32
	var cancelStatus atomic.Int32
	cancelStatus.Store(http.StatusAccepted)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer admin-token" {
			t.Errorf("missing admin login: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/v1/admin/operations" {
			t.Errorf("unexpected endpoint: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(operationsListFixture))
		case http.MethodDelete:
			if r.URL.RawQuery != "cluster=prod&id=op-1&instance=node" {
				t.Errorf("unexpected cancel query: %s", r.URL)
			}
			w.WriteHeader(int(cancelStatus.Load()))
			if cancelStatus.Load() == http.StatusOK {
				_, _ = w.Write([]byte(`{"id":"op-1","canceled":true}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"op-1","cancel_requested":true,"state":"cancel_requested"}`))
			}
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer gateway.Close()

	binary := buildDoopsCLIBinary(t)
	root := t.TempDir()
	config := filepath.Join(root, "config.json")
	t.Setenv("DOOPS_CONFIG", config)
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "prod", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "admin-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		command := exec.Command(binary, args...)
		command.Env = os.Environ()
		output, err := command.CombinedOutput()
		return string(output), err
	}
	t.Run("help_is_explicit", func(t *testing.T) {
		for _, tc := range []struct {
			args []string
			want []string
		}{
			{[]string{"admin", "operations", "cancel", "--help"}, []string{"request cancellation", "does not confirm it has stopped"}},
			{[]string{"unlock", "--help"}, []string{"only when idle", "does not force-release active work"}},
			{[]string{"exec", "--help"}, []string{"one streaming background task RPC", "no status polling or task retries"}},
		} {
			output, err := run(tc.args...)
			if err != nil {
				t.Fatalf("help %v: %v\n%s", tc.args, err, output)
			}
			for _, want := range tc.want {
				if !strings.Contains(output, want) {
					t.Errorf("help %v missing %q:\n%s", tc.args, want, output)
				}
			}
		}
		output, err := run("--help")
		if err != nil || strings.Contains(output, "reconcile") || strings.Contains(output, "confirm-stopped") {
			t.Fatalf("help still advertises human reconciliation: err=%v output=%s", err, output)
		}
		if requests.Load() != 0 {
			t.Fatal("help sent an HTTP request")
		}
	})

	t.Run("reconcile_is_rejected_before_HTTP", func(t *testing.T) {
		for _, args := range [][]string{
			{"admin", "operations", "reconcile"},
			{"admin", "operations", "reconcile", "--help"},
			{"admin", "operations", "reconcile", "--target", "test", "--id", "audit-44", "--confirm-stopped", "--reason", "operator investigation", "--evidence", "process absent; target inspected"},
		} {
			output, err := run(args...)
			if err == nil || !strings.Contains(output, "Usage:") || strings.Contains(output, "reconcile") ||
				strings.Contains(output, "confirm-stopped") || strings.Contains(output, "resolved_manually") {
				t.Errorf("removed command accepted or advertised: args=%v err=%v output=%s", args, err, output)
			}
		}
		if requests.Load() != 0 {
			t.Fatalf("removed reconcile command sent %d HTTP requests", requests.Load())
		}
	})

	t.Run("list_displays_lifecycle", func(t *testing.T) {
		output, err := run("admin", "operations", "list", "--target", "test")
		if err != nil {
			t.Fatalf("list: %v\n%s", err, output)
		}
		for _, want := range []string{"AUDIT_ID", "STATE", "REQUESTED_RESOURCE", "EFFECTIVE_LOCK", "BLOCKED_BY", "waiting", "running", "cancel_requested", "outcome_unknown", "op-1,op-3", "audit-44"} {
			if !strings.Contains(output, want) {
				t.Errorf("list missing %q:\n%s", want, output)
			}
		}
		wantLocks := map[string][2]string{
			"op-1":     {"target-mutation:prod/node", "session-mutation:prod/node/release"},
			"op-2":     {"-", "-"},
			"op-3":     {"target-mutation:prod/node", "-"},
			"audit-44": {"-", "-"},
		}
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			if want, ok := wantLocks[fields[0]]; ok {
				if fields[3] != want[0] || fields[4] != want[1] {
					t.Errorf("%s requested/effective = %q/%q, want %q/%q", fields[0], fields[3], fields[4], want[0], want[1])
				}
				delete(wantLocks, fields[0])
			}
		}
		if len(wantLocks) != 0 {
			t.Errorf("missing operation rows: %v", wantLocks)
		}
	})

	t.Run("cancel_is_only_requested", func(t *testing.T) {
		output, err := run("admin", "operations", "cancel", "--target", "test", "--id", "op-1")
		if err != nil || !strings.Contains(output, "state=cancel_requested") ||
			!strings.Contains(output, "not confirmed stopped") || strings.Contains(output, "Canceled operation") {
			t.Fatalf("cancel: err=%v output=%s", err, output)
		}
		cancelStatus.Store(http.StatusOK)
		output, err = run("admin", "operations", "cancel", "--target", "test", "--id", "op-1")
		if err == nil || !strings.Contains(output, "Error:") || strings.Contains(output, "Canceled operation") {
			t.Fatalf("legacy cancel was accepted: err=%v output=%s", err, output)
		}
	})

}
