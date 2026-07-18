package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCredentialHTTPPersonalOwnershipPlatformBoundaryAndPayloadRedaction(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("h", 32))
	store := openCredentialTestStore(t)
	owner, ownerToken := createCredentialHTTPUser(t, store, "owner", false)
	_, adminToken := createCredentialHTTPUser(t, store, "admin", true)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	personal := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost, "/v1/credentials", map[string]any{
		"name":  "owner-registry",
		"scope": "personal",
		"type":  "registry",
	})
	if personal.Code != http.StatusOK {
		t.Fatalf("create personal credential: HTTP %d: %s", personal.Code, personal.Body.String())
	}
	var personalBody CredentialResource
	if err := json.Unmarshal(personal.Body.Bytes(), &personalBody); err != nil {
		t.Fatalf("decode personal credential: %v", err)
	}
	if personalBody.OwnerID != owner.ID || personalBody.Scope != CredentialScopePersonal {
		t.Fatalf("unexpected personal credential: %#v", personalBody)
	}

	platformDenied := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost, "/v1/credentials", map[string]any{
		"name":  "platform-registry",
		"scope": "platform",
		"type":  "registry",
	})
	if platformDenied.Code != http.StatusForbidden {
		t.Fatalf("non-admin platform create = HTTP %d, want 403", platformDenied.Code)
	}
	platform := credentialHTTPRequest(t, mux, adminToken, http.MethodPost, "/v1/credentials", map[string]any{
		"name":  "platform-registry",
		"scope": "platform",
		"type":  "registry",
	})
	if platform.Code != http.StatusOK {
		t.Fatalf("admin platform create: HTTP %d: %s", platform.Code, platform.Body.String())
	}

	put := credentialHTTPRequest(t, mux, ownerToken, http.MethodPut,
		"/v1/credentials/"+personalBody.ID+"/payload",
		json.RawMessage(`{"server":"registry.example.com","username":"owner","password":"http-canary-secret"}`))
	if put.Code != http.StatusOK {
		t.Fatalf("put personal payload: HTTP %d: %s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), "http-canary-secret") {
		t.Fatalf("payload response exposed credential value: %s", put.Body.String())
	}

	list := credentialHTTPRequest(t, mux, ownerToken, http.MethodGet, "/v1/credentials", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list credentials: HTTP %d: %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "http-canary-secret") {
		t.Fatalf("credential list exposed payload value: %s", list.Body.String())
	}
	audits, err := store.ListAudit(100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	auditJSON, err := json.Marshal(audits)
	if err != nil {
		t.Fatalf("marshal audits: %v", err)
	}
	if strings.Contains(string(auditJSON), "http-canary-secret") {
		t.Fatalf("credential audit exposed payload value: %s", auditJSON)
	}
}

func TestCredentialHTTPGrantRequiresOwnerTargetAuthority(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("a", 32))
	store := openCredentialTestStore(t)
	owner, ownerToken := createCredentialHTTPUser(t, store, "owner", false)
	deployer, deployerToken := createCredentialHTTPUser(t, store, "deployer", false)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	created := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost, "/v1/credentials", map[string]any{
		"name":  "owner-registry",
		"scope": "personal",
		"type":  "registry",
	})
	var credential CredentialResource
	if err := json.Unmarshal(created.Body.Bytes(), &credential); err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	grantBody := map[string]any{
		"grantee_id":  deployer.ID,
		"cluster":     "doops-edu",
		"instance":    "edu-coder",
		"project":     "oilan",
		"environment": "production",
		"template":    "oilan-agent-release",
		"namespace":   "kz-ops",
		"uses":        []string{"imagePull"},
	}
	denied := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/grants", grantBody)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("owner without target authority grant = HTTP %d, want 403", denied.Code)
	}

	if err := store.GrantUser(owner.ID, ScopeGrant{
		Cluster: "doops-edu", Instance: "edu-coder", Actions: []GatewayAction{ActionAsk},
	}); err != nil {
		t.Fatalf("grant owner target authority: %v", err)
	}
	allowed := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/grants", grantBody)
	if allowed.Code != http.StatusOK {
		t.Fatalf("owner with target authority grant: HTTP %d: %s", allowed.Code, allowed.Body.String())
	}
	var grant CredentialGrant
	if err := json.Unmarshal(allowed.Body.Bytes(), &grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	deniedRevoke := credentialHTTPRequest(t, mux, deployerToken, http.MethodDelete,
		"/v1/credentials/"+credential.ID+"/grants", map[string]any{"grant_id": grant.ID})
	if deniedRevoke.Code != http.StatusForbidden {
		t.Fatalf("grantee revoke = HTTP %d, want 403", deniedRevoke.Code)
	}
	revoked := credentialHTTPRequest(t, mux, ownerToken, http.MethodDelete,
		"/v1/credentials/"+credential.ID+"/grants", map[string]any{"grant_id": grant.ID})
	if revoked.Code != http.StatusOK {
		t.Fatalf("owner grant revoke: HTTP %d: %s", revoked.Code, revoked.Body.String())
	}
	active, err := store.ActiveCredentialGrants(credential.ID)
	if err != nil || len(active) != 0 {
		t.Fatalf("active grants after revoke = %#v, %v", active, err)
	}
}

