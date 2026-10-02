package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCredentialVersionAndGrantHistoryIsMetadataOnlyAndOwnerScoped(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("h", 32))
	store := openCredentialTestStore(t)
	owner, ownerToken := createCredentialHTTPUser(t, store, "history-owner", false)
	_, otherToken := createCredentialHTTPUser(t, store, "history-other", false)
	_, adminToken := createCredentialHTTPUser(t, store, "history-admin", true)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	resource, err := store.CreateCredential(CredentialCreateRequest{Name: "history", Scope: CredentialScopePersonal, Type: CredentialTypeRegistry, OwnerID: owner.ID, CreatedBy: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.PutCredentialVersion(CredentialVersionPutRequest{CredentialID: resource.ID, CreatedBy: owner.ID, Payload: json.RawMessage(`{"server":"registry.example.test","username":"synthetic","password":"history-payload-canary"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"versions", "grants"} {
		path := "/v1/credentials/" + resource.ID + "/" + endpoint
		for _, token := range []string{ownerToken, adminToken} {
			response := credentialHTTPRequest(t, mux, token, http.MethodGet, path, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("read metadata %s: %d %s", endpoint, response.Code, response.Body.String())
			}
			for _, forbidden := range []string{"history-payload-canary", "ciphertext", `"payload":`, `"password":`} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("history leaked sensitive field %q", forbidden)
				}
			}
			if endpoint == "versions" && !strings.Contains(response.Body.String(), version.ID) {
				t.Fatal("persisted staged version missing from fresh metadata request")
			}
		}
		denied := credentialHTTPRequest(t, mux, otherToken, http.MethodGet, path, nil)
		if denied.Code != http.StatusNotFound {
			t.Fatalf("cross-owner history %s returned %d, want 404", endpoint, denied.Code)
		}
		unauthenticated := credentialHTTPRequest(t, mux, "", http.MethodGet, path, nil)
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated history returned %d", unauthenticated.Code)
		}
	}
	deniedUser, deniedToken := createCredentialHTTPUser(t, store, "history-no-metadata", false)
	if err := store.GrantUser(deniedUser.ID, ScopeGrant{Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAsk}}); err != nil {
		t.Fatal(err)
	}
	resourceWithoutMetadata, err := store.CreateCredential(CredentialCreateRequest{Name: "no-metadata", Scope: CredentialScopePersonal, Type: CredentialTypeRegistry, OwnerID: deniedUser.ID, CreatedBy: deniedUser.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Explicitly remove the fixture helper's metadata grant before asserting denial.
	grants, err := store.ListGrants(deniedUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants {
		for _, action := range grant.Actions {
			if action == ActionCredentialMetadata || action == ActionAll {
				if err := store.DeleteGrant(grant.ID); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
	}
	response := credentialHTTPRequest(t, mux, deniedToken, http.MethodGet, "/v1/credentials/"+resourceWithoutMetadata.ID+"/versions", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("owner without metadata authority returned %d", response.Code)
	}
}
