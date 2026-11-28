package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCredentialMaterializationInventoryKeepsEveryWorkloadContext(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	store := openCredentialTestStore(t)
	admin := createCredentialTestUser(t, store, "inventory-admin")
	if err := store.SetUserAdmin(admin.ID, true); err != nil {
		t.Fatalf("grant admin actions: %v", err)
	}
	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name: "inventory-registry", Scope: CredentialScopePlatform,
		Type: CredentialTypeRegistry, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	version, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload: json.RawMessage(
			`{"server":"registry.example.test","username":"canary","password":"not-a-real-secret"}`,
		),
		CreatedBy: admin.ID,
		Activate:  true,
	})
	if err != nil {
		t.Fatalf("put active version: %v", err)
	}
	grant, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: credential.ID,
		Scope:        testImagePullCredentialScope("api"),
		CreatedBy:    admin.ID,
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}

	for _, workload := range []string{"api", "worker"} {
		request := CredentialMaterializeRequest{
			SessionID: "inventory-session", Cluster: "doops", Instance: "runner",
			CredentialID: credential.ID, VersionID: version.ID, GrantID: grant.ID,
			CredentialType: CredentialTypeRegistry,
			Scope:          testImagePullCredentialScope(workload),
		}
		record, err := store.RecordCredentialMaterializationIntent(request)
		if err != nil {
			t.Fatalf("record %s intent: %v", workload, err)
		}
		evidence := CredentialMaterialization{
			CredentialID: credential.ID, VersionID: version.ID, GrantID: grant.ID,
			Namespace: "apps", ResourceName: credentialSecretName(credential.ID, grant.ID),
			SecretType: "kubernetes.io/dockerconfigjson", Keys: []string{".dockerconfigjson"},
			ResourceVersion: workload + "-rv", Status: "verified",
		}
		if err := store.CompleteCredentialMaterialization(record.ID, evidence); err != nil {
			t.Fatalf("complete %s intent: %v", workload, err)
		}
		if err := store.RecordCredentialVerification(CredentialVerification{
			CredentialID: credential.ID, VersionID: version.ID, GrantID: grant.ID,
			Use: request.Scope.Use, Request: request, Evidence: evidence,
		}); err != nil {
			t.Fatalf("record %s verification: %v", workload, err)
		}
	}

	materializations, err := store.CredentialMaterializations(credential.ID, version.ID, grant.ID)
	if err != nil {
		t.Fatalf("list materializations: %v", err)
	}
	verifications, err := store.CredentialVerifications(credential.ID, version.ID)
	if err != nil {
		t.Fatalf("list verifications: %v", err)
	}
	if len(materializations) != 2 || len(verifications) != 2 {
		t.Fatalf("contexts collapsed: materializations=%d verifications=%d", len(materializations), len(verifications))
	}
}

// activeInventoryGrant creates an active registry credential with an active
// grant, the only state in which a materialization intent may be recorded.
func activeInventoryGrant(t *testing.T, store *GatewayStore, name string) (string, string) {
	t.Helper()
	admin := createCredentialTestUser(t, store, name+"-admin")
	if err := store.SetUserAdmin(admin.ID, true); err != nil {
		t.Fatalf("grant admin: %v", err)
	}
	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name: name, Scope: CredentialScopePlatform, Type: CredentialTypeRegistry, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	grant, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: credential.ID, Scope: testImagePullCredentialScope("api"), CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return credential.ID, grant.ID
}

