package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

const operationsListFixture = `{"operations":[
	{"id":"op-1","audit_id":41,"state":"running","resource":"target/prod/node","blocked_by":[],"user_id":"admin","token_id":"token-1","cluster":"prod","instance":"node","action":"exec","session":"release","command_summary":"sleep 10","kind":"rpc","started_at":"2026-10-03T01:00:00Z","age_seconds":10},
	{"id":"op-2","audit_id":42,"state":"waiting","resource":"target/prod/node","blocked_by":["op-1","op-3"],"cluster":"prod","instance":"node"},
	{"id":"op-3","audit_id":43,"state":"cancel_requested","resource":"target/prod/node","cluster":"prod","instance":"node"},
	{"id":"audit-44","audit_id":44,"state":"outcome_unknown","resource":"target/prod/node","cluster":"prod","instance":"node"}
]}`

func TestGatewayAdminOperationsListParsesLifecycleFields(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/doops/v1/admin/operations" ||
			r.URL.RawQuery != "cluster=prod&instance=node" || r.Header.Get("Authorization") != "Bearer admin-token" {
			t.Errorf("unexpected list request: %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(operationsListFixture))
	}))
	defer gateway.Close()

	ops, err := GatewayAdminOperationsList(gateway.URL+"/doops", "admin-token", "prod", "node")
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 4 {
		t.Fatalf("expected four operation states, got %#v", ops)
	}
	for i, state := range []string{"running", "waiting", "cancel_requested", "outcome_unknown"} {
		if ops[i].State != state || ops[i].AuditID != int64(41+i) || ops[i].Resource != "target/prod/node" {
			t.Fatalf("lifecycle fields lost: %#v", ops[i])
		}
	}
	if !reflect.DeepEqual(ops[1].BlockedBy, []string{"op-1", "op-3"}) {
		t.Fatalf("blockers lost: %#v", ops[1])
	}
	if ops[0].UserID != "admin" || ops[0].TokenID != "token-1" || ops[0].Cluster != "prod" ||
		ops[0].Instance != "node" || ops[0].Action != "exec" || ops[0].Session != "release" ||
		ops[0].CommandSummary != "sleep 10" || ops[0].Kind != "rpc" ||
		ops[0].StartedAt != "2026-10-03T01:00:00Z" || ops[0].AgeSeconds != 10 {
		t.Fatalf("existing operation fields lost: %#v", ops[0])
	}
}

func TestFilterGatewayOperationsByTarget(t *testing.T) {
	ops := []gatewayActiveOperation{
		{ID: "edu-op", Cluster: "doops-edu", Instance: "edu-coder"},
		{ID: "zyj-op", Cluster: "doops-zyj", Instance: "control-plane-1"},
		{ID: "unscoped-op"},
	}

	filtered := filterGatewayOperations(ops, " doops-edu ", " edu-coder ")
	if len(filtered) != 1 || filtered[0].ID != "edu-op" {
		t.Fatalf("target filter returned %#v", filtered)
	}
	if operationBelongsToTarget(ops, "edu-op", "doops-edu", "edu-coder") == false {
		t.Fatal("target operation was not recognized")
	}
	if operationBelongsToTarget(ops, "zyj-op", "doops-edu", "edu-coder") {
		t.Fatal("cross-target operation was accepted")
	}
	if operationBelongsToTarget(ops, "unscoped-op", "doops-edu", "edu-coder") {
		t.Fatal("unscoped operation was accepted")
	}
}

func TestGatewayAdminOperationCancelRequiresNewAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"accepted", 202, `{"id":"op-1","cancel_requested":true,"state":"cancel_requested"}`, true},
		{"legacy_success", 200, `{"id":"op-1","canceled":true}`, false},
		{"legacy_body_with_202", 202, `{"id":"op-1","canceled":true}`, false},
		{"wrong_status", 200, `{"id":"op-1","cancel_requested":true,"state":"cancel_requested"}`, false},
		{"wrong_id", 202, `{"id":"op-2","cancel_requested":true,"state":"cancel_requested"}`, false},
		{"missing_id", 202, `{"cancel_requested":true,"state":"cancel_requested"}`, false},
		{"missing_state", 202, `{"id":"op-1","cancel_requested":true}`, false},
		{"false_request", 202, `{"id":"op-1","cancel_requested":false,"state":"cancel_requested"}`, false},
		{"wrong_state", 202, `{"id":"op-1","cancel_requested":true,"state":"stopped"}`, false},
		{"invalid_json", 202, `{`, false},
		{"empty", 202, ``, false},
		{"forbidden", 403, `admin required`, false},
		{"conflict", 409, `operation outcome unknown`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/admin/operations" ||
					r.URL.RawQuery != "cluster=prod&id=op-1&instance=node" || r.Header.Get("Authorization") != "Bearer admin-token" {
					t.Errorf("unexpected cancel request: %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer gateway.Close()
			err := GatewayAdminOperationCancel(gateway.URL, "admin-token", " op-1 ", "prod", "node")
			if (err == nil) != tc.ok {
				t.Fatalf("cancel acknowledgment: err=%v, want success=%t", err, tc.ok)
			}
		})
	}
}