func TestCredentialHTTPMutationFailsClosedWhenAuditCannotStart(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("f", 32))
	store := openCredentialTestStore(t)
	_, token := createCredentialHTTPUser(t, store, "audit-fail-owner", false)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_credential_audit
		BEFORE INSERT ON audit_events
		WHEN NEW.action LIKE 'credential:%'
		BEGIN
			SELECT RAISE(FAIL, 'synthetic audit failure');
		END`); err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	response := credentialHTTPRequest(t, mux, token, http.MethodPost, "/v1/credentials", map[string]any{
		"name": "must-not-exist", "scope": "personal", "type": "registry",
	})
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(response.Body.String(), "credential_audit_unavailable") {
		t.Fatalf("audit failure response = HTTP %d: %s", response.Code, response.Body.String())
	}
	credentials, err := store.ListCredentials(CredentialListFilter{})
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(credentials) != 0 {
		t.Fatalf("credential mutation continued after audit failure: %#v", credentials)
	}
}

func TestCredentialHTTPMutationRollsBackWhenAuditCannotFinish(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("f", 32))
	store := openCredentialTestStore(t)
	_, token := createCredentialHTTPUser(t, store, "audit-finish-owner", false)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_credential_audit_finish
		BEFORE UPDATE ON audit_events
		WHEN NEW.action LIKE 'credential:%' AND NEW.status = 'success'
		BEGIN
			SELECT RAISE(FAIL, 'synthetic audit finish failure');
		END`); err != nil {
		t.Fatalf("create audit finish failure trigger: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	response := credentialHTTPRequest(t, mux, token, http.MethodPost, "/v1/credentials", map[string]any{
		"name": "must-roll-back", "scope": "personal", "type": "registry",
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("audit finish failure response = HTTP %d: %s", response.Code, response.Body.String())
	}
	credentials, err := store.ListCredentials(CredentialListFilter{})
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(credentials) != 0 {
		t.Fatalf("credential mutation survived audit finish failure: %#v", credentials)
	}
}

