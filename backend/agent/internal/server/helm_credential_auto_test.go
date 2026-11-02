package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// helmAutoCanary stands in for a repository password. No test or fixture may
// carry a real credential, and no assertion may expect this value back out.
const helmAutoCanary = "synthetic-helm-canary-not-a-real-password"

// helmAutoCredential creates an active helmRepository credential granted on
// cluster/instance for use=helmPull, which is what a target binds for Chart
// downloads.
func helmAutoCredential(
	t *testing.T, f *executionLifecycleFixture, owner CreatedToken, name string, scope CredentialGrantScope,
) (CredentialResource, CredentialGrant) {
	t.Helper()
	var resource CredentialResource
	if err := json.Unmarshal(f.http(t, owner, http.MethodPost, "/v1/credentials",
		fmt.Sprintf(`{"name":%q,"scope":"personal","type":"helmRepository"}`, name), http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	version, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: resource.ID, CreatedBy: resource.OwnerID, Activate: true,
		Payload: json.RawMessage(fmt.Sprintf(
			`{"url":"https://helm.example.com","username":"deploy","password":%q}`, helmAutoCanary)),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]interface{}{"scope": scope})
	if err != nil {
		t.Fatal(err)
	}
	var grant CredentialGrant
	if err := json.Unmarshal(f.http(t, owner, http.MethodPost, "/v1/credentials/"+resource.ID+"/grants",
		string(encoded), http.StatusOK), &grant); err != nil {
		t.Fatal(err)
	}
	if resource.State != CredentialStateActive || version.State != CredentialVersionActive ||
		grant.State != CredentialStateActive {
		t.Fatal("fixture must have an active credential, version and grant")
	}
	return resource, grant
}

func helmAutoScope() CredentialGrantScope {
	return CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseHelmPull}
}

// A change task uses the Helm credential already bound to the target, so the
// operator does not name again what the grant has already settled.
func TestApplyBindsTheTargetHelmCredentialWithoutNamingIt(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	resource, grant := helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if len(authorized) != 1 {
		t.Fatalf("authorized = %d, want 1", len(authorized))
	}
	item := authorized[0]
	if item.Authorized.Resource.ID != resource.ID || item.Authorized.Grant.ID != grant.ID {
		t.Fatalf("bound the wrong credential: %#v", item.Authorized)
	}
	if item.Reference.Scope.Use != CredentialUseHelmPull {
		t.Fatalf("binding use = %q, want helmPull", item.Reference.Scope.Use)
	}
	// The automatic path must carry the same metadata the named path does.
	if item.Reference.Scope.Cluster != "dev" || item.Reference.Scope.Instance != "local" {
		t.Fatalf("binding lost its target: %#v", item.Reference.Scope)
	}
}

// Automatic selection is limited to a change task that pulls a Chart. Every
// other form must behave exactly as it did before.
func TestAutomaticHelmCredentialIsLimitedToTheDeploymentForm(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	for _, tc := range []struct {
		name   string
		action GatewayAction
		skill  string
		want   int
	}{
		{"apply", ActionExec, "", 1},
		{"read-only ask", ActionAsk, "", 0},
		{"dosql owns its own selection", ActionExec, "dosql", 0},
		{"doweb pulls no chart", ActionExec, "doweb", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
			helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
			auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
			if err != nil {
				t.Fatal(err)
			}
			authorized, err := f.hub.resolveExplicitCredentials(
				auth, "dev", "local", "do the thing", nil, tc.action, tc.skill, "", "")
			if err != nil {
				t.Fatalf("resolve credentials: %v", err)
			}
			if len(authorized) != tc.want {
				t.Fatalf("authorized = %d, want %d", len(authorized), tc.want)
			}
		})
	}
}

// A target with no Helm credential keeps downloading a public Chart
// anonymously; automatic selection must not turn that into a failure.
func TestApplyWithoutAnyHelmCredentialStaysAnonymous(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if len(authorized) != 0 {
		t.Fatalf("authorized = %d, want 0", len(authorized))
	}
}

