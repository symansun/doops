package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// renamedInstance stands for a target that was renamed and never comes back,
// like the online canary's second grant.
const renamedInstance = "renamed"

// renamedGrant binds the fixture's credential to the renamed target, so the
// resource has one grant on the live dev/local target and one on a target
// that stays offline.
func (f *revocationFixture) renamedGrant(t *testing.T) CredentialGrant {
	t.Helper()
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	scope := revocationScope("api")
	scope.Instance = renamedInstance
	grant, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: f.resource.ID, Scope: scope, CreatedBy: auth.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *revocationFixture) requestFor(grant CredentialGrant) CredentialMaterializeRequest {
	request := f.request("api")
	request.GrantID = grant.ID
	request.Instance = grant.Scope.Instance
	request.Scope = grant.Scope
	return request
}

func (f *revocationFixture) revokeResource(t *testing.T) <-chan revocationResponse {
	t.Helper()
	return f.send(t, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/revoke", "")
}

// lifecycleStates renders the resource state and every grant state by target.
func (f *revocationFixture) lifecycleStates(t *testing.T) string {
	t.Helper()
	resource, err := f.store.CredentialByID(f.resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := f.store.CredentialGrants(f.resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := []string{"resource=" + string(resource.State)}
	for _, grant := range grants {
		parts = append(parts, grant.Scope.Instance+"="+string(grant.State))
	}
	sort.Strings(parts[1:])
	return strings.Join(parts, " ")
}

// rowsByTarget renders every materialization row of the resource by target.
func (f *revocationFixture) rowsByTarget(t *testing.T) string {
	t.Helper()
	rows, err := f.store.db.Query(`SELECT request_json, status, error_category FROM credential_materializations WHERE credential_id = ?`, f.resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var requestJSON, status, category string
		if err := rows.Scan(&requestJSON, &status, &category); err != nil {
			t.Fatal(err)
		}
		var request CredentialMaterializeRequest
		if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, request.Instance+"="+status+"/"+category)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// resourceRevokeAudits lists the resource revocation audits newest first. The
// internal cleanup calls share the credential:revoke action, so the filter
// also pins the resource-level session.
func (f *revocationFixture) resourceRevokeAudits(t *testing.T) []AuditEvent {
	t.Helper()
	events, err := f.store.ListAuditFiltered(AuditFilter{Action: ActionCredentialRevoke, Session: f.resource.ID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestCredentialResourceRevocationOutcomes(t *testing.T) {
	type result struct {
		code            int
		body            string
		states, rows    string
		audit, category string
	}
	revoking := "resource=revoking local=revoking renamed=revoking"
	for _, tc := range []struct {
		name string
		// given prepares both grants' rows and returns the online dev/local Agent, if any.
		given func(*testing.T, *revocationFixture, CredentialGrant) *executionLifecycleAgent
		// cleanups answers each cleanup call that reaches dev/local, in order.
		cleanups []string
		want     result
	}{
		{
			name: "renamed target holds only an attempt that left it unchanged",
			given: func(t *testing.T, f *revocationFixture, renamed CredentialGrant) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				f.recordRequest(t, f.requestFor(renamed), "failed", "credential_secret_get_failed")
				return f.agent(t, "resource-revoke-unchanged")
			},
			cleanups: []string{""},
			want: result{http.StatusOK, "", "resource=revoked local=revoked renamed=revoked",
				"local=cleaned/ renamed=not_applied/credential_secret_get_failed", "success", ""},
		},
		{
			name: "no target holds anything and both are offline",
			given: func(t *testing.T, f *revocationFixture, renamed CredentialGrant) *executionLifecycleAgent {
				return nil
			},
			want: result{http.StatusOK, "", "resource=revoked local=revoked renamed=revoked", "", "success", ""},
		},
		{
			name: "renamed target still holds a verified context",
			given: func(t *testing.T, f *revocationFixture, renamed CredentialGrant) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				f.recordRequest(t, f.requestFor(renamed), "verified", "")
				return f.agent(t, "resource-revoke-offline")
			},
			cleanups: []string{""},
			want: result{http.StatusServiceUnavailable, "target_offline: dev/renamed", revoking,
				"local=cleaned/ renamed=verified/", "error", "target_offline"},
		},
		{
			name: "renamed target holds a row written before not_applied existed",
			given: func(t *testing.T, f *revocationFixture, renamed CredentialGrant) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				f.recordRequest(t, f.requestFor(renamed), "outcome_unknown", "credential_secret_get_failed")
				return f.agent(t, "resource-revoke-legacy")
			},
			cleanups: []string{""},
			want: result{http.StatusServiceUnavailable, "target_offline: dev/renamed", revoking,
				"local=cleaned/ renamed=outcome_unknown/credential_secret_get_failed", "error", "target_offline"},
		},
		{
			name: "online cleanup reached the Agent and failed while the renamed target is offline",
			given: func(t *testing.T, f *revocationFixture, renamed CredentialGrant) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				f.recordRequest(t, f.requestFor(renamed), "verified", "")
				return f.agent(t, "resource-revoke-failed")
			},
			cleanups: []string{"credential_secret_ownership_mismatch"},
			want: result{http.StatusBadGateway, "credential_revocation_outcome_unknown: dev/local, dev/renamed", revoking,
				"local=verified/ renamed=verified/", "error", "credential_revocation_outcome_unknown"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a resource bound to a live target and to a renamed one
			f := newRevocationFixture(t)
			renamed := f.renamedGrant(t)
			agent := tc.given(t, f, renamed)
			// When the resource is revoked
			pending := f.revokeResource(t)
			for _, failure := range tc.cleanups {
				call := agent.call(t, "doops_credential_cleanup", revocationSession)
				if failure == "" {
					tokenLifecycleReplyText(t, agent, call, `{"status":"removed"}`, false)
				} else {
					tokenLifecycleReplyText(t, agent, call, failure, true)
				}
			}
			response := f.wait(t, pending)
			// Then every reachable context is cleaned, the response names the targets
			// left, and the resource and its grants move together
			if response.code != tc.want.code || (tc.want.body != "" && response.body != tc.want.body) {
				t.Fatalf("revoke = HTTP %d %q, want %d %q", response.code, response.body, tc.want.code, tc.want.body)
			}
			if got := f.lifecycleStates(t); got != tc.want.states {
				t.Fatalf("states = %s, want %s", got, tc.want.states)
			}
			if got := f.rowsByTarget(t); got != tc.want.rows {
				t.Fatalf("rows = %s, want %s", got, tc.want.rows)
			}
			audits := f.resourceRevokeAudits(t)
			if len(audits) != 1 || audits[0].Status != tc.want.audit || audits[0].Error != tc.want.category ||
				audits[0].EndedAt.IsZero() || audits[0].CredentialID != f.resource.ID {
				t.Fatalf("revoke audit = %#v, want one %s %q", audits, tc.want.audit, tc.want.category)
			}
		})
	}
}

func TestCredentialResourceRevocationRetryAfterRevoking(t *testing.T) {
	// Given a resource left revoking by an earlier revocation that did not move
	// its grants, with a verified context on dev/local and only an unchanged
	// attempt on the renamed target
	f := newRevocationFixture(t)
	renamed := f.renamedGrant(t)
	f.record(t, "api", "verified", "")
	f.recordRequest(t, f.requestFor(renamed), "failed", "credential_secret_get_failed")
	if _, err := f.store.db.Exec(`UPDATE credentials SET state = ? WHERE id = ?`, CredentialStateRevoking, f.resource.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.lifecycleStates(t); got != "resource=revoking local=active renamed=active" {
		t.Fatalf("legacy states = %s", got)
	}
	// When revocation is retried while dev/local is offline
	for attempt := 0; attempt < 2; attempt++ {
		response := f.wait(t, f.revokeResource(t))
		// Then it fails closed naming dev/local, and no grant reads as active
		if response.code != http.StatusServiceUnavailable || response.body != "target_offline: dev/local" {
			t.Fatalf("offline retry %d = HTTP %d %q", attempt, response.code, response.body)
		}
		if got := f.lifecycleStates(t); got != "resource=revoking local=revoking renamed=revoking" {
			t.Fatalf("offline retry %d states = %s", attempt, got)
		}
	}
	listed := f.wait(t, f.send(t, http.MethodGet, "/v1/credentials/"+f.resource.ID+"/grants", ""))
	if listed.code != http.StatusOK || strings.Contains(listed.body, `"state":"active"`) {
		t.Fatalf("grant list during revoking = HTTP %d %s", listed.code, listed.body)
	}
	// When dev/local comes back and revocation is retried
	agent := f.agent(t, "resource-revoke-retry")
	pending := f.revokeResource(t)
	tokenLifecycleReplyText(t, agent, agent.call(t, "doops_credential_cleanup", revocationSession), `{"status":"removed"}`, false)
	// Then the resource and both grants reach revoked
	if response := f.wait(t, pending); response.code != http.StatusOK {
		t.Fatalf("online retry = HTTP %d %q", response.code, response.body)
	}
	if got := f.lifecycleStates(t); got != "resource=revoked local=revoked renamed=revoked" {
		t.Fatalf("states after online retry = %s", got)
	}
	// When it is revoked once more, Then nothing reaches the Agent and it still succeeds
	if response := f.wait(t, f.revokeResource(t)); response.code != http.StatusOK {
		t.Fatalf("retry after revoked = HTTP %d %q", response.code, response.body)
	}
	select {
	case call := <-agent.calls:
		t.Fatalf("revoked resource dispatched %s", call.name)
	case <-time.After(100 * time.Millisecond):
	}
	audits := f.resourceRevokeAudits(t)
	if len(audits) != 4 {
		t.Fatalf("each revoke attempt needs one audit, got %d", len(audits))
	}
	for _, audit := range audits {
		if audit.EndedAt.IsZero() || audit.Cluster != "" || audit.Instance != "" {
			t.Fatalf("resource revoke audit must be finished and resource level: %#v", audit)
		}
	}
	if got := fmt.Sprint(audits[0].Status, audits[3].Error); got != "successtarget_offline" {
		t.Fatalf("first and last audit = %s", got)
	}
}
