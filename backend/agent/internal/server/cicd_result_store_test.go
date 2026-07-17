package server

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestCICDResultStorePersistsOneTerminalResponse(t *testing.T) {
	t.Setenv("DOOPS_CICD_RESULT_DIR", t.TempDir())
	store := newCICDResultStore()
	run := testCICDResultRun()

	result, created, err := store.begin(run)
	if err != nil || !created || result.State != cicdResultClaimed {
		t.Fatalf("begin result=%#v created=%t err=%v", result, created, err)
	}
	response := api.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      float64(7),
		Result:  map[string]interface{}{"structuredContent": map[string]interface{}{"status": "Converged"}},
	}
	if err := store.complete(run, response); err != nil {
		t.Fatalf("complete: %v", err)
	}

	reloaded := newCICDResultStore()
	stored, err := reloaded.get(run)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.State != cicdResultCompleted || stored.Response.ID != nil {
		t.Fatalf("stored result = %#v", stored)
	}
	digest, _ := cicdRunDigest(run)
	info, err := os.Stat(reloaded.path(run.ID, digest))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("result mode = %o, want 600", info.Mode().Perm())
	}

	existing, created, err := reloaded.begin(run)
	if err != nil || created || existing.State != cicdResultCompleted {
		t.Fatalf("repeat begin result=%#v created=%t err=%v", existing, created, err)
	}
}

func TestCICDResultStoreRejectsUnsafeRunID(t *testing.T) {
	t.Setenv("DOOPS_CICD_RESULT_DIR", t.TempDir())
	store := newCICDResultStore()
	for _, runID := range []string{"", "../escape", "a/b", "has space"} {
		run := testCICDResultRun()
		run.ID = runID
		if _, _, err := store.begin(run); err == nil {
			t.Fatalf("unsafe run id %q was accepted", runID)
		}
	}
}

func TestCICDResultStoreRejectsMismatchedBinding(t *testing.T) {
	t.Setenv("DOOPS_CICD_RESULT_DIR", t.TempDir())
	store := newCICDResultStore()
	run := testCICDResultRun()
	if _, _, err := store.begin(run); err != nil {
		t.Fatalf("begin: %v", err)
	}
	mismatched := run
	mismatched.Instance = "other-instance"
	if _, err := store.get(mismatched); !os.IsNotExist(err) {
		t.Fatalf("mismatched binding error = %v, want not exist", err)
	}
}

func TestCICDResultStoreDetectsClaimFromPreviousRuntime(t *testing.T) {
	t.Setenv("DOOPS_CICD_RESULT_DIR", t.TempDir())
	store := newCICDResultStore()
	run := testCICDResultRun()
	result, _, err := store.begin(run)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	result.RuntimeID = "previous-runtime"
	store.mu.Lock()
	if err := store.writeLocked(result); err != nil {
		store.mu.Unlock()
		t.Fatalf("rewrite runtime: %v", err)
	}
	store.mu.Unlock()
	stored, err := store.get(run)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.RuntimeID == agentProcessRuntimeID {
		t.Fatalf("claim runtime was not changed: %#v", stored)
	}
}

func testCICDResultRun() CredentialRun {
	return CredentialRun{
		APIVersion:     "doops.sh/v2",
		Kind:           "CredentialRun",
		ID:             "credrun_store_test",
		Mode:           "apply",
		Cluster:        "doops-edu",
		Instance:       "release-runner",
		Target:         "gw-edu-release-runner",
		Session:        "doops-oilan-release",
		SourceRevision: strings.Repeat("a", 40),
		Template:       "doops-oilan-doops-agent",
		Project:        "doops",
		Environment:    "oilan",
		CreatedAt:      time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	}
}
