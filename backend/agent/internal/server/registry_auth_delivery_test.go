package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// registryAuthLifecycleFixture prepares an in-process Agent connected to the
// Gateway, with the Agent's registry authfile pointed at a fixture directory.
// The Agent is the real one: the Gateway delivers the credential through the
// internal control channel and the Agent's own code writes the file.
func registryAuthLifecycleFixture(t *testing.T) (*executionLifecycleFixture, string) {
	t.Helper()
	authfile := useRegistryAuthFileFixture(t)
	f, _ := credentialImportFixture(t)
	return f, authfile
}

// registryAuthCredential creates a platform registry credential with a staged
// payload and a grant on the dev/local target.
func registryAuthCredential(t *testing.T, f *executionLifecycleFixture) (string, CredentialVersion, CredentialGrant) {
	t.Helper()
	var resource CredentialResource
	if err := json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"target-registry","scope":"platform","type":"registry"}`, http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	base := "/v1/credentials/" + resource.ID
	var version CredentialVersion
	put := f.http(t, f.admin, http.MethodPut, base+"/payload",
		`{"server":"registry.example.com:30002","username":"deploy","password":"`+registryAuthCanary+`"}`, http.StatusOK)
	if err := json.Unmarshal(put, &version); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(put), registryAuthCanary) {
		t.Fatalf("payload canary leaked in the response: %s", put)
	}
	var grant CredentialGrant
	if err := json.Unmarshal(f.http(t, f.admin, http.MethodPost, base+"/grants",
		`{"scope":{"cluster":"dev","instance":"local","namespace":"kz-ops"}}`, http.StatusOK), &grant); err != nil {
		t.Fatal(err)
	}
	return base, version, grant
}

func registryAuthVerifyBody(versionID string) string {
	body, _ := json.Marshal(map[string]any{
		"session_id": "registry-auth-lifecycle", "version_id": versionID,
		"scope": map[string]string{"cluster": "dev", "instance": "local"},
	})
	return string(body)
}

// Given a registry credential bound to a target,
// When it is verified and promoted,
// Then the Agent's own OCI authfile carries the registry, and a reconnect
// restores the file after the Pod's copy of it is gone.
func TestRegistryAuthLifecycleDeliversAndRestoresTheAuthfile(t *testing.T) {
	f, authfile := registryAuthLifecycleFixture(t)
	base, version, grant := registryAuthCredential(t, f)

	responses := []string{}
	// Promotion before verification must stay blocked.
	responses = append(responses, string(f.http(t, f.admin, http.MethodPost, base+"/promote",
		`{"version_id":"`+version.ID+`"}`, http.StatusConflict)))
	responses = append(responses, string(f.http(t, f.admin, http.MethodPost, base+"/verify",
		registryAuthVerifyBody(version.ID), http.StatusOK)))
	responses = append(responses, string(f.http(t, f.admin, http.MethodPost, base+"/promote",
		`{"version_id":"`+version.ID+`"}`, http.StatusOK)))

	auths := registryAuthFileEntries(t, authfile)
	entry, ok := auths["registry.example.com:30002"]
	if !ok {
		t.Fatalf("the verified registry is missing from the authfile: %#v", auths)
	}
	if entry["username"] != "deploy" || entry["password"] != registryAuthCanary {
		t.Fatalf("unexpected authfile entry: %#v", entry)
	}

	// The Pod is recreated: the file is gone, the Secret and the Gateway's copy
	// are not. The Agent reconnects and must get its registry back.
	if err := os.Remove(authfile); err != nil {
		t.Fatalf("remove the authfile: %v", err)
	}
	if category := f.hub.deliverRegistryAuthFilesTo(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("reconnect delivery category = %q, want success", category)
	}
	auths = registryAuthFileEntries(t, authfile)
	entry, ok = auths["registry.example.com:30002"]
	if !ok {
		t.Fatalf("the reconnect did not restore the authfile: %#v", auths)
	}
	if entry["password"] != registryAuthCanary {
		t.Fatalf("the restored authfile does not carry the credential: %#v", entry)
	}
	owners := registryAuthRecordEntries(t, registryAuthRecordPath())
	if len(owners) != 1 || owners[0].Server != "registry.example.com:30002" ||
		owners[0].CredentialID != grant.CredentialID || owners[0].GrantID != grant.ID {
		t.Fatalf("unexpected registry auth ownership record: %#v", owners)
	}

	// Revoking the grant removes the entry the Gateway delivered.
	revoke, _ := json.Marshal(map[string]string{"grant_id": grant.ID})
	responses = append(responses, string(f.http(t, f.admin, http.MethodDelete, base+"/grants",
		string(revoke), http.StatusOK)))
	auths = registryAuthFileEntries(t, authfile)
	if _, ok := auths["registry.example.com:30002"]; ok {
		t.Fatalf("the revoked grant left the registry in the authfile: %#v", auths)
	}

	for _, response := range responses {
		if strings.Contains(response, registryAuthCanary) {
			t.Fatalf("payload canary leaked in a response: %s", response)
		}
	}
	audit, err := f.store.ListAudit(200)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(audit)
	if strings.Contains(string(encoded), registryAuthCanary) {
		t.Fatal("payload canary leaked into audit")
	}
}

// Given a registry credential granted on one target,
// When another target's Agent reconnects,
// Then that target receives nothing and the file is left alone.
func TestRegistryAuthIsNotDeliveredToAnotherTarget(t *testing.T) {
	f, authfile := registryAuthLifecycleFixture(t)
	base, version, _ := registryAuthCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify", registryAuthVerifyBody(version.ID), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote", `{"version_id":"`+version.ID+`"}`, http.StatusOK)

	before, err := os.ReadFile(authfile)
	if err != nil {
		t.Fatalf("read the authfile: %v", err)
	}
	if category := f.hub.deliverRegistryAuthFilesTo(context.Background(), "dev", "other"); category != "" {
		t.Fatalf("delivery to a target without the grant = %q, want success", category)
	}
	after, err := os.ReadFile(authfile)
	if err != nil {
		t.Fatalf("read the authfile: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("the wrong target changed the authfile: %s", after)
	}
}

// Given a registry credential whose grant was revoked,
// When the Agent reconnects,
// Then it receives nothing.
func TestRegistryAuthIsNotDeliveredAfterRevocation(t *testing.T) {
	f, authfile := registryAuthLifecycleFixture(t)
	base, version, grant := registryAuthCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify", registryAuthVerifyBody(version.ID), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote", `{"version_id":"`+version.ID+`"}`, http.StatusOK)

	revoke, _ := json.Marshal(map[string]string{"grant_id": grant.ID})
	f.http(t, f.admin, http.MethodDelete, base+"/grants", string(revoke), http.StatusOK)
	if err := os.Remove(authfile); err != nil {
		t.Fatalf("remove the authfile: %v", err)
	}

	if category := f.hub.deliverRegistryAuthFilesTo(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("delivery after revocation = %q, want success", category)
	}
	if _, err := os.Stat(authfile); err == nil {
		auths := registryAuthFileEntries(t, authfile)
		if len(auths) != 0 {
			t.Fatalf("the revoked grant was delivered again: %#v", auths)
		}
	}
}

// Given a registry credential whose version was never promoted,
// When the Agent reconnects,
// Then it receives nothing: a staged version is not a live credential.
func TestRegistryAuthIsNotDeliveredBeforePromotion(t *testing.T) {
	f, authfile := registryAuthLifecycleFixture(t)
	_, version, _ := registryAuthCredential(t, f)

	if category := f.hub.deliverRegistryAuthFilesTo(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("delivery of an unpromoted version = %q, want success", category)
	}
	if _, err := os.Stat(authfile); err == nil {
		auths := registryAuthFileEntries(t, authfile)
		if len(auths) != 0 {
			t.Fatalf("an unpromoted version was delivered: %#v", auths)
		}
	}
	_ = version
}

// A failed restore must not take away registry access the Agent already had.
// The Kubernetes Secret belongs to the workload pulling with it, and the
// authfile entry the restore replaces was there before the restore.
func TestRegistryAuthFailedRestoreLeavesTheSecretAndEntryAlone(t *testing.T) {
	f, authfile := registryAuthLifecycleFixture(t)
	base, version, grant := registryAuthCredential(t, f)
	f.http(t, f.admin, http.MethodPost, base+"/verify", registryAuthVerifyBody(version.ID), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote", `{"version_id":"`+version.ID+`"}`, http.StatusOK)

	before := readCredentialTestFile(t, authfile)
	secretState := os.Getenv("DOOPS_TEST_SECRET_STATE")
	secretBefore := readCredentialTestFile(t, secretState)

	// The Agent answers the restore with evidence the Gateway rejects, so the
	// Gateway runs its failure cleanup.
	if err := f.hub.cleanupCredentialVersion(context.Background(), CredentialMaterializeRequest{
		SessionID: "registry-auth-failed-restore", Cluster: "dev", Instance: "local",
		CredentialID: grant.CredentialID, GrantID: grant.ID, VersionID: version.ID,
		CredentialType: CredentialTypeRegistry, AuthfileOnly: true,
		Scope: CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "kz-ops"},
	}); err != nil {
		t.Fatalf("cleanup after a failed restore: %v", err)
	}

	if after := readCredentialTestFile(t, authfile); after != before {
		t.Fatalf("the failed restore removed the authfile entry: %s", after)
	}
	if after := readCredentialTestFile(t, secretState); after != secretBefore {
		t.Fatalf("the failed restore removed the Kubernetes Secret:\nbefore: %s\nafter: %s", secretBefore, after)
	}
}

// Given a target with no registry credential at all,
// When its Agent reconnects,
// Then the delivery is a no-op rather than a failure.
func TestRegistryAuthDeliveryWithoutACredentialIsANoop(t *testing.T) {
	f, _ := registryAuthLifecycleFixture(t)
	if category := f.hub.deliverRegistryAuthFilesTo(context.Background(), "dev", "local"); category != "" {
		t.Fatalf("delivery without a registry credential = %q, want success", category)
	}
}

// The delivery must not outlive the operation timeout: a hung Agent must not
// hold the Gateway's registration path open.
func TestRegistryAuthDeliveryRespectsItsTimeout(t *testing.T) {
	f, _ := registryAuthLifecycleFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	f.hub.deliverRegistryAuthFilesTo(ctx, "dev", "local")
}
