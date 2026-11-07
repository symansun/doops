package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// stagedVersion puts a staged version of the fixture credential, the kind of
// version a verification runs on.
func (f *revocationFixture) stagedVersion(t *testing.T) CredentialVersion {
	t.Helper()
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	version, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: f.resource.ID, CreatedBy: auth.UserID,
		Payload: json.RawMessage(`{"data":{"dsn":"` + revocationCanary + `-staged"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func (f *revocationFixture) verify(t *testing.T, versionID string) <-chan revocationResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"version_id": versionID, "session_id": revocationSession,
		"scope": map[string]string{"cluster": "dev", "instance": "local"},
	})
	return f.send(t, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/verify", string(body))
}

// auditQuery reads /v1/audit as the platform admin, the way the Web audit page does.
func (f *revocationFixture) auditQuery(t *testing.T, query url.Values) []auditRecord {
	t.Helper()
	response := f.wait(t, f.send(t, http.MethodGet, "/v1/audit?"+query.Encode(), ""))
	if response.code != http.StatusOK {
		t.Fatalf("audit query %s = HTTP %d %q", query.Encode(), response.code, response.body)
	}
	var result auditResponse
	if err := json.Unmarshal([]byte(response.body), &result); err != nil {
		t.Fatal(err)
	}
	return result.Events
}

func auditRecordIDs(records []auditRecord) map[int64]bool {
	ids := make(map[int64]bool, len(records))
	for _, record := range records {
		ids[record.ID] = true
	}
	return ids
}

func TestCredentialVerificationFailureAuditsTheTarget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		online   bool
		category string
	}{
		{name: "target offline before dispatch", category: "target_offline"},
		{name: "Agent refused before writing", online: true, category: "credential_secret_get_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a staged version bound to dev/local
			f := newRevocationFixture(t)
			staged := f.stagedVersion(t)
			var agent *executionLifecycleAgent
			if tc.online {
				agent = f.agent(t, "audit-target-verify")
			}
			// When its verification on dev/local fails
			pending := f.verify(t, staged.ID)
			if agent != nil {
				tokenLifecycleReplyText(t, agent, agent.call(t, "doops_credential_materialize", revocationSession), tc.category, true)
			}
			response := f.wait(t, pending)
			if response.code != http.StatusBadGateway || response.body != tc.category {
				t.Fatalf("verify = HTTP %d %q, want 502 %q", response.code, response.body, tc.category)
			}
			// Then the failed verification is audited against that target, like a success
			events, err := f.store.ListAuditFiltered(AuditFilter{Action: ActionCredentialVerify, Limit: 5})
			if err != nil || len(events) != 1 {
				t.Fatalf("verify audit: %v, %#v", err, events)
			}
			event := events[0]
			if event.Status != "error" || event.Error != tc.category || event.EndedAt.IsZero() ||
				event.Cluster != "dev" || event.Instance != "local" || event.GrantID != f.grant.ID ||
				event.VersionID != staged.ID || event.CredentialID != f.resource.ID ||
				event.Scope == nil || event.Scope.Namespace != "apps" {
				t.Fatalf("verify failure audit = %#v", event)
			}
			byTarget := f.auditQuery(t, url.Values{"cluster": {"dev"}, "instance": {"local"}, "action": {string(ActionCredentialVerify)}})
			if !auditRecordIDs(byTarget)[event.ID] {
				t.Fatalf("target query missed the failed verification: %#v", byTarget)
			}
		})
	}
}

func TestCredentialGrantFailuresAuditTheTarget(t *testing.T) {
	t.Run("revocation left revoking", func(t *testing.T) {
		// Given a grant whose verified context sits on an offline target
		f := newRevocationFixture(t)
		f.record(t, "api", "verified", "")
		// When its revocation cannot clean the context
		if response := f.revokeNow(t); response.code != http.StatusServiceUnavailable || response.body != "target_offline" {
			t.Fatalf("revoke = HTTP %d %q", response.code, response.body)
		}
		// Then the failure is audited against the grant's target and scope
		event := f.lastRevokeAudit(t)
		if event.Status != "error" || event.Error != "target_offline" || event.Cluster != "dev" ||
			event.Instance != "local" || event.GrantID != f.grant.ID || event.Scope == nil ||
			event.Scope.Namespace != "apps" || event.CredentialID != f.resource.ID {
			t.Fatalf("grant revoke failure audit = %#v", event)
		}
		byTarget := f.auditQuery(t, url.Values{"cluster": {"dev"}, "instance": {"local"}, "action": {string(ActionCredentialGrant)}})
		if !auditRecordIDs(byTarget)[event.ID] {
			t.Fatalf("target query missed the failed revocation: %#v", byTarget)
		}
	})
	t.Run("grant rejected", func(t *testing.T) {
		// Given a grant request for dev/local with an invalid namespace
		f := newRevocationFixture(t)
		body := `{"scope":{"cluster":"dev","instance":"local","namespace":"Not_A_Namespace"}}`
		// When the Gateway rejects it
		response := f.wait(t, f.send(t, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/grants", body))
		if response.code != http.StatusBadRequest || response.body != "credential_scope_invalid" {
			t.Fatalf("grant = HTTP %d %q", response.code, response.body)
		}
		// Then the rejection is audited against the requested target, with no grant
		event := f.lastRevokeAudit(t)
		if event.Status != "error" || event.Error != "credential_scope_invalid" ||
			event.Cluster != "dev" || event.Instance != "local" || event.GrantID != "" || event.Scope != nil {
			t.Fatalf("grant failure audit = %#v", event)
		}
	})
	t.Run("grant rejected with request text in the target", func(t *testing.T) {
		// Given a grant request whose cluster field is request text, not an identifier
		f := newRevocationFixture(t)
		body := `{"scope":{"cluster":"dev probe=` + revocationCanary + `","instance":"local"}}`
		// When the Gateway rejects it
		response := f.wait(t, f.send(t, http.MethodPost, "/v1/credentials/"+f.resource.ID+"/grants", body))
		if response.code != http.StatusBadRequest || response.body != "credential_scope_invalid" {
			t.Fatalf("grant = HTTP %d %q", response.code, response.body)
		}
		// Then the request text reaches no audit column: the target stays empty
		// and the summary keeps its generic form
		event := f.lastRevokeAudit(t)
		for name, value := range map[string]string{
			"cluster": event.Cluster, "instance": event.Instance, "command summary": event.CommandSummary,
		} {
			if strings.Contains(value, revocationCanary) {
				t.Fatalf("grant failure audit %s carries request text: %q", name, value)
			}
		}
		if event.Cluster != "" || event.Instance != "" || event.CommandSummary != "credential grant" {
			t.Fatalf("grant failure audit = %#v, want no target and the generic summary", event)
		}
	})
}

func TestCredentialResourceAuditsAreFoundByCredential(t *testing.T) {
	// Given a new resource that is created, written, promoted and revoked
	f := newRevocationFixture(t)
	created := f.wait(t, f.send(t, http.MethodPost, "/v1/credentials", `{"name":"audit-resource","scope":"platform","type":"opaque"}`))
	var resource CredentialResource
	if created.code != http.StatusOK || json.Unmarshal([]byte(created.body), &resource) != nil || resource.ID == "" {
		t.Fatalf("create = HTTP %d %q", created.code, created.body)
	}
	base := "/v1/credentials/" + resource.ID
	put := f.wait(t, f.send(t, http.MethodPut, base+"/payload", `{"data":{"dsn":"`+revocationCanary+`-resource"}}`))
	var version CredentialVersion
	if put.code != http.StatusOK || json.Unmarshal([]byte(put.body), &version) != nil {
		t.Fatalf("put = HTTP %d %q", put.code, put.body)
	}
	// A promotion without a verified grant is refused, and is still a resource-level audit.
	f.wait(t, f.send(t, http.MethodPost, base+"/promote", `{"version_id":"`+version.ID+`"}`))
	if revoked := f.wait(t, f.send(t, http.MethodPost, base+"/revoke", "")); revoked.code != http.StatusOK {
		t.Fatalf("revoke = HTTP %d %q", revoked.code, revoked.body)
	}
	// When the audit is queried by the credential
	records := f.auditQuery(t, url.Values{"session": {resource.ID}})
	// Then all four operations are there, each at resource level without a target
	var actions []string
	for _, record := range records {
		if record.CredentialID != resource.ID || record.Cluster != "" || record.Instance != "" {
			t.Fatalf("resource-level audit carries a target or lost its credential: %#v", record)
		}
		actions = append(actions, record.Action)
	}
	sort.Strings(actions)
	want := []string{"credential:create", "credential:revoke", "credential:rotate", "credential:rotate"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("resource audits = %v, want %v", actions, want)
	}
}
