package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const revocationCanary = "SYNTHETIC_REVOCATION_PAYLOAD_CANARY_DO_NOT_LOG"

const revocationSession = "revocation-session"

type revocationFixture struct {
	*executionLifecycleFixture
	resource CredentialResource
	version  CredentialVersion
	grant    CredentialGrant
	bodies   []string
}

type revocationResponse struct {
	code int
	body string
}

func newRevocationFixture(t *testing.T) *revocationFixture {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("k", 32))
	f := &revocationFixture{executionLifecycleFixture: newExecutionLifecycleFixture(t, GatewayHubOptions{})}
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	f.resource, err = f.store.CreateCredential(CredentialCreateRequest{
		Name: "revocation-canary", Scope: CredentialScopePlatform, Type: CredentialTypeOpaque, CreatedBy: auth.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.version, err = f.store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: f.resource.ID, CreatedBy: auth.UserID, Activate: true,
		Payload: json.RawMessage(`{"data":{"dsn":"` + revocationCanary + `"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.grant, err = f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: f.resource.ID, Scope: revocationScope("api"), CreatedBy: auth.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.assertNoPayload(t) })
	return f
}

func revocationScope(workload string) CredentialGrantScope {
	return CredentialGrantScope{
		Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: workload}, RequiredKeys: []string{"dsn"},
	}
}

func (f *revocationFixture) request(workload string) CredentialMaterializeRequest {
	return CredentialMaterializeRequest{
		SessionID: revocationSession, Cluster: "dev", Instance: "local",
		CredentialID: f.resource.ID, GrantID: f.grant.ID, VersionID: f.version.ID,
		CredentialType: CredentialTypeOpaque, Scope: revocationScope(workload), RequiredKeys: []string{"dsn"},
	}
}

// record writes one materialization row the way a finished attempt leaves it.
// "outcome_unknown" writes it the way the Gateway did before not_applied existed.
func (f *revocationFixture) record(t *testing.T, workload, status, category string) CredentialMaterializationRecord {
	t.Helper()
	intent, err := f.store.RecordCredentialMaterializationIntent(f.request(workload))
	if err != nil {
		t.Fatal(err)
	}
	switch status {
	case "verified":
		err = f.store.CompleteCredentialMaterialization(intent.ID, CredentialMaterialization{
			CredentialID: f.resource.ID, GrantID: f.grant.ID, VersionID: f.version.ID, Namespace: "apps",
			ResourceName: credentialSecretName(f.resource.ID, f.grant.ID), SecretType: "Opaque",
			Keys: []string{"dsn"}, ResourceVersion: "1", Status: "verified",
		})
	case "failed":
		err = f.store.MarkCredentialMaterializationFailed(intent, category)
	case "outcome_unknown":
		err = f.store.MarkCredentialMaterializationUnknown(intent.ID, category)
	case "pending":
	default:
		t.Fatalf("unknown record status %q", status)
	}
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func (f *revocationFixture) statuses(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.store.db.Query(`SELECT request_json, status, error_category FROM credential_materializations WHERE grant_id = ?`, f.grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var requestJSON, status, category string
		if err := rows.Scan(&requestJSON, &status, &category); err != nil {
			t.Fatal(err)
		}
		var request CredentialMaterializeRequest
		if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
			t.Fatal(err)
		}
		out[request.Scope.Workload.Name] = status + "/" + category
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *revocationFixture) grantState(t *testing.T) CredentialState {
	t.Helper()
	grant, err := f.store.credentialGrantByID(f.resource.ID, f.grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	return grant.State
}

// revoke sends the grant revocation in the background, so the test can answer
// the cleanup calls the Agent receives while the request is in flight.
func (f *revocationFixture) revoke(t *testing.T) <-chan revocationResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"grant_id": f.grant.ID})
	req, err := http.NewRequest(http.MethodDelete, f.server.URL+"/v1/credentials/"+f.resource.ID+"/grants", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.admin.Plaintext)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan revocationResponse, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			done <- revocationResponse{code: -1, body: err.Error()}
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		done <- revocationResponse{code: resp.StatusCode, body: strings.TrimSpace(string(data))}
	}()
	return done
}

func (f *revocationFixture) wait(t *testing.T, pending <-chan revocationResponse) revocationResponse {
	t.Helper()
	select {
	case response := <-pending:
		f.bodies = append(f.bodies, response.body)
		return response
	case <-time.After(10 * time.Second):
		t.Fatal("grant revocation did not finish")
		return revocationResponse{}
	}
}

func (f *revocationFixture) revokeNow(t *testing.T) revocationResponse {
	t.Helper()
	return f.wait(t, f.revoke(t))
}

func (f *revocationFixture) lastRevokeAudit(t *testing.T) AuditEvent {
	t.Helper()
	events, err := f.store.ListAuditFiltered(AuditFilter{Action: ActionCredentialGrant, Limit: 1})
	if err != nil || len(events) != 1 {
		t.Fatalf("grant revoke audit: %v, %#v", err, events)
	}
	return events[0]
}

func (f *revocationFixture) assertNoPayload(t *testing.T) {
	t.Helper()
	events, err := f.store.ListAudit(500)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(events)
	var materializations strings.Builder
	rows, err := f.store.db.Query(`SELECT request_json || evidence_json FROM credential_materializations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		materializations.WriteString(value)
	}
	for name, text := range map[string]string{
		"audit": string(data), "responses": strings.Join(f.bodies, "\n"), "materializations": materializations.String(),
	} {
		if strings.Contains(text, revocationCanary) {
			t.Fatalf("payload canary reached %s: %s", name, text)
		}
	}
}

func (f *revocationFixture) disconnect(t *testing.T, agent *executionLifecycleAgent) {
	t.Helper()
	_ = agent.conn.Close()
	requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
}

// materialize runs one materialization against the controlled Agent and lets
// reply decide how the Agent answers.
func (f *revocationFixture) materialize(t *testing.T, agent *executionLifecycleAgent, reply func(executionLifecycleCall)) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, _, err := f.hub.materializeCredentialVersion(
			context.Background(), "dev", "local", revocationSession, f.resource, f.version, "", f.grant.ID,
			CredentialPlanReference{Name: f.resource.Name, Scope: revocationScope("api")},
		)
		done <- err
	}()
	if agent != nil {
		reply(agent.call(t, "doops_credential_materialize", revocationSession))
	}
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("materialization did not finish")
		return nil
	}
}

func TestCredentialMaterializationFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, category, want string
	}{
		{"dispatch never happened", "target_offline", "not_applied"},
		{"request could not be encoded", "credential_request_encode_failed", "not_applied"},
		{"audit refused before dispatch", "execution_audit_unavailable", "not_applied"},
		{"payload check before any write", "credential_key_mismatch", "not_applied"},
		{"registry probe before any write", "registry_auth_failed", "not_applied"},
		{"snapshot read before apply", "credential_secret_get_failed", "not_applied"},
		{"snapshot ownership refusal", "credential_secret_ownership_conflict", "not_applied"},
		{"apply failed and restore was confirmed", "credential_target_mutation_failed", "not_applied"},
		{"workload patch failed and restore was confirmed", "credential_workload_verification_failed", "not_applied"},
		{"restore failed", "credential_target_outcome_unknown", "outcome_unknown"},
		{"restore itself reported failure", "credential_secret_restore_failed", "outcome_unknown"},
		{"authfile delivery after the Secret verified", "registry_authfile_unavailable", "outcome_unknown"},
		{"transport timeout", "verification_transport_failed", "outcome_unknown"},
		{"unclassified Gateway error", "credential_operation_failed", "outcome_unknown"},
		{"unclassified Agent error", "credential_tool_failed", "outcome_unknown"},
		{"free text never matches", "kubectl said " + revocationCanary, "outcome_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a first materialization attempt for a grant context
			f := newRevocationFixture(t)
			// When it fails with the category
			f.record(t, "api", "failed", tc.category)
			// Then only a failure that proves the target unchanged becomes not_applied
			got := strings.SplitN(f.statuses(t)["api"], "/", 2)[0]
			if got != tc.want {
				t.Fatalf("status = %s, want %s", got, tc.want)
			}
			listed, err := f.store.CredentialMaterializations(f.resource.ID, "", f.grant.ID)
			if err != nil {
				t.Fatal(err)
			}
			if wantListed := tc.want != "not_applied"; (len(listed) == 1) != wantListed {
				t.Fatalf("cleanup inventory = %#v, want listed=%v", listed, wantListed)
			}
		})
	}
}