func TestTargetScopedCredentialActionDoesNotGrantPlatformAdministration(t *testing.T) {
	store := openCredentialTestStore(t)
	platformAdmin, _ := createCredentialHTTPUser(t, store, "platform-admin", true)
	targetOperator, targetToken := createCredentialHTTPUser(t, store, "target-operator", false)
	if err := store.GrantUser(targetOperator.ID, ScopeGrant{
		Cluster: "cluster-a", Instance: "target-a", Actions: []GatewayAction{ActionCredentialRotate},
	}); err != nil {
		t.Fatalf("grant target-scoped rotate: %v", err)
	}
	resource, err := store.CreateCredential(CredentialCreateRequest{
		Name: "platform-registry", Scope: CredentialScopePlatform, Type: CredentialTypeRegistry,
		CreatedBy: platformAdmin.ID,
	})
	if err != nil {
		t.Fatalf("create platform credential: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	response := credentialHTTPRequest(t, mux, targetToken, http.MethodPut,
		"/v1/credentials/"+resource.ID+"/payload",
		json.RawMessage(`{"server":"registry.example.test","username":"canary","password":"synthetic"}`))
	if response.Code != http.StatusForbidden {
		t.Fatalf("target-scoped rotate managed platform credential: HTTP %d: %s", response.Code, response.Body.String())
	}
	if err := store.GrantUser(targetOperator.ID, ScopeGrant{
		Cluster: "cluster-a", Instance: "target-a",
		Actions: []GatewayAction{ActionCredentialMetadata, ActionCredentialGrant, ActionCredentialAudit},
	}); err != nil {
		t.Fatalf("grant target-scoped metadata actions: %v", err)
	}
	list := credentialHTTPRequest(t, mux, targetToken, http.MethodGet, "/v1/credentials", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("target-scoped credential list: HTTP %d: %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), resource.ID) {
		t.Fatalf("target-scoped credential action listed unrelated platform metadata: %s", list.Body.String())
	}
}

func TestTargetScopedCredentialAuditCannotReadOtherTargets(t *testing.T) {
	store := openCredentialTestStore(t)
	user := createCredentialTestUser(t, store, "target-auditor")
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "cluster-a", Instance: "target-a", Actions: []GatewayAction{ActionCredentialAudit},
	}); err != nil {
		t.Fatalf("grant target-scoped audit: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "target-auditor",
	})
	if err != nil {
		t.Fatalf("create target auditor token: %v", err)
	}
	other := createCredentialTestUser(t, store, "other-operator")
	for _, event := range []AuditEvent{
		{UserID: other.ID, Cluster: "cluster-a", Instance: "target-a", Action: ActionCredentialUse, CredentialID: "cred_allowed", StartedAt: time.Now().UTC()},
		{UserID: other.ID, Cluster: "cluster-b", Instance: "target-b", Action: ActionCredentialUse, CredentialID: "cred_forbidden", StartedAt: time.Now().UTC()},
		{UserID: user.ID, Action: ActionCredentialRotate, CredentialID: "cred_own", StartedAt: time.Now().UTC()},
	} {
		if _, err := store.StartAudit(event); err != nil {
			t.Fatalf("seed target audit: %v", err)
		}
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	response := credentialHTTPRequest(t, mux, token.Plaintext, http.MethodGet, "/v1/audit?limit=20", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("target-scoped audit read: HTTP %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "cred_allowed") ||
		strings.Contains(response.Body.String(), "cred_own") ||
		strings.Contains(response.Body.String(), "cred_forbidden") {
		t.Fatalf("target-scoped audit boundary failed: %s", response.Body.String())
	}
}

func TestCredentialAuditPermissionCannotReadNonCredentialEvents(t *testing.T) {
	store := openCredentialTestStore(t)
	user := createCredentialTestUser(t, store, "credential-auditor")
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionCredentialAudit},
	}); err != nil {
		t.Fatalf("grant credential audit: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{
		Kind: TokenKindUser, UserID: user.ID, Name: "credential-auditor",
	})
	if err != nil {
		t.Fatalf("create auditor token: %v", err)
	}
	for _, event := range []AuditEvent{
		{UserID: user.ID, Action: ActionCredentialUse, CredentialID: "cred_visible", StartedAt: time.Now().UTC()},
		{UserID: user.ID, Action: ActionExec, CommandSummary: "must-not-be-visible", StartedAt: time.Now().UTC()},
	} {
		if _, err := store.StartAudit(event); err != nil {
			t.Fatalf("seed audit event: %v", err)
		}
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	response := credentialHTTPRequest(t, mux, token.Plaintext, http.MethodGet, "/v1/audit?limit=20", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("credential audit read: HTTP %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "cred_visible") ||
		strings.Contains(response.Body.String(), "must-not-be-visible") {
		t.Fatalf("credential audit boundary failed: %s", response.Body.String())
	}
}

func createCredentialHTTPUser(t *testing.T, store *GatewayStore, name string, admin bool) (GatewayUser, string) {
	t.Helper()
	user, err := store.CreateUser(name)
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	actions := []GatewayAction{ActionCredentialCreate, ActionCredentialMetadata, ActionCredentialUse}
	if admin {
		actions = append(actions, ActionAdmin)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "*", Instance: "*", Actions: actions}); err != nil {
		t.Fatalf("grant credential actions: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: name})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return user, token.Plaintext
}

func credentialHTTPRequest(
	t *testing.T,
	handler http.Handler,
	token, method, path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	switch value := body.(type) {
	case nil:
	case json.RawMessage:
		payload = value
	default:
		var err error
		payload, err = json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