func TestCredentialMaterializationCleanupUsesNewestVersionAndClosesContext(t *testing.T) {
	store := openCredentialTestStore(t)
	credentialID, grantID := activeInventoryGrant(t, store, "rotate")
	base := CredentialMaterializeRequest{
		SessionID: "rotate-session", Cluster: "doops", Instance: "runner",
		CredentialID: credentialID, GrantID: grantID,
		CredentialType: CredentialTypeRegistry,
		Scope:          testImagePullCredentialScope("api"),
	}
	old := base
	old.VersionID = "credver_old"
	if _, err := store.RecordCredentialMaterializationIntent(old); err != nil {
		t.Fatalf("record old materialization: %v", err)
	}
	time.Sleep(time.Millisecond)
	current := base
	current.VersionID = "credver_current"
	if _, err := store.RecordCredentialMaterializationIntent(current); err != nil {
		t.Fatalf("record current materialization: %v", err)
	}

	records, err := store.CredentialMaterializations(base.CredentialID, "", base.GrantID)
	if err != nil {
		t.Fatalf("list rotated materializations: %v", err)
	}
	if len(records) != 2 || records[0].VersionID != current.VersionID {
		t.Fatalf("newest materialization was not selected first: %#v", records)
	}
	if err := store.MarkCredentialMaterializationContextCleaned(current); err != nil {
		t.Fatalf("mark materialization context cleaned: %v", err)
	}
	remaining, err := store.CredentialMaterializations(base.CredentialID, "", base.GrantID)
	if err != nil {
		t.Fatalf("list cleaned materializations: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("historical materializations remained after context cleanup: %#v", remaining)
	}
}

func TestCredentialMaterializationInventoryPreservesUnknownOutcomeForCleanup(t *testing.T) {
	store := openCredentialTestStore(t)
	credentialID, grantID := activeInventoryGrant(t, store, "unknown")
	request := CredentialMaterializeRequest{
		SessionID: "unknown-session", Cluster: "doops", Instance: "runner",
		CredentialID: credentialID, VersionID: "credver_unknown", GrantID: grantID,
		CredentialType: CredentialTypeRegistry,
		Scope:          testImagePullCredentialScope("api"),
	}
	record, err := store.RecordCredentialMaterializationIntent(request)
	if err != nil {
		t.Fatalf("record materialization intent: %v", err)
	}
	if err := store.MarkCredentialMaterializationUnknown(record.ID, "credential_target_outcome_unknown"); err != nil {
		t.Fatalf("mark unknown outcome: %v", err)
	}
	records, err := store.CredentialMaterializations(request.CredentialID, request.VersionID, request.GrantID)
	if err != nil {
		t.Fatalf("list unknown materialization: %v", err)
	}
	if len(records) != 1 || records[0].Status != "outcome_unknown" ||
		records[0].ErrorCategory != "credential_target_outcome_unknown" ||
		records[0].Request.Scope.Workload.Name != "api" {
		t.Fatalf("unknown materialization inventory lost cleanup context: %#v", records)
	}
	second := request
	second.VersionID = "credver_staged"
	second.Scope.Workload.Name = "worker"
	secondRecord, err := store.RecordCredentialMaterializationIntent(second)
	if err != nil {
		t.Fatalf("record staged materialization intent: %v", err)
	}
	if err := store.MarkCredentialMaterializationUnknown(secondRecord.ID, "verification_transport_failed"); err != nil {
		t.Fatalf("mark staged unknown outcome: %v", err)
	}
	allVersions, err := store.CredentialMaterializations(request.CredentialID, "", request.GrantID)
	if err != nil {
		t.Fatalf("list all-version materializations: %v", err)
	}
	if len(allVersions) != 2 {
		t.Fatalf("all-version inventory count = %d, want 2", len(allVersions))
	}
}

func testImagePullCredentialScope(workload string) CredentialGrantScope {
	return CredentialGrantScope{
		Cluster: "doops", Instance: "runner", Namespace: "apps",
		Use: CredentialUseImagePull,
		Workload: CredentialPlanWorkload{
			Kind: "deployment", Name: workload,
		},
		ImageRepository: "registry.example.test/team/app",
		ImageDigest:     "sha256:" + strings.Repeat("a", 64),
	}
}

func TestCredentialToolErrorCategoryAllowsOnlyStatusCodes(t *testing.T) {
	if got := credentialToolErrorCategory("registry_auth_failed"); got != "registry_auth_failed" {
		t.Fatalf("credential error category = %q", got)
	}
	if got := credentialToolErrorCategory("Authorization: Bearer synthetic-canary"); got != "credential_tool_failed" {
		t.Fatalf("secret-bearing tool error was not collapsed: %q", got)
	}
	if got := credentialToolErrorCategory("credential declaration is invalid: unsupported root kind"); got != "credential_declaration_invalid" {
		t.Fatalf("declaration error category = %q", got)
	}
}
