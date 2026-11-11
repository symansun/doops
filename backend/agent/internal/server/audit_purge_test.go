package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestAuditPurgeRemoteAuthorizationRemainsDistinctFromLocal(t *testing.T) {
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	adminAuth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := f.user(t, "purge-target-operator", ScopeGrant{Cluster: "*", Instance: "*"})
	ordinaryAuth, err := f.store.VerifyUserToken(ordinary.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC()
	id, err := f.store.StartAudit(AuditEvent{Action: ActionExec, StartedAt: cutoff.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.FinishAudit(id, AuditFinish{Status: "success"}); err != nil {
		t.Fatal(err)
	}
	originals, err := f.store.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetUserAdmin(adminAuth.UserID, false); err != nil {
		t.Fatal(err)
	}
	localUserID := fmt.Sprintf("local:uid:%d", os.Geteuid())
	for _, tc := range []struct {
		name string
		auth TokenAuth
	}{
		{"anonymous", TokenAuth{}},
		{"local_identity", TokenAuth{UserID: localUserID}},
		{"target_operator", ordinaryAuth},
		{"revoked_admin", adminAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if deleted, err := f.store.PurgeAuditBefore(tc.auth, cutoff); !errors.Is(err, ErrUnauthorized) || deleted != 0 {
				t.Fatalf("unauthorized remote purge: deleted=%d err=%v", deleted, err)
			}
		})
	}
	path := "/v1/audit?before=" + url.QueryEscape(cutoff.Format(time.RFC3339Nano)) + "&local=true&user_id=" + url.QueryEscape(localUserID)
	f.http(t, CreatedToken{}, http.MethodDelete, path, "", http.StatusUnauthorized)
	f.http(t, ordinary, http.MethodDelete, path, "", http.StatusForbidden)
	f.http(t, f.admin, http.MethodDelete, path, "", http.StatusForbidden)
	events, err := f.store.ListAudit(100)
	if err != nil || !reflect.DeepEqual(events, originals) {
		t.Fatalf("denied purge changed audit history: events=%#v err=%v", events, err)
	}
	if err := f.store.SetUserAdmin(adminAuth.UserID, true); err != nil {
		t.Fatal(err)
	}
	body := f.http(t, f.admin, http.MethodDelete, path, "", http.StatusOK)
	var result auditPurgeResponse
	if err := json.Unmarshal(body, &result); err != nil || result.Deleted != 1 {
		t.Fatalf("admin purge result=%s err=%v", body, err)
	}
	events, err = f.store.ListAudit(100)
	if err != nil || len(events) != 1 {
		t.Fatalf("admin purge did not leave exactly one receipt: events=%#v err=%v", events, err)
	}
	receipt := events[0]
	if receipt.Action != "audit:purge" || receipt.Status != "success" || receipt.UserID != adminAuth.UserID || receipt.TokenID != adminAuth.TokenID {
		t.Fatalf("remote purge lost its authenticated identity: %#v", receipt)
	}
	var evidence struct {
		Before  string `json:"before"`
		Deleted int64  `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(receipt.Tail), &evidence); err != nil || evidence.Before != cutoff.Format(time.RFC3339Nano) || evidence.Deleted != 1 {
		t.Fatalf("remote purge receipt evidence=%q err=%v", receipt.Tail, err)
	}
}