func TestCredentialMaterializationUnchangedFailureKeepsEarlierAttempt(t *testing.T) {
	for _, prior := range []string{"verified", "pending"} {
		t.Run(prior, func(t *testing.T) {
			// Given an earlier attempt of the same row that may own the Secret
			f := newRevocationFixture(t)
			f.record(t, "api", prior, "")
			// When a later attempt of the same row fails without touching the target
			f.record(t, "api", "failed", "credential_secret_get_failed")
			// Then the row still says what the earlier attempt left, and cleanup still runs
			if got := f.statuses(t)["api"]; got != prior+"/" {
				t.Fatalf("row = %s, want the earlier %s attempt", got, prior)
			}
			listed, err := f.store.CredentialMaterializations(f.resource.ID, "", f.grant.ID)
			if err != nil || len(listed) != 1 {
				t.Fatalf("earlier attempt dropped from cleanup: %v, %#v", err, listed)
			}
		})
	}
}

func TestCredentialMaterializationLegacyUnknownStaysFailClosed(t *testing.T) {
	// Given a row written before not_applied existed, whose category alone
	// cannot show whether it replaced an earlier successful attempt
	f := newRevocationFixture(t)
	f.record(t, "api", "outcome_unknown", "credential_secret_get_failed")
	// When its offline target is revoked
	response := f.revokeNow(t)
	// Then the grant stays revoking and the caller learns the target is offline
	if response.code != http.StatusServiceUnavailable || response.body != "target_offline" {
		t.Fatalf("legacy unknown revoke = HTTP %d %q", response.code, response.body)
	}
	if state := f.grantState(t); state != CredentialStateRevoking {
		t.Fatalf("grant state = %s, want revoking", state)
	}
}

