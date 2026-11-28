package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
)

func TestResolveExplicitCredentialsAmbiguousListsOnlyAuthorizedTargetCandidates(t *testing.T) {
	t.Run("helmPull named ambiguity lists deterministic visible candidates and resolves by id", func(t *testing.T) {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("r", 32))
		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})

		secondOwner := f.user(t, "second-helm-owner", ScopeGrant{Cluster: "*", Instance: "*"})
		thirdOwner := f.user(t, "third-helm-owner", ScopeGrant{Cluster: "*", Instance: "*"})
		operator := f.user(t, "target-operator", ScopeGrant{Cluster: "dev", Instance: "local"})
		outsider := f.user(t, "outsider-user", ScopeGrant{Cluster: "other", Instance: "other"})

		first, _ := helmAutoCredential(t, f, f.admin, "shared-helm", helmAutoScope())
		second, _ := helmAutoCredential(t, f, secondOwner, "shared-helm", helmAutoScope())

		// Same name on another target must never be disclosed in candidates.
		otherTargetScope := CredentialGrantScope{Cluster: "dev", Instance: "other", Namespace: "apps", Use: CredentialUseHelmPull}
		otherTargetCred, _ := helmAutoCredential(t, f, thirdOwner, "shared-helm", otherTargetScope)

		// Unpromoted (staged-only) credential on the same target is hidden from target visibility.
		var unpromoted CredentialResource
		if err := json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
			`{"name":"shared-helm","scope":"platform","type":"helmRepository"}`, http.StatusOK), &unpromoted); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PutCredentialVersion(CredentialVersionPutRequest{
			CredentialID: unpromoted.ID, CreatedBy: unpromoted.CreatedBy, Activate: false,
			Payload: json.RawMessage(fmt.Sprintf(`{"url":"https://staged.example.com","username":"staged-user","password":%q}`, helmAutoCanary)),
		}); err != nil {
			t.Fatal(err)
		}
		encodedGrant, err := json.Marshal(map[string]interface{}{"scope": helmAutoScope()})
		if err != nil {
			t.Fatal(err)
		}
		f.http(t, f.admin, http.MethodPost, "/v1/credentials/"+unpromoted.ID+"/grants", string(encodedGrant), http.StatusOK)

		expectedIDs := []string{first.ID, second.ID}
		sort.Strings(expectedIDs)
		wantCandidates := fmt.Sprintf("shared-helm [%s], shared-helm [%s]", expectedIDs[0], expectedIDs[1])

		// Given an authorized non-owner operator on (dev, local), When naming "shared-helm",
		// Then ambiguity lists only the two active visible target candidates and zero secrets.
		operatorAuth, err := f.store.VerifyUserToken(operator.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.hub.resolveExplicitCredentials(
			operatorAuth, "dev", "local", "release app in apps", []string{"shared-helm"}, ActionExec, "", "", "",
		)
		if !errors.Is(err, ErrCredentialAmbiguous) {
			t.Fatalf("error = %v, want ErrCredentialAmbiguous", err)
		}
		if got := credentialErrorCategory(err); got != "credential_reference_ambiguous" {
			t.Fatalf("category = %q, want credential_reference_ambiguous", got)
		}
		if !strings.Contains(err.Error(), "candidates: "+wantCandidates) {
			t.Fatalf("error %q missing deterministic candidates %q", err.Error(), wantCandidates)
		}
		for _, forbidden := range []string{
			otherTargetCred.ID,
			unpromoted.ID,
			helmAutoCanary,
			"https://helm.example.com",
			"https://staged.example.com",
			"deploy",
			"staged-user",
			second.OwnerID,
			first.OwnerID,
		} {
			assertCredentialCanaryNotInText(t, "helm reference ambiguity", err.Error(), forbidden)
		}

		// Disambiguating with a listed credential ID succeeds uniquely.
		authorized, err := f.hub.resolveExplicitCredentials(
			operatorAuth, "dev", "local", "release app in apps", []string{second.ID}, ActionExec, "", "", "",
		)
		if err != nil {
			t.Fatalf("resolve by credential ID: %v", err)
		}
		if len(authorized) != 1 || authorized[0].Authorized.Resource.ID != second.ID {
			t.Fatalf("expected single selected credential %s, got %#v", second.ID, authorized)
		}

		// Given an outsider without target access, When naming "shared-helm",
		// Then access is denied with ErrCredentialForbidden and zero credential metadata leaks.
		outsiderAuth, err := f.store.VerifyUserToken(outsider.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.hub.resolveExplicitCredentials(
			outsiderAuth, "dev", "local", "release app in apps", []string{"shared-helm"}, ActionExec, "", "", "",
		)
		if !errors.Is(err, ErrCredentialForbidden) {
			t.Fatalf("outsider error = %v, want ErrCredentialForbidden", err)
		}
		for _, forbidden := range []string{first.ID, second.ID, otherTargetCred.ID, "shared-helm", helmAutoCanary} {
			assertCredentialCanaryNotInText(t, "outsider forbidden error", err.Error(), forbidden)
		}
	})

	t.Run("dosql named ambiguity lists deterministic visible database candidates without leaking other targets", func(t *testing.T) {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("d", 32))
		f := newExecutionLifecycleFixture(t, GatewayHubOptions{})
		database := CredentialGrantScope{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseDatabase}
		otherTarget := CredentialGrantScope{Cluster: "dev", Instance: "other", Namespace: "apps", Use: CredentialUseDatabase}

		secondOwner := f.user(t, "second-db-owner", ScopeGrant{Cluster: "*", Instance: "*"})
		thirdOwner := f.user(t, "third-db-owner", ScopeGrant{Cluster: "*", Instance: "*"})
		first, _ := namedDatabaseCredential(t, f, f.admin, "shared-db", database)
		second, _ := namedDatabaseCredential(t, f, secondOwner, "shared-db", database)
		unrelated, _ := namedDatabaseCredential(t, f, f.admin, "other-db", database)
		elsewhere, _ := namedDatabaseCredential(t, f, thirdOwner, "shared-db", otherTarget)

		auth, err := f.store.VerifyUserToken(f.admin.Plaintext)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.hub.resolveExplicitCredentials(
			auth, "dev", "local", "SELECT 1", nil, ActionAsk, "dosql", "auto", "shared-db",
		)
		if !errors.Is(err, ErrCredentialAmbiguous) {
			t.Fatalf("error = %v, want ErrCredentialAmbiguous", err)
		}
		if got := credentialErrorCategory(err); got != "credential_reference_ambiguous" {
			t.Fatalf("category = %q, want credential_reference_ambiguous", got)
		}
		expectedIDs := []string{first.ID, second.ID}
		sort.Strings(expectedIDs)
		wantCandidates := fmt.Sprintf("shared-db [%s], shared-db [%s]", expectedIDs[0], expectedIDs[1])
		if !strings.Contains(err.Error(), "candidates: "+wantCandidates) {
			t.Fatalf("error %q missing deterministic candidates %q", err.Error(), wantCandidates)
		}
		for _, forbidden := range []string{
			unrelated.ID,
			"other-db",
			elsewhere.ID,
			credentialTargetPermissionCanary,
			"postgres://",
			second.OwnerID,
			first.OwnerID,
		} {
			assertCredentialCanaryNotInText(t, "dosql reference ambiguity", err.Error(), forbidden)
		}
	})
}
