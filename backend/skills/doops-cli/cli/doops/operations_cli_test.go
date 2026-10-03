package main

import (
	"encoding/json"
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
	var cancelStatus, reconcileStatus atomic.Int32
	cancelStatus.Store(http.StatusAccepted)
	reconcileStatus.Store(http.StatusOK)
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
			if r.URL.RawQuery != "id=op-1" {
				t.Errorf("unexpected cancel query: %s", r.URL)
			}
			w.WriteHeader(int(cancelStatus.Load()))
			if cancelStatus.Load() == http.StatusOK {
				_, _ = w.Write([]byte(`{"id":"op-1","canceled":true}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"op-1","cancel_requested":true,"state":"cancel_requested"}`))
			}
		case http.MethodPost:
			var body gatewayAdminOperationReconcileRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode reconcile: %v", err)
			}
			if body.ID != "audit-44" || !body.ConfirmStopped || body.Reason != "operator investigation" ||
				body.Evidence != "process absent; target inspected" {
				t.Errorf("unexpected manual review: %#v", body)
			}
			w.WriteHeader(int(reconcileStatus.Load()))
			if reconcileStatus.Load() == http.StatusForbidden {
				_, _ = w.Write([]byte("administrator required"))
			} else {
				_, _ = w.Write([]byte(`{"id":"audit-44","state":"resolved_manually"}`))
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
	reconcileArgs := []string{
		"admin", "operations", "reconcile", "--target", "test", "--id", "audit-44",
		"--confirm-stopped", "--reason", "operator investigation", "--evidence", "process absent; target inspected",
	}

	t.Run("help_is_explicit", func(t *testing.T) {
		for _, tc := range []struct {
			args []string
			want []string
		}{
			{[]string{"admin", "operations", "reconcile", "--help"}, []string{"Admin-only", "human-reviewed stopped state", "no automated takeover", "-confirm-stopped", "-reason", "-evidence"}},
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
		if requests.Load() != 0 {
			t.Fatal("help sent an HTTP request")
		}
	})

	t.Run("missing_fields_fail_before_HTTP", func(t *testing.T) {
		for _, missing := range []string{"--target", "--id", "--confirm-stopped", "--reason", "--evidence"} {
			args := make([]string, 0, len(reconcileArgs))
			for i := 0; i < len(reconcileArgs); i++ {
				if reconcileArgs[i] == missing {
					if missing != "--confirm-stopped" {
						i++
					}
					continue
				}
				args = append(args, reconcileArgs[i])
			}
			output, err := run(args...)
			if err == nil || !strings.Contains(output, "Error: "+missing) {
				t.Errorf("missing %s: err=%v output=%s", missing, err, output)
			}
		}
		for _, replacement := range []struct{ flag, value string }{
			{"--target", " "}, {"--id", "op-1"}, {"--reason", " \t"}, {"--evidence", " \n"},
		} {
			args := append([]string(nil), reconcileArgs...)
			for i := range args {
				if args[i] == replacement.flag {
					args[i+1] = replacement.value
				}
			}
			output, err := run(args...)
			if err == nil || !strings.Contains(output, "Error: "+replacement.flag) {
				t.Errorf("invalid %s: err=%v output=%s", replacement.flag, err, output)
			}
		}
		if requests.Load() != 0 {
			t.Fatalf("invalid input sent %d HTTP requests", requests.Load())
		}
	})

	t.Run("list_displays_lifecycle", func(t *testing.T) {
		output, err := run("admin", "operations", "list", "--target", "test")
		if err != nil {
			t.Fatalf("list: %v\n%s", err, output)
		}
		for _, want := range []string{"AUDIT_ID", "STATE", "RESOURCE", "BLOCKED_BY", "waiting", "running", "cancel_requested", "outcome_unknown", "target/prod/node", "op-1,op-3", "audit-44"} {
			if !strings.Contains(output, want) {
				t.Errorf("list missing %q:\n%s", want, output)
			}
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

	t.Run("reconcile_records_manual_review_only", func(t *testing.T) {
		output, err := run(reconcileArgs...)
		if err != nil {
			t.Fatalf("reconcile: %v\n%s", err, output)
		}
		for _, want := range []string{"id=audit-44 state=resolved_manually", "human-reviewed stopped state", "no automated takeover"} {
			if !strings.Contains(output, want) {
				t.Errorf("reconcile missing %q:\n%s", want, output)
			}
		}
		reconcileStatus.Store(http.StatusForbidden)
		output, err = run(reconcileArgs...)
		if err == nil || !strings.Contains(output, "administrator required") || strings.Contains(output, "state=resolved_manually") {
			t.Fatalf("non-admin reconciliation was accepted: err=%v output=%s", err, output)
		}
	})
}