// Naming a credential on a target that binds no Helm credential at all keeps
// working: discovery finds nothing to add, and the named selection stands on
// its own with the Chart still downloaded anonymously.
func TestNamingACredentialWithoutAnyHelmCredentialKeepsTheNamedOne(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	opaque, _, _ := credentialTargetPermissionResource(t, f, f.admin,
		CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{opaque.Name}, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if len(authorized) != 1 || authorized[0].Authorized.Resource.ID != opaque.ID {
		t.Fatalf("named credential must stand alone, got %#v", authorized)
	}
}

// Only a helmPull credential is selected automatically. A registry, TLS or
// opaque credential writes a Secret into the namespace, so creating one still
// requires the operator to ask for it by name.
func TestAutomaticSelectionIgnoresCredentialsThatWriteSecrets(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	credentialTargetPermissionResource(t, f, f.admin,
		CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if len(authorized) != 0 {
		t.Fatalf("authorized = %d, want 0 (only helmPull is automatic)", len(authorized))
	}
}

// Naming a credential that is not a Helm repository must not switch the
// automatic Helm selection off. A release can need both at once: the Chart
// comes from a private repository (helmPull, settled by the grant) while a
// runtime value needs a Secret in the namespace (opaque, which only the task
// can ask for by name). Dropping the repository credential because the task
// named the opaque one leaves the Chart download anonymous and fails the
// release, with no way to express the pair.
func TestNamingANonHelmCredentialKeepsTheAutomaticHelmSelection(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helm, _ := helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	opaque, _, _ := credentialTargetPermissionResource(t, f, f.admin,
		CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{opaque.Name}, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}

	selected := make(map[string]CredentialType, len(authorized))
	for _, item := range authorized {
		selected[item.Authorized.Resource.ID] = item.Authorized.Resource.Type
	}
	if got := selected[opaque.ID]; got != CredentialTypeOpaque {
		t.Fatalf("named opaque credential missing: type = %q, want %q", got, CredentialTypeOpaque)
	}
	if got := selected[helm.ID]; got != CredentialTypeHelmRepository {
		t.Fatalf("naming the opaque credential dropped the target's Helm credential: "+
			"type = %q, want %q (%d authorized)", got, CredentialTypeHelmRepository, len(authorized))
	}
}

// Naming the Helm credential explicitly stays exactly one selection: discovery
// must not add the same credential a second time.
func TestNamingTheHelmCredentialDoesNotSelectItTwice(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helm, _ := helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{helm.Name}, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if len(authorized) != 1 || authorized[0].Authorized.Resource.ID != helm.ID {
		t.Fatalf("named Helm credential must be selected once, got %#v", authorized)
	}
}

// Adding the target's Helm credential beside a named one must not widen
// authority: a user without the target still gets nothing, so the pair cannot
// become a way to reach a credential the grant does not cover.
func TestNamingACredentialKeepsTargetAuthorityForTheAutomaticOne(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	opaque, _, _ := credentialTargetPermissionResource(t, f, f.admin,
		CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret})
	outsider := f.user(t, "no-target", ScopeGrant{Cluster: "other", Instance: "other"})
	auth, err := f.store.VerifyUserToken(outsider.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{opaque.Name}, ActionExec, "", "", "")
	if got := credentialErrorCategory(err); got != "credential_forbidden" {
		t.Fatalf("category = %q, want credential_forbidden (err=%v)", got, err)
	}
}

// The combined selection exposes the same metadata the named path does and
// nothing more: neither credential's payload may reach the bindings the model
// sees or the audit trail.
func TestNamingACredentialBesideTheAutomaticOneNeverReturnsPayloads(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	opaque, _, _ := credentialTargetPermissionResource(t, f, f.admin,
		CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{opaque.Name}, ActionExec, "", "", "")
	if err != nil || len(authorized) != 2 {
		t.Fatalf("resolve credentials: %v (%d authorized)", err, len(authorized))
	}
	events, err := f.store.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]interface{}{
		"authorized selection": authorized,
		"audit trail":          events,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, canary := range []string{helmAutoCanary, credentialTargetPermissionCanary} {
			if strings.Contains(string(encoded), canary) {
				t.Fatalf("%s leaked a credential payload", name)
			}
		}
	}
}

// Ambiguity is still reported when the task names a non-Helm credential and
// several Helm repositories are bound: which repository the Chart comes from
// stays the task's to state.
func TestNamingANonHelmCredentialStillReportsHelmAmbiguity(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helmAutoCredential(t, f, f.admin, "first-helm", helmAutoScope())
	helmAutoCredential(t, f, f.admin, "second-helm", helmAutoScope())
	opaque, _, _ := credentialTargetPermissionResource(t, f, f.admin,
		CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret})
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{opaque.Name}, ActionExec, "", "", "")
	if got := credentialErrorCategory(err); got != "credential_reference_ambiguous" {
		t.Fatalf("category = %q, want credential_reference_ambiguous (err=%v)", got, err)
	}
}

// Which repository a Chart comes from is the task's to state. Two bound Helm
// credentials must report ambiguity instead of a guess.
func TestSeveralBoundHelmCredentialsReportAmbiguity(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helmAutoCredential(t, f, f.admin, "first-helm", helmAutoScope())
	helmAutoCredential(t, f, f.admin, "second-helm", helmAutoScope())
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
	if got := credentialErrorCategory(err); got != "credential_reference_ambiguous" {
		t.Fatalf("category = %q, want credential_reference_ambiguous (err=%v)", got, err)
	}
}