func TestCredentialGrantRevocationOutcomes(t *testing.T) {
	type result struct {
		code     int
		body     string
		state    CredentialState
		audit    string
		category string
	}
	revoked := result{http.StatusOK, "", CredentialStateRevoked, "success", ""}
	offline := result{http.StatusServiceUnavailable, "target_offline", CredentialStateRevoking, "error", "target_offline"}
	for _, tc := range []struct {
		name string
		// given prepares the materialization rows and returns the online Agent, if any.
		given func(*testing.T, *revocationFixture) *executionLifecycleAgent
		// cleanups answers each cleanup call the Agent receives, in order.
		cleanups []string
		want     result
		rows     map[string]string
	}{
		{
			name: "online target is cleaned then revoked",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				return f.agent(t, "revocation-online")
			},
			cleanups: []string{""},
			want:     revoked,
			rows:     map[string]string{"api": "cleaned/"},
		},
		{
			name: "offline target with no materialization",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				return nil
			},
			want: revoked,
			rows: map[string]string{},
		},
		{
			name: "offline target whose only attempt failed before dispatch",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				if err := f.materialize(t, nil, nil); err == nil {
					t.Fatal("materialization on an offline target succeeded")
				}
				return nil
			},
			want: revoked,
			rows: map[string]string{"api": "not_applied/target_offline"},
		},
		{
			name: "offline target whose Agent refused before writing",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				agent := f.agent(t, "revocation-refused")
				err := f.materialize(t, agent, func(call executionLifecycleCall) {
					tokenLifecycleReplyText(t, agent, call, "credential_secret_get_failed", true)
				})
				if err == nil || credentialOperationErrorCategory(err) != "credential_secret_get_failed" {
					t.Fatalf("Agent refusal = %v", err)
				}
				f.disconnect(t, agent)
				return nil
			},
			want: revoked,
			rows: map[string]string{"api": "not_applied/credential_secret_get_failed"},
		},
		{
			name: "offline target whose Agent could not restore",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				agent := f.agent(t, "revocation-restore-failed")
				if err := f.materialize(t, agent, func(call executionLifecycleCall) {
					tokenLifecycleReplyText(t, agent, call, "credential_target_outcome_unknown", true)
				}); err == nil {
					t.Fatal("restore failure succeeded")
				}
				f.disconnect(t, agent)
				return nil
			},
			want: offline,
			rows: map[string]string{"api": "outcome_unknown/credential_target_outcome_unknown"},
		},
		{
			name: "offline target that lost the connection mid-call",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				agent := f.agent(t, "revocation-transport")
				if err := f.materialize(t, agent, func(executionLifecycleCall) {
					_ = agent.conn.Close()
				}); err == nil || !errors.Is(err, errExecutionOutcomeUnknown) {
					t.Fatalf("lost connection = %v, want outcome unknown", err)
				}
				requireEventually(t, 3*time.Second, func() bool { return f.hub.getAgent("dev", "local") == nil })
				return nil
			},
			want: offline,
			rows: map[string]string{"api": "outcome_unknown/credential_operation_failed"},
		},
		{
			name: "offline target with a verified materialization",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				return nil
			},
			want: offline,
			rows: map[string]string{"api": "verified/"},
		},
		{
			name: "online cleanup that reached the Agent and failed",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				return f.agent(t, "revocation-cleanup-failed")
			},
			cleanups: []string{"credential_secret_ownership_mismatch"},
			want: result{http.StatusBadGateway, "credential_grant_revocation_outcome_unknown", CredentialStateRevoking,
				"error", "credential_grant_revocation_outcome_unknown"},
			rows: map[string]string{"api": "verified/"},
		},
		{
			name: "one failed context does not stop the others",
			given: func(t *testing.T, f *revocationFixture) *executionLifecycleAgent {
				f.record(t, "api", "verified", "")
				f.record(t, "worker", "verified", "")
				return f.agent(t, "revocation-two-contexts")
			},
			cleanups: []string{"credential_secret_ownership_mismatch", ""},
			want: result{http.StatusBadGateway, "credential_grant_revocation_outcome_unknown", CredentialStateRevoking,
				"error", "credential_grant_revocation_outcome_unknown"},
			rows: map[string]string{"api": "verified/", "worker": "cleaned/"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given the grant's materialization history and target connectivity
			f := newRevocationFixture(t)
			agent := tc.given(t, f)
			// When the grant is revoked
			pending := f.revoke(t)
			for _, failure := range tc.cleanups {
				call := agent.call(t, "doops_credential_cleanup", revocationSession)
				if failure == "" {
					tokenLifecycleReplyText(t, agent, call, `{"status":"removed"}`, false)
				} else {
					tokenLifecycleReplyText(t, agent, call, failure, true)
				}
			}
			response := f.wait(t, pending)
			// Then the response, grant state, rows and audit agree
			if response.code != tc.want.code || (tc.want.body != "" && response.body != tc.want.body) {
				t.Fatalf("revoke = HTTP %d %q, want %d %q", response.code, response.body, tc.want.code, tc.want.body)
			}
			if state := f.grantState(t); state != tc.want.state {
				t.Fatalf("grant state = %s, want %s", state, tc.want.state)
			}
			if got := f.statuses(t); fmt.Sprint(got) != fmt.Sprint(tc.rows) {
				t.Fatalf("rows = %v, want %v", got, tc.rows)
			}
			audit := f.lastRevokeAudit(t)
			if audit.Status != tc.want.audit || audit.Error != tc.want.category || audit.EndedAt.IsZero() {
				t.Fatalf("revoke audit = %s %q, want %s %q", audit.Status, audit.Error, tc.want.audit, tc.want.category)
			}
		})
	}
}

