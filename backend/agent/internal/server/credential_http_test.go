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
	_, deployerToken := createCredentialHTTPUser(t, store, "deployer", false)
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
		"scope": map[string]any{
			"cluster":   "doops-edu",
			"instance":  "edu-coder",
			"namespace": "kz-ops",
			"use":       "imagePull",
			"workload": map[string]any{
				"kind": "deployment",
				"name": "doops-agent",
			},
			"image_repository": "l8ai/doops-agent",
			"image_digest":     "sha256:" + strings.Repeat("a", 64),
		},
	}
	denied := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/grants", grantBody)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("owner without target authority grant = HTTP %d, want 403", denied.Code)
	}

	if err := store.GrantUser(owner.ID, ScopeGrant{
		Cluster: "doops-edu", Instance: "edu-coder",
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

func TestCredentialPromotionUsesOnlyActiveGrantVerifications(t *testing.T) {
	activeGrant := CredentialGrant{
		ID:    "credgrant_active",
		Scope: CredentialGrantScope{Use: CredentialUseImagePull},
	}
	revokedGrantVerification := CredentialVerification{
		GrantID: "credgrant_revoked",
		Use:     CredentialUseImagePull,
	}
	activeGrantVerification := CredentialVerification{
		GrantID: activeGrant.ID,
		Use:     activeGrant.Scope.Use,
	}

	required, ok := activeCredentialVerifications(
		[]CredentialGrant{activeGrant},
		[]CredentialVerification{revokedGrantVerification, activeGrantVerification},
	)
	if !ok || len(required) != 1 || required[0].GrantID != activeGrant.ID {
		t.Fatalf("active verification selection = %#v, %v", required, ok)
	}
	if required, ok := activeCredentialVerifications(nil, []CredentialVerification{revokedGrantVerification}); ok || len(required) != 0 {
		t.Fatalf("promotion without active grants was accepted: %#v", required)
	}
	if required, ok := activeCredentialVerifications([]CredentialGrant{activeGrant}, nil); ok || len(required) != 0 {
		t.Fatalf("promotion without active grant verification was accepted: %#v", required)
	}
}

func TestCredentialVerificationErrorFinishesAudit(t *testing.T) {
	store := openCredentialTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{})
	auditID, err := store.StartAudit(AuditEvent{
		UserID: "user_1", CredentialID: "cred_1", Action: ActionCredentialVerify,
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("start audit: %v", err)
	}
	response := httptest.NewRecorder()

	hub.writeCredentialVerificationError(
		response, auditID, credentialTargetAudit(CredentialGrantScope{Cluster: "dev", Instance: "node"}, ""),
		"credential_verification_outcome_unknown", http.StatusBadGateway,
	)

	if response.Code != http.StatusBadGateway ||
		strings.TrimSpace(response.Body.String()) != "credential_verification_outcome_unknown" {
		t.Fatalf("verification error response: HTTP %d: %s", response.Code, response.Body.String())
	}
	events, err := store.ListAudit(10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 1 || events[0].Status != "error" ||
		events[0].Error != "credential_verification_outcome_unknown" ||
		events[0].Cluster != "dev" || events[0].Instance != "node" ||
		events[0].EndedAt.IsZero() {
		t.Fatalf("verification audit was not completed: %#v", events)
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
		Cluster: "cluster-a", Instance: "target-a",
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

func TestCredentialHTTPListIsIsolatedByUserAndTarget(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("l", 32))
	store := openCredentialTestStore(t)
	admin, adminToken := createCredentialHTTPUser(t, store, "credential-admin", true)
	user, userToken := createCredentialHTTPUser(t, store, "target-user", false)
	other, otherToken := createCredentialHTTPUser(t, store, "other-user", false)
	for _, grant := range []struct {
		userID            string
		cluster, instance string
	}{
		{user.ID, "cluster-a", "instance-a"},
		{user.ID, "cluster-b", "instance-b"},
		{other.ID, "cluster-a", "instance-a"},
	} {
		if err := store.GrantUser(grant.userID, ScopeGrant{Cluster: grant.cluster, Instance: grant.instance}); err != nil {
			t.Fatalf("grant target: %v", err)
		}
	}

	owned, err := store.CreateCredential(CredentialCreateRequest{
		Name: "owned-ungranted", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
		OwnerID: user.ID, CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("create owned credential: %v", err)
	}
	targetA, err := store.CreateCredential(CredentialCreateRequest{
		Name: "target-a-platform", Scope: CredentialScopePlatform, Type: CredentialTypeRegistry,
		CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create target A credential: %v", err)
	}
	targetB, err := store.CreateCredential(CredentialCreateRequest{
		Name: "target-b-personal", Scope: CredentialScopePersonal, Type: CredentialTypeRegistry,
		OwnerID: other.ID, CreatedBy: other.ID,
	})
	if err != nil {
		t.Fatalf("create target B credential: %v", err)
	}
	for _, item := range []struct {
		resource CredentialResource
		actorID  string
	}{
		{targetA, admin.ID},
		{targetB, other.ID},
	} {
		if _, err := store.PutCredentialVersion(CredentialVersionPutRequest{
			CredentialID: item.resource.ID,
			Payload: json.RawMessage(
				`{"server":"registry.example.com","username":"synthetic","password":"synthetic"}`,
			),
			CreatedBy: item.actorID,
			Activate:  true,
		}); err != nil {
			t.Fatalf("activate target credential version: %v", err)
		}
	}
	for _, item := range []struct {
		resource CredentialResource
		scope    CredentialGrantScope
		creator  string
	}{
		{targetA, credentialHTTPTestTLSOrOpaqueScope("cluster-a", "instance-a", CredentialUseImagePull), admin.ID},
		{targetB, credentialHTTPTestTLSOrOpaqueScope("cluster-b", "instance-b", CredentialUseImagePull), other.ID},
	} {
		if _, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
			CredentialID: item.resource.ID,
			Scope:        item.scope,
			CreatedBy:    item.creator,
		}); err != nil {
			t.Fatalf("grant target credential: %v", err)
		}
	}

	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	targetAList := credentialHTTPRequest(t, mux, userToken, http.MethodGet,
		"/v1/credentials?view=target&cluster=cluster-a&instance=instance-a", nil)
	if targetAList.Code != http.StatusOK {
		t.Fatalf("list target A credentials: HTTP %d: %s", targetAList.Code, targetAList.Body.String())
	}
	if !strings.Contains(targetAList.Body.String(), targetA.ID) ||
		strings.Contains(targetAList.Body.String(), targetB.ID) ||
		strings.Contains(targetAList.Body.String(), owned.ID) ||
		strings.Contains(targetAList.Body.String(), admin.ID) ||
		strings.Contains(targetAList.Body.String(), user.ID) ||
		strings.Contains(targetAList.Body.String(), `"owner_id"`) ||
		strings.Contains(targetAList.Body.String(), `"created_by"`) ||
		strings.Contains(targetAList.Body.String(), `"grantee_id"`) ||
		!strings.Contains(targetAList.Body.String(), `"grants"`) {
		t.Fatalf("target A credential visibility mismatch: %s", targetAList.Body.String())
	}
	targetADetail := credentialHTTPRequest(t, mux, userToken, http.MethodGet,
		"/v1/credentials/"+targetA.ID+"?cluster=cluster-a&instance=instance-a", nil)
	if targetADetail.Code != http.StatusOK {
		t.Fatalf("target A credential detail: HTTP %d: %s", targetADetail.Code, targetADetail.Body.String())
	}
	if strings.Contains(targetADetail.Body.String(), admin.ID) ||
		strings.Contains(targetADetail.Body.String(), `"owner_id"`) ||
		strings.Contains(targetADetail.Body.String(), `"created_by"`) {
		t.Fatalf("target A credential detail exposed owner metadata: %s", targetADetail.Body.String())
	}
	adminDetail := credentialHTTPRequest(t, mux, adminToken, http.MethodGet,
		"/v1/credentials/"+targetA.ID, nil)
	if adminDetail.Code != http.StatusOK ||
		!strings.Contains(adminDetail.Body.String(), `"created_by":"`+admin.ID+`"`) {
		t.Fatalf("platform admin credential detail missing management metadata: HTTP %d: %s",
			adminDetail.Code, adminDetail.Body.String())
	}
	unscopedDetail := credentialHTTPRequest(t, mux, userToken, http.MethodGet,
		"/v1/credentials/"+targetA.ID, nil)
	if unscopedDetail.Code != http.StatusNotFound {
		t.Fatalf("unscoped granted credential detail = HTTP %d, want 404", unscopedDetail.Code)
	}
	wrongTargetDetail := credentialHTTPRequest(t, mux, userToken, http.MethodGet,
		"/v1/credentials/"+targetA.ID+"?cluster=cluster-b&instance=instance-b", nil)
	if wrongTargetDetail.Code != http.StatusNotFound {
		t.Fatalf("wrong-target credential detail = HTTP %d, want 404", wrongTargetDetail.Code)
	}

	targetBList := credentialHTTPRequest(t, mux, userToken, http.MethodGet,
		"/v1/credentials?view=target&cluster=cluster-b&instance=instance-b", nil)
	if targetBList.Code != http.StatusOK {
		t.Fatalf("list target B credentials: HTTP %d: %s", targetBList.Code, targetBList.Body.String())
	}
	if !strings.Contains(targetBList.Body.String(), targetB.ID) ||
		strings.Contains(targetBList.Body.String(), targetA.ID) {
		t.Fatalf("target B credential visibility mismatch: %s", targetBList.Body.String())
	}

	managed := credentialHTTPRequest(t, mux, userToken, http.MethodGet,
		"/v1/credentials?view=managed", nil)
	if managed.Code != http.StatusOK {
		t.Fatalf("list managed credentials: HTTP %d: %s", managed.Code, managed.Body.String())
	}
	if !strings.Contains(managed.Body.String(), owned.ID) ||
		strings.Contains(managed.Body.String(), targetA.ID) ||
		strings.Contains(managed.Body.String(), targetB.ID) {
		t.Fatalf("managed credential visibility mismatch: %s", managed.Body.String())
	}

	otherTargetA := credentialHTTPRequest(t, mux, otherToken, http.MethodGet,
		"/v1/credentials?view=target&cluster=cluster-a&instance=instance-a", nil)
	if otherTargetA.Code != http.StatusOK || !strings.Contains(otherTargetA.Body.String(), targetA.ID) {
		t.Fatalf("other target operator could not see target A credential: HTTP %d: %s", otherTargetA.Code, otherTargetA.Body.String())
	}
	adminTargetA := credentialHTTPRequest(t, mux, adminToken, http.MethodGet,
		"/v1/credentials?view=target&cluster=cluster-a&instance=instance-a", nil)
	if adminTargetA.Code != http.StatusOK || !strings.Contains(adminTargetA.Body.String(), targetA.ID) {
		t.Fatalf("platform admin could not use the target's credential: HTTP %d: %s",
			adminTargetA.Code, adminTargetA.Body.String())
	}
}

func TestCredentialHTTPSelfGrantUsesAuthenticatedUser(t *testing.T) {
	store := openCredentialTestStore(t)
	owner, ownerToken := createCredentialHTTPUser(t, store, "self-grant-owner", false)
	if err := store.GrantUser(owner.ID, ScopeGrant{
		Cluster: "cluster-a", Instance: "instance-a",
	}); err != nil {
		t.Fatalf("grant owner target authority: %v", err)
	}
	resource, err := store.CreateCredential(CredentialCreateRequest{
		Name: "self-grant", Scope: CredentialScopePersonal, Type: CredentialTypeTLS,
		OwnerID: owner.ID, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	response := credentialHTTPRequest(t, mux, ownerToken, http.MethodPost,
		"/v1/credentials/"+resource.ID+"/grants", map[string]any{
			"scope": credentialHTTPTestTLSOrOpaqueScope("cluster-a", "instance-a", CredentialUseTLS),
		})
	if response.Code != http.StatusOK {
		t.Fatalf("self grant: HTTP %d: %s", response.Code, response.Body.String())
	}
	var grant CredentialGrant
	if err := json.Unmarshal(response.Body.Bytes(), &grant); err != nil {
		t.Fatalf("decode self grant: %v", err)
	}
	if strings.Contains(response.Body.String(), `"grantee_id"`) {
		t.Fatalf("target binding exposed removed grantee field: %s", response.Body.String())
	}
}

func credentialHTTPTestTLSOrOpaqueScope(cluster, instance string, use CredentialUse) CredentialGrantScope {
	scope := CredentialGrantScope{
		Cluster: cluster, Instance: instance, Namespace: "apps", Use: use,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: "api"},
	}
	if use == CredentialUseImagePull {
		scope.ImageRepository = "team/api"
		scope.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	}
	return scope
}

func TestTargetScopedCredentialAuditCannotReadOtherTargets(t *testing.T) {
	store := openCredentialTestStore(t)
	user := createCredentialTestUser(t, store, "target-auditor")
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "cluster-a", Instance: "target-a",
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
	owned, err := store.CreateCredential(CredentialCreateRequest{
		Name: "auditor-owned", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
		OwnerID: user.ID, CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateCredential(CredentialCreateRequest{
		Name: "other-owned", Scope: CredentialScopePersonal, Type: CredentialTypeOpaque,
		OwnerID: other.ID, CreatedBy: other.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Visibility follows credential ownership, not the target grant.
	for _, event := range []AuditEvent{
		{UserID: other.ID, Cluster: "cluster-b", Instance: "target-b", Action: ActionCredentialUse, CredentialID: owned.ID, CommandSummary: "cred_allowed", StartedAt: time.Now().UTC()},
		{UserID: other.ID, Cluster: "cluster-a", Instance: "target-a", Action: ActionCredentialUse, CredentialID: foreign.ID, CommandSummary: "cred_forbidden", StartedAt: time.Now().UTC()},
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
		!strings.Contains(response.Body.String(), "cred_own") ||
		strings.Contains(response.Body.String(), "cred_forbidden") {
		t.Fatalf("target-scoped audit boundary failed: %s", response.Body.String())
	}
}

func TestCredentialAuditPermissionCannotReadNonCredentialEvents(t *testing.T) {
	store := openCredentialTestStore(t)
	user := createCredentialTestUser(t, store, "credential-auditor")
	other := createCredentialTestUser(t, store, "other-operator")
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "*", Instance: "*",
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
		{UserID: other.ID, Action: ActionExec, CommandSummary: "must-not-be-visible", StartedAt: time.Now().UTC()},
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
	// Credential management needs no target grant; tests add target grants explicitly.
	if admin {
		if err := store.SetUserAdmin(user.ID, true); err != nil {
			t.Fatalf("set admin: %v", err)
		}
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