// Naming a credential keeps working and wins over discovery.
func TestNamedHelmCredentialStillTakesPrecedence(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	first, _ := helmAutoCredential(t, f, f.admin, "first-helm", helmAutoScope())
	helmAutoCredential(t, f, f.admin, "second-helm", helmAutoScope())
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Two are bound, so discovery would be ambiguous; the name settles it.
	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", []string{"first-helm"}, ActionExec, "", "", "")
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if len(authorized) != 1 || authorized[0].Authorized.Resource.ID != first.ID {
		t.Fatalf("named selection ignored: %#v", authorized)
	}
}

// Automatic selection must not widen authority: a user without the target still
// gets nothing, and the credential's own grant still has to match the target.
func TestAutomaticHelmCredentialKeepsTargetAuthority(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	t.Run("user without the target", func(t *testing.T) {
		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
		helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
		outsider := f.user(t, "no-target", ScopeGrant{Cluster: "other", Instance: "other"})
		auth, err := f.store.VerifyUserToken(outsider.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.hub.resolveExplicitCredentials(
			auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
		if got := credentialErrorCategory(err); got != "credential_forbidden" {
			t.Fatalf("category = %q, want credential_forbidden (err=%v)", got, err)
		}
	})
	t.Run("credential granted on another target", func(t *testing.T) {
		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
		scope := helmAutoScope()
		scope.Cluster, scope.Instance = "other", "other"
		helmAutoCredential(t, f, f.admin, "elsewhere-helm", scope)
		auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		authorized, err := f.hub.resolveExplicitCredentials(
			auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
		if err != nil {
			t.Fatalf("resolve credentials: %v", err)
		}
		if len(authorized) != 0 {
			t.Fatalf("authorized = %d, want 0 (grant is on another target)", len(authorized))
		}
	})
}

// A prompt carrying inline credential material is still rejected, whether or
// not a credential was named.
func TestAutomaticHelmCredentialStillRejectsInlineMaterial(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release with Authorization: Bearer abcdef123456", nil, ActionExec, "", "", "")
	if !errors.Is(err, ErrInlineCredentialMaterial) {
		t.Fatalf("automatic selection must still reject inline material, got %v", err)
	}
}

// The automatic path must expose the same metadata the named path does and
// nothing more: the repository password stays inside the Gateway, so it may
// not reach the binding the model sees, nor the audit trail.
func TestAutomaticHelmCredentialNeverReturnsThePayload(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
	helmAutoCredential(t, f, f.admin, "target-helm", helmAutoScope())
	auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := f.hub.resolveExplicitCredentials(
		auth, "dev", "local", "release app in apps", nil, ActionExec, "", "", "")
	if err != nil || len(authorized) != 1 {
		t.Fatalf("resolve credentials: %v (%d authorized)", err, len(authorized))
	}
	events, err := f.store.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]interface{}{
		"authorized selection": authorized,
		"audit trail":          events,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), helmAutoCanary) {
			t.Fatalf("%s leaked the credential payload", name)
		}
		if strings.Contains(string(encoded), "deploy") && strings.Contains(string(encoded), "password") {
			t.Fatalf("%s carries payload fields", name)
		}
	}
}

// helmCredentialIDs selects by stable ID so two resources sharing a name cannot
// make the automatic choice ambiguous, and reports each credential once.
func TestHelmCredentialIDsSelectsActiveHelmPullByID(t *testing.T) {
	helm := func(id string, state CredentialState, use CredentialUse) CredentialListEntry {
		return CredentialListEntry{
			CredentialResource: CredentialResource{ID: id, Name: "shared", Type: CredentialTypeHelmRepository, State: state},
			Grants:             []CredentialGrant{{Scope: CredentialGrantScope{Use: use}}},
		}
	}
	for _, tc := range []struct {
		name    string
		entries []CredentialListEntry
		want    []string
	}{
		{"active helmPull", []CredentialListEntry{helm("cred_a", CredentialStateActive, CredentialUseHelmPull)}, []string{"cred_a"}},
		{"same name, two IDs", []CredentialListEntry{
			helm("cred_a", CredentialStateActive, CredentialUseHelmPull),
			helm("cred_b", CredentialStateActive, CredentialUseHelmPull),
		}, []string{"cred_a", "cred_b"}},
		{"revoking is skipped", []CredentialListEntry{helm("cred_a", CredentialStateRevoking, CredentialUseHelmPull)}, nil},
		{"other use is skipped", []CredentialListEntry{helm("cred_a", CredentialStateActive, CredentialUseImagePull)}, nil},
		{"empty use falls back to the type", []CredentialListEntry{helm("cred_a", CredentialStateActive, "")}, []string{"cred_a"}},
		{"wrong type is skipped", []CredentialListEntry{{
			CredentialResource: CredentialResource{ID: "cred_a", Type: CredentialTypeRegistry, State: CredentialStateActive},
			Grants:             []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseHelmPull}}},
		}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := helmCredentialIDs(tc.entries)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("helmCredentialIDs = %v, want %v", got, tc.want)
			}
		})
	}
}
