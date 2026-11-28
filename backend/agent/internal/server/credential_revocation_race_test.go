package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// revocationRaceKinds are the two revocations that clean materializations: one
// grant, or the whole resource with every grant.
var revocationRaceKinds = []string{"grant", "resource"}

func (f *revocationFixture) adminID(t *testing.T) string {
	t.Helper()
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return auth.UserID
}

// beginRevocation and cleanupList are the first two steps the revocation
// handlers take, in the same order, so a test can interleave a use between them.
func (f *revocationFixture) beginRevocation(t *testing.T, kind string) {
	t.Helper()
	var err error
	if kind == "grant" {
		_, err = f.store.BeginCredentialGrantRevocation(f.resource.ID, f.grant.ID, f.adminID(t))
	} else {
		_, err = f.store.BeginCredentialRevocation(f.resource.ID, f.adminID(t))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (f *revocationFixture) cleanupList(t *testing.T, kind string) []CredentialMaterializationRecord {
	t.Helper()
	grantID := ""
	if kind == "grant" {
		grantID = f.grant.ID
	}
	records, err := f.store.CredentialMaterializations(f.resource.ID, "", grantID)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func (f *revocationFixture) finalizeRevocation(t *testing.T, kind string) error {
	t.Helper()
	if kind == "grant" {
		_, err := f.store.FinalizeCredentialGrantRevocation(f.resource.ID, f.grant.ID, 0)
		return err
	}
	return f.store.FinalizeCredentialRevocation(f.resource.ID)
}

// revokedState is the state the revoked object of kind is in.
func (f *revocationFixture) revokedState(t *testing.T, kind string) CredentialState {
	t.Helper()
	if kind == "grant" {
		return f.grantState(t)
	}
	resource, err := f.store.CredentialByID(f.resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	return resource.State
}

// assertRevocationInvariant fails when a resource or grant reads revoked while
// a materialization record may still own something on the target.
func (f *revocationFixture) assertRevocationInvariant(t *testing.T) {
	t.Helper()
	resource, err := f.store.CredentialByID(f.resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := uncleanedCredentialMaterializations(f.store.db, f.resource.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if (resource.State == CredentialStateRevoked || f.grantState(t) == CredentialStateRevoked) && pending != 0 {
		t.Fatalf("revoked with %d uncleaned materialization records: resource=%s grant=%s",
			pending, resource.State, f.grantState(t))
	}
}

// insertRacingRecord writes a verified context directly, the way a use that
// raced the revocation would have left it. The store refuses such an intent
// once the revocation began, so the row bypasses RecordCredentialMaterializationIntent
// to exercise the check that guards the terminal state.
func (f *revocationFixture) insertRacingRecord(t *testing.T, workload string) {
	t.Helper()
	request := f.request(workload)
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := f.store.db.Exec(`INSERT INTO credential_materializations
		(id, credential_id, version_id, grant_id, use, context_key, request_json,
		 evidence_json, status, error_category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}', 'verified', '', ?, ?)`,
		"credmat_"+randomHex(12), f.resource.ID, f.version.ID, f.grant.ID, request.Scope.Use,
		credentialMaterializationContextKey(request), string(requestJSON), now, now); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialUseThatRacesARevocationCannotRecordAfterTheCleanupList(t *testing.T) {
	for _, kind := range revocationRaceKinds {
		t.Run(kind, func(t *testing.T) {
			// Given a use that has already read its active grant, as ask --apply
			// does before it materializes each credential
			f := newRevocationFixture(t)
			// When the revocation begins and reads its cleanup list first
			f.beginRevocation(t, kind)
			if listed := f.cleanupList(t, kind); len(listed) != 0 {
				t.Fatalf("cleanup list = %#v, want empty", listed)
			}
			// And the use then reaches its materialization
			err := f.materialize(t, nil, nil)
			// Then the intent is refused before any record or dispatch
			if !errors.Is(err, ErrCredentialRevoked) {
				t.Fatalf("racing materialization = %v, want ErrCredentialRevoked", err)
			}
			if rows := f.statuses(t); len(rows) != 0 {
				t.Fatalf("refused intent left rows: %v", rows)
			}
			// And the revocation reaches its terminal state with nothing left
			if err := f.finalizeRevocation(t, kind); err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if state := f.revokedState(t, kind); state != CredentialStateRevoked {
				t.Fatalf("%s state = %s, want revoked", kind, state)
			}
			f.assertRevocationInvariant(t)
		})
	}
}

func TestCredentialRevocationFinalizeRefusesAContextARacingUseFinished(t *testing.T) {
	for _, kind := range revocationRaceKinds {
		t.Run(kind, func(t *testing.T) {
			// Given a use that recorded its intent before the revocation began
			// and is still running on the target
			f := newRevocationFixture(t)
			intent := f.record(t, "api", "pending", "")
			// When the revocation begins, lists the context and cleans it
			f.beginRevocation(t, kind)
			if listed := f.cleanupList(t, kind); len(listed) != 1 {
				t.Fatalf("cleanup list = %#v, want the running context", listed)
			}
			if err := f.store.MarkCredentialMaterializationContextCleaned(f.request("api")); err != nil {
				t.Fatal(err)
			}
			// And the use then finishes on the target and reports success
			if err := f.store.CompleteCredentialMaterialization(intent.ID, CredentialMaterialization{
				CredentialID: f.resource.ID, GrantID: f.grant.ID, VersionID: f.version.ID, Namespace: "apps",
				ResourceName: credentialSecretName(f.resource.ID, f.grant.ID), SecretType: "Opaque",
				Keys: []string{"dsn"}, ResourceVersion: "2", Status: "verified",
			}); err != nil {
				t.Fatal(err)
			}
			// Then the revocation refuses its terminal state and stays revoking
			if err := f.finalizeRevocation(t, kind); !errors.Is(err, ErrCredentialCleanupPending) {
				t.Fatalf("finalize with a finished racing use = %v, want ErrCredentialCleanupPending", err)
			}
			if state := f.revokedState(t, kind); state != CredentialStateRevoking {
				t.Fatalf("%s state = %s, want revoking", kind, state)
			}
			f.assertRevocationInvariant(t)
			// When the next attempt cleans the context, Then it reaches revoked
			if err := f.store.MarkCredentialMaterializationContextCleaned(f.request("api")); err != nil {
				t.Fatal(err)
			}
			if err := f.finalizeRevocation(t, kind); err != nil {
				t.Fatalf("finalize after cleanup: %v", err)
			}
			if state := f.revokedState(t, kind); state != CredentialStateRevoked {
				t.Fatalf("%s state after cleanup = %s, want revoked", kind, state)
			}
			f.assertRevocationInvariant(t)
		})
	}
}

func TestCredentialRevocationReportsAContextLeftAfterTheCleanup(t *testing.T) {
	for _, tc := range []struct {
		kind, body, category string
		revoke               func(*testing.T, *revocationFixture) <-chan revocationResponse
	}{
		{"grant", "credential_grant_revocation_outcome_unknown", "credential_grant_revocation_outcome_unknown",
			func(t *testing.T, f *revocationFixture) <-chan revocationResponse { return f.revoke(t) }},
		{"resource", "credential_revocation_outcome_unknown: dev/local", "credential_revocation_outcome_unknown",
			func(t *testing.T, f *revocationFixture) <-chan revocationResponse { return f.revokeResource(t) }},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			// Given a verified context on an online target
			f := newRevocationFixture(t)
			f.record(t, "api", "verified", "")
			agent := f.agent(t, "revocation-race-"+tc.kind)
			// When the revocation cleans it, and while that cleanup is in flight a
			// racing use leaves another context on the same target
			pending := tc.revoke(t, f)
			call := agent.call(t, "doops_credential_cleanup", revocationSession)
			f.insertRacingRecord(t, "worker")
			tokenLifecycleReplyText(t, agent, call, `{"status":"removed"}`, false)
			// Then the revocation fails closed with the outcome-unknown category
			response := f.wait(t, pending)
			if response.code != http.StatusBadGateway || response.body != tc.body {
				t.Fatalf("revoke = HTTP %d %q, want 502 %q", response.code, response.body, tc.body)
			}
			if state := f.revokedState(t, tc.kind); state != CredentialStateRevoking {
				t.Fatalf("%s state = %s, want revoking", tc.kind, state)
			}
			f.assertRevocationInvariant(t)
			var audit AuditEvent
			if tc.kind == "grant" {
				audit = f.lastRevokeAudit(t)
			} else {
				audit = f.resourceRevokeAudits(t)[0]
			}
			if audit.Status != "error" || audit.Error != tc.category {
				t.Fatalf("revoke audit = %s %q, want error %q", audit.Status, audit.Error, tc.category)
			}
			// When the revocation is retried, Then it cleans the context the use left and finishes
			pending = tc.revoke(t, f)
			tokenLifecycleReplyText(t, agent, agent.call(t, "doops_credential_cleanup", revocationSession), `{"status":"removed"}`, false)
			if response := f.wait(t, pending); response.code != http.StatusOK {
				t.Fatalf("retry = HTTP %d %q", response.code, response.body)
			}
			if state := f.revokedState(t, tc.kind); state != CredentialStateRevoked {
				t.Fatalf("%s state after retry = %s, want revoked", tc.kind, state)
			}
			if rows := f.statuses(t); rows["api"] != "cleaned/" || rows["worker"] != "cleaned/" {
				t.Fatalf("rows after retry = %v", rows)
			}
			f.assertRevocationInvariant(t)
		})
	}
}

func TestCredentialDeliveryRechecksTheGrantAfterTheLock(t *testing.T) {
	for _, tc := range []struct {
		name      string
		credType  CredentialType
		use       CredentialUse
		namespace string
		payload   string
		deliver   func(context.Context, *GatewayHub) string
	}{
		{
			name: "registry authfile", credType: CredentialTypeRegistry, use: CredentialUseImagePull, namespace: "apps",
			payload: `{"server":"registry.example.com:30002","username":"deploy","password":"` + revocationCanary + `"}`,
			deliver: func(ctx context.Context, h *GatewayHub) string {
				return h.deliverRegistryAuthFilesTo(ctx, "dev", "local")
			},
		},
		{
			name: "model provider", credType: CredentialTypeModelProvider, use: CredentialUseModelProvider, namespace: "default",
			payload: modelLifecyclePayload(),
			deliver: func(ctx context.Context, h *GatewayHub) string { return h.deliverModelProvider(ctx, "dev", "local") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a promoted credential bound to dev/local
			f := newRevocationFixture(t)
			admin := f.adminID(t)
			resource, err := f.store.CreateCredential(CredentialCreateRequest{
				Name: "delivery-race", Scope: CredentialScopePlatform, Type: tc.credType, CreatedBy: admin,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
				CredentialID: resource.ID, CreatedBy: admin, Activate: true, Payload: json.RawMessage(tc.payload),
			}); err != nil {
				t.Fatal(err)
			}
			grant, err := f.store.CreateCredentialGrant(CredentialGrantCreateRequest{
				CredentialID: resource.ID, CreatedBy: admin,
				Scope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: tc.namespace, Use: tc.use},
			})
			if err != nil {
				t.Fatal(err)
			}
			// When a delivery reads the binding and waits for the credential lock
			unlock, err := f.hub.acquireCredentialMutationLocks(context.Background(), []string{resource.ID})
			if err != nil {
				t.Fatal(err)
			}
			deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx := &credentialLockWaitContext{Context: deadline, waiting: make(chan struct{})}
			result := make(chan string, 1)
			go func() { result <- tc.deliver(ctx, f.hub) }()
			select {
			case <-ctx.waiting:
			case <-deadline.Done():
				unlock()
				t.Fatal("delivery did not wait for the credential lock")
			}
			// And the grant is revoked before the delivery gets the lock
			if _, err := f.store.BeginCredentialGrantRevocation(resource.ID, grant.ID, admin); err != nil {
				unlock()
				t.Fatal(err)
			}
			unlock()
			// Then the delivery finds nothing to deliver and records nothing
			select {
			case category := <-result:
				if category != "" {
					t.Fatalf("delivery of a revoked grant = %q, want nothing to deliver", category)
				}
			case <-deadline.Done():
				t.Fatal("delivery did not finish")
			}
			var rows int
			if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ?`,
				resource.ID).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("delivery of a revoked grant recorded %d materializations: %v", rows, err)
			}
		})
	}
}