func TestCredentialGrantRevocationRetryIsIdempotent(t *testing.T) {
	// Given a revoking grant whose verified context sits on an offline target
	f := newRevocationFixture(t)
	f.record(t, "api", "verified", "")
	// When revocation is retried while the target stays offline
	for attempt := 0; attempt < 2; attempt++ {
		response := f.revokeNow(t)
		// Then every retry fails closed with the same category and state
		if response.code != http.StatusServiceUnavailable || response.body != "target_offline" ||
			f.grantState(t) != CredentialStateRevoking {
			t.Fatalf("offline retry %d = HTTP %d %q, grant %s", attempt, response.code, response.body, f.grantState(t))
		}
	}
	// When the target comes back and revocation is retried
	agent := f.agent(t, "revocation-retry")
	pending := f.revoke(t)
	tokenLifecycleReplyText(t, agent, agent.call(t, "doops_credential_cleanup", revocationSession), `{"status":"removed"}`, false)
	// Then the grant reaches revoked, and a further retry changes nothing
	if response := f.wait(t, pending); response.code != http.StatusOK || f.grantState(t) != CredentialStateRevoked {
		t.Fatalf("online retry = HTTP %d %q, grant %s", response.code, response.body, f.grantState(t))
	}
	if response := f.revokeNow(t); response.code != http.StatusOK || f.grantState(t) != CredentialStateRevoked {
		t.Fatalf("retry after revoked = HTTP %d %q", response.code, response.body)
	}
	events, err := f.store.ListAuditFiltered(AuditFilter{Action: ActionCredentialGrant, Limit: 10})
	if err != nil || len(events) != 4 {
		t.Fatalf("each revoke attempt needs one audit: %v, %d", err, len(events))
	}
}

func TestCredentialCleanupFailureNamesTheReason(t *testing.T) {
	const unknown = "credential_grant_revocation_outcome_unknown"
	for _, tc := range []struct {
		name   string
		err    error
		want   string
		status int
	}{
		{"target offline", errors.New("target offline: dev/local"), "target_offline", http.StatusServiceUnavailable},
		{"target busy", errors.New("target busy: active operation op-1"), "target_busy", http.StatusConflict},
		{"queue full", errors.New("target queue full"), "target_queue_full", http.StatusConflict},
		{"queue timeout", errors.New("target queue timeout"), "target_queue_timeout", http.StatusConflict},
		{"reached the Agent", errors.New("credential_secret_ownership_mismatch"), unknown, http.StatusBadGateway},
		{"lost mid-call", fmt.Errorf("%w: target offline after dispatch", errExecutionOutcomeUnknown), unknown, http.StatusBadGateway},
		{"offline and unknown", errors.Join(errors.New("target offline: dev/local"), fmt.Errorf("%w: lost", errExecutionOutcomeUnknown)), unknown, http.StatusBadGateway},
		{"only offline", errors.Join(errors.New("target offline: dev/local"), errors.New("target offline: dev/local")), "target_offline", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, status := credentialCleanupFailure(tc.err, unknown); got != tc.want || status != tc.status {
				t.Fatalf("credentialCleanupFailure = %s %d, want %s %d", got, status, tc.want, tc.status)
			}
		})
	}
	// A call that may have reached the Agent is never reported as offline.
	if got := credentialOperationErrorCategory(fmt.Errorf("%w: target offline mid-call", errExecutionOutcomeUnknown)); got == "target_offline" {
		t.Fatalf("outcome-unknown error classified as %s", got)
	}
}

// Every category on the unchanged list must still be one the code returns; a
// renamed category would otherwise silently fall back to outcome_unknown or,
// worse, a new category reuse an old name with a different meaning.
func TestCredentialMaterializationUnchangedCategoriesExistInSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "credential_materialization_store.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		source.Write(data)
	}
	for category := range credentialMaterializationUnchangedCategories {
		if !strings.Contains(source.String(), `"`+category+`"`) {
			t.Errorf("unchanged category %s is not returned anywhere", category)
		}
	}
	for _, category := range []string{"credential_target_outcome_unknown", "credential_secret_restore_failed",
		"credential_workload_restore_failed", "registry_authfile_unavailable", "verification_transport_failed",
		"credential_operation_failed", "credential_tool_failed"} {
		if credentialMaterializationUnchangedCategories[category] {
			t.Errorf("%s may leave a change and must not be on the unchanged list", category)
		}
	}
}
