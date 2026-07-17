package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCredentialPrepareAuthorizesParsedPlanAndDoesNotLeakPayload(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("p", 32))
	store := openCredentialTestStore(t)
	deployer, deployerToken := createCredentialHTTPUser(t, store, "deployer", false)
	admin := createCredentialTestUser(t, store, "credential-admin")
	if err := store.GrantUser(admin.ID, ScopeGrant{
		Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatalf("grant credential admin: %v", err)
	}
	if err := store.GrantUser(deployer.ID, ScopeGrant{
		Cluster: "doops-edu", Instance: "edu-coder", Actions: []GatewayAction{ActionAsk, ActionCredentialUse},
	}); err != nil {
		t.Fatalf("grant deployer target actions: %v", err)
	}
	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name: "cnb-oci-pull", Scope: CredentialScopePlatform, Type: CredentialTypeRegistry, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	version, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      json.RawMessage(`{"server":"registry.example.com","username":"deploy","password":"runtime-canary-secret"}`),
		CreatedBy:    admin.ID,
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("put credential: %v", err)
	}
	grantRequest := CredentialGrantCreateRequest{
		CredentialID: credential.ID,
		GranteeID:    deployer.ID,
		Cluster:      "doops-edu",
		Instance:     "edu-coder",
		Project:      "oilan",
		Environment:  "production",
		Template:     "oilan-agent-release",
		Namespace:    "kz-ops",
		Uses:         []CredentialUse{CredentialUseImagePull},
		CreatedBy:    admin.ID,
	}
	grant, err := store.CreateCredentialGrant(grantRequest)
	if err != nil {
		t.Fatalf("create credential grant: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: time.Second})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	agentConn := dialFakeGatewayAgent(t, server.URL, "doops-edu", "edu-coder")
	defer agentConn.Close()

	var materializeCalls atomic.Int64
	var cleanupCalls atomic.Int64
	var payloadObserved atomic.Bool
	go serveCredentialRuntimeAgent(t, agentConn, &materializeCalls, &cleanupCalls, &payloadObserved, "", nil)
	waitForGatewayAgent(t, hub, "doops-edu", "edu-coder")

	dryRun := credentialHTTPRequest(t, mux, deployerToken, http.MethodPost, "/v1/credentials/prepare", map[string]any{
		"cluster":           "doops-edu",
		"instance":          "edu-coder",
		"target":            "gw-edu-coder",
		"session_id":        "release-1",
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
		"workspace_commit":  credentialRunFixtureCommit,
		"mode":              "dry-run",
	})
	if dryRun.Code != http.StatusOK {
		t.Fatalf("credential dry-run: HTTP %d: %s", dryRun.Code, dryRun.Body.String())
	}
	if materializeCalls.Load() != 0 {
		t.Fatalf("dry-run performed %d materializations", materializeCalls.Load())
	}

	apply := credentialHTTPRequest(t, mux, deployerToken, http.MethodPost, "/v1/credentials/prepare", map[string]any{
		"cluster":           "doops-edu",
		"instance":          "edu-coder",
		"target":            "gw-edu-coder",
		"session_id":        "release-1",
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
		"workspace_commit":  credentialRunFixtureCommit,
		"mode":              "apply",
	})
	if apply.Code != http.StatusOK {
		t.Fatalf("credential apply: HTTP %d: %s", apply.Code, apply.Body.String())
	}
	if materializeCalls.Load() != 1 || !payloadObserved.Load() {
		t.Fatalf("materialization calls=%d payloadObserved=%v", materializeCalls.Load(), payloadObserved.Load())
	}
	if strings.Contains(apply.Body.String(), "runtime-canary-secret") {
		t.Fatalf("credential apply response exposed payload: %s", apply.Body.String())
	}
	var run CredentialRun
	if err := json.Unmarshal(apply.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode credential run: %v", err)
	}
	if run.MutationCount != 1 || len(run.Materializations) != 1 ||
		run.Materializations[0].CredentialID != credential.ID ||
		run.Materializations[0].VersionID != version.ID ||
		run.Materializations[0].ResourceName != "" ||
		run.Materializations[0].ResourceVersion != "" ||
		run.Materializations[0].Digest != "" ||
		len(run.Materializations[0].Keys) != 0 {
		t.Fatalf("unexpected credential run: %#v", run)
	}
	_, adminToken := createCredentialHTTPUser(t, store, "credential-operator", true)
	revokeGrant := credentialHTTPRequest(t, mux, adminToken, http.MethodDelete,
		"/v1/credentials/"+credential.ID+"/grants", map[string]any{"grant_id": grant.ID})
	if revokeGrant.Code != http.StatusOK {
		t.Fatalf("credential grant revoke: HTTP %d: %s", revokeGrant.Code, revokeGrant.Body.String())
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("credential grant revoke cleanup calls=%d, want 1", cleanupCalls.Load())
	}
	if _, err := store.CreateCredentialGrant(grantRequest); err != nil {
		t.Fatalf("restore credential grant: %v", err)
	}
	staged, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      json.RawMessage(`{"server":"registry.example.com","username":"deploy","password":"rotated-canary-secret"}`),
		CreatedBy:    admin.ID,
	})
	if err != nil {
		t.Fatalf("put staged credential: %v", err)
	}
	verify := credentialHTTPRequest(t, mux, deployerToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/verify", map[string]any{
			"version_id":        staged.ID,
			"cluster":           "doops-edu",
			"instance":          "edu-coder",
			"target":            "gw-edu-coder",
			"session_id":        "release-1",
			"workflow_path":     "deploy/environments.yaml",
			"source_revision":   strings.Repeat("a", 40),
			"workspace_commit":  strings.Repeat("a", 40),
			"source_repository": "https://example.test/app.git",
			"source_branch":     "main",
			"inputs": map[string]string{
				"environment": "oilan",
				"service":     "doops-agent",
			},
		})
	if verify.Code != http.StatusForbidden {
		t.Fatalf("deployer credential verify = HTTP %d, want 403: %s", verify.Code, verify.Body.String())
	}
	verify = credentialHTTPRequest(t, mux, adminToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/verify", map[string]any{
			"version_id":        staged.ID,
			"cluster":           "doops-edu",
			"instance":          "edu-coder",
			"target":            "gw-edu-coder",
			"session_id":        "release-1",
			"workflow_path":     "deploy/environments.yaml",
			"source_revision":   strings.Repeat("a", 40),
			"workspace_commit":  strings.Repeat("a", 40),
			"source_repository": "https://example.test/app.git",
			"source_branch":     "main",
			"inputs": map[string]string{
				"environment": "oilan",
				"service":     "doops-agent",
			},
		})
	if verify.Code != http.StatusOK {
		t.Fatalf("credential verify: HTTP %d: %s", verify.Code, verify.Body.String())
	}
	for _, forbidden := range []string{
		"credential_id", "version_id", "grantId", "resourceName", "namespace",
		"secretType", "keys", "fingerprint", "expiresAt",
	} {
		if strings.Contains(verify.Body.String(), forbidden) {
			t.Fatalf("credential verify response exposed %q: %s", forbidden, verify.Body.String())
		}
	}
	if !strings.Contains(verify.Body.String(), `"status":"verified"`) ||
		!strings.Contains(verify.Body.String(), `"resourceVersion":"42"`) ||
		!strings.Contains(verify.Body.String(), `"digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"`) {
		t.Fatalf("credential verify response omitted allowed evidence: %s", verify.Body.String())
	}
	stagedMaterializations, err := store.CredentialMaterializations(credential.ID, staged.ID, grant.ID)
	if err != nil {
		t.Fatalf("list staged materializations after verify: %v", err)
	}
	if len(stagedMaterializations) != 0 {
		t.Fatalf("staged verification materialization remained active after restore: %#v", stagedMaterializations)
	}
	promote := credentialHTTPRequest(t, mux, adminToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/promote", map[string]any{"version_id": staged.ID})
	if promote.Code != http.StatusOK {
		t.Fatalf("credential promote: HTTP %d: %s", promote.Code, promote.Body.String())
	}
	active, err := store.ActiveCredentialVersion(credential.ID)
	if err != nil || active.ID != staged.ID {
		t.Fatalf("active credential after promotion = %#v, %v", active, err)
	}
	if materializeCalls.Load() != 4 {
		t.Fatalf("apply + verify/restore + promote materializations = %d, want 4", materializeCalls.Load())
	}
	revoke := credentialHTTPRequest(t, mux, adminToken, http.MethodPost,
		"/v1/credentials/"+credential.ID+"/revoke", nil)
	if revoke.Code != http.StatusOK {
		t.Fatalf("credential revoke: HTTP %d: %s", revoke.Code, revoke.Body.String())
	}
	revoked, err := store.CredentialByID(credential.ID)
	if err != nil || revoked.State != CredentialStateRevoked || cleanupCalls.Load() != 2 {
		t.Fatalf("credential revoke state=%#v cleanupCalls=%d err=%v", revoked, cleanupCalls.Load(), err)
	}
	audits, err := store.ListAudit(100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	serialized, _ := json.Marshal(audits)
	if strings.Contains(string(serialized), "runtime-canary-secret") {
		t.Fatalf("credential audit exposed payload: %s", serialized)
	}
	var verifiedAudit, materializationAudit bool
	for _, audit := range audits {
		if audit.Action == ActionCredentialVerify &&
			audit.Session == credential.ID &&
			audit.CommandSummary == "credential verify" &&
			audit.Cluster == "doops-edu" &&
			audit.Instance == "edu-coder" &&
			audit.VersionID == staged.ID &&
			audit.GrantID == grant.ID &&
			audit.Environment == "production" &&
			audit.Namespace == "kz-ops" &&
			audit.Template == "oilan-agent-release" &&
			audit.ResourceVersion == "42" &&
			audit.Digest == "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" &&
			audit.Status == "success" {
			verifiedAudit = true
		}
		if audit.Action == ActionCredentialUse &&
			audit.CredentialID == credential.ID &&
			audit.VersionID == version.ID &&
			audit.GrantID == grant.ID &&
			audit.Cluster == "doops-edu" &&
			audit.Instance == "edu-coder" &&
			audit.Environment == "production" &&
			audit.Namespace == "kz-ops" &&
			audit.Template == "oilan-agent-release" &&
			audit.ResourceVersion == "42" &&
			audit.Digest == "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" &&
			strings.HasPrefix(audit.CorrelationID, "credrun_") {
			materializationAudit = true
		}
	}
	if !verifiedAudit {
		t.Fatalf("credential verify success audit missing: %#v", audits)
	}
	if !materializationAudit {
		t.Fatalf("credential materialization audit fields missing: %#v", audits)
	}
}

func TestCredentialMaterializationRequiresExpectedPreviousVersion(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("p", 32))
	store := openCredentialTestStore(t)
	admin := createCredentialTestUser(t, store, "credential-admin")
	if err := store.GrantUser(admin.ID, ScopeGrant{
		Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatalf("grant credential admin: %v", err)
	}
	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name: "registry", Scope: CredentialScopePlatform, Type: CredentialTypeRegistry, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	active, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      json.RawMessage(`{"server":"registry.example.com","username":"deploy","password":"active-synthetic"}`),
		CreatedBy:    admin.ID,
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("put active credential version: %v", err)
	}
	staged, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      json.RawMessage(`{"server":"registry.example.com","username":"deploy","password":"staged-synthetic"}`),
		CreatedBy:    admin.ID,
	})
	if err != nil {
		t.Fatalf("put staged credential version: %v", err)
	}

	const grantID = "credgrant_test"
	materialization := CredentialMaterialization{
		CredentialID: credential.ID, GrantID: grantID, VersionID: active.ID,
		ResourceName: credentialSecretName(credential.ID, grantID),
		Namespace:    "apps", SecretType: "kubernetes.io/dockerconfigjson",
		Keys: []string{".dockerconfigjson"}, ResourceVersion: "42",
		Digest: "sha256:" + strings.Repeat("a", 64), Status: "verified",
		PreviousVersionID: staged.ID,
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	ref := CredentialPlanReference{Namespace: "apps", Use: CredentialUseImagePull}
	if err := hub.validateCredentialMaterializationResult(
		materialization, credential, grantID, ref, []string{".dockerconfigjson"}, active.ID,
	); err == nil {
		t.Fatal("unexpected previous credential version was accepted")
	}
	if err := hub.validateCredentialMaterializationResult(
		materialization, credential, grantID, ref, []string{".dockerconfigjson"}, staged.ID,
	); err != nil {
		t.Fatalf("expected staged previous credential version rejected: %v", err)
	}
}

func TestCredentialMaterializationBindingFailureRestoresExpectedVersion(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("p", 32))
	store := openCredentialTestStore(t)
	admin := createCredentialTestUser(t, store, "credential-admin")
	if err := store.GrantUser(admin.ID, ScopeGrant{
		Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatalf("grant credential admin: %v", err)
	}
	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name: "registry", Scope: CredentialScopePlatform, Type: CredentialTypeRegistry, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	active, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      json.RawMessage(`{"server":"registry.example.com","username":"deploy","password":"active-synthetic"}`),
		CreatedBy:    admin.ID,
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("put active credential version: %v", err)
	}
	staged, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: credential.ID,
		Payload:      json.RawMessage(`{"server":"registry.example.com","username":"deploy","password":"staged-synthetic"}`),
		CreatedBy:    admin.ID,
	})
	if err != nil {
		t.Fatalf("put staged credential version: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: time.Second})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	agentConn := dialFakeGatewayAgent(t, server.URL, "doops-edu", "edu-coder")
	defer agentConn.Close()

	var materializeCalls atomic.Int64
	var cleanupCalls atomic.Int64
	var payloadObserved atomic.Bool
	var currentVersion atomic.Value
	go serveCredentialRuntimeAgent(
		t,
		agentConn,
		&materializeCalls,
		&cleanupCalls,
		&payloadObserved,
		"credver_"+strings.Repeat("f", 24),
		&currentVersion,
	)
	waitForGatewayAgent(t, hub, "doops-edu", "edu-coder")

	const grantID = "credgrant_test"
	_, _, err = hub.materializeCredentialVersion(
		context.Background(),
		"doops-edu",
		"edu-coder",
		"binding-recovery",
		credential,
		staged,
		active.ID,
		grantID,
		CredentialPlanReference{Namespace: "kz-ops", Use: CredentialUseImagePull},
	)
	if err == nil || err.Error() != "credential_materialization_metadata_invalid" {
		t.Fatalf("binding mismatch error = %v", err)
	}
	if materializeCalls.Load() != 2 || cleanupCalls.Load() != 0 {
		t.Fatalf("materialize calls=%d cleanup calls=%d", materializeCalls.Load(), cleanupCalls.Load())
	}
	if got, _ := currentVersion.Load().(string); got != active.ID {
		t.Fatalf("target version after binding recovery = %q, want %q", got, active.ID)
	}
}

func TestLoadCredentialPlanRejectsIncompatibleAgentBeforeToolDispatch(t *testing.T) {
	store := openCredentialTestStore(t)
	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: time.Second})
	agent := &GatewayAgent{
		Cluster:                "doops-edu",
		Instance:               "legacy",
		Key:                    "doops-edu/legacy",
		CredentialPlanProtocol: "",
		ConnectedAt:            time.Now().UTC(),
		LastSeen:               time.Now().UTC(),
		closed:                 make(chan struct{}),
	}
	if err := hub.registry.Register(agent); err != nil {
		t.Fatalf("register legacy agent: %v", err)
	}

	_, err := hub.loadCredentialPlan(context.Background(), credentialPrepareHTTPBody{
		Cluster: "doops-edu", Instance: "legacy",
	})
	if err == nil || err.Error() != "agent_protocol_incompatible" {
		t.Fatalf("incompatible agent error = %v", err)
	}
	if got := credentialPlanHTTPStatus(err); got != http.StatusUpgradeRequired {
		t.Fatalf("incompatible agent status = %d", got)
	}
}

func TestCredentialPrepareReturnsVersionedProtocolErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		register   bool
		wantStatus int
		want       string
	}{
		{
			name:       "offline target",
			wantStatus: http.StatusServiceUnavailable,
			want:       "credential_target_offline",
		},
		{
			name:       "incompatible agent",
			register:   true,
			wantStatus: http.StatusUpgradeRequired,
			want:       "agent_protocol_incompatible",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openCredentialTestStore(t)
			deployer, token := createCredentialHTTPUser(t, store, "deployer", false)
			if err := store.GrantUser(deployer.ID, ScopeGrant{
				Cluster: "doops-edu", Instance: "legacy",
				Actions: []GatewayAction{ActionAsk, ActionCredentialUse},
			}); err != nil {
				t.Fatalf("grant deployer actions: %v", err)
			}
			hub := NewGatewayHub(store, GatewayHubOptions{OperationTimeout: time.Second})
			if test.register {
				if err := hub.registry.Register(&GatewayAgent{
					Cluster: "doops-edu", Instance: "legacy", Key: "doops-edu/legacy",
					ConnectedAt: time.Now().UTC(), LastSeen: time.Now().UTC(),
					closed: make(chan struct{}),
				}); err != nil {
					t.Fatalf("register incompatible agent: %v", err)
				}
			}
			mux := http.NewServeMux()
			hub.RegisterRoutes(mux)
			response := credentialHTTPRequest(t, mux, token, http.MethodPost, "/v1/credentials/prepare", map[string]any{
				"cluster":           "doops-edu",
				"instance":          "legacy",
				"target":            "gw-legacy",
				"session_id":        "release",
				"workflow_path":     "deploy/environments.yaml",
				"source_repository": "https://example.test/app.git",
				"source_branch":     "main",
				"source_revision":   credentialRunFixtureCommit,
				"workspace_commit":  credentialRunFixtureCommit,
				"mode":              "dry-run",
			})
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusUpgradeRequired {
				if got := response.Header().Get("Upgrade"); got != credentialPlanProtocolV2 {
					t.Fatalf("upgrade header = %q, want %q", got, credentialPlanProtocolV2)
				}
				if got := response.Header().Get("Doops-Agent-Required-Protocol"); got != credentialPlanProtocolV2 {
					t.Fatalf("required protocol header = %q, want %q", got, credentialPlanProtocolV2)
				}
			}
			var failure credentialPrepareErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
				t.Fatalf("decode protocol error: %v", err)
			}
			if failure.APIVersion != "doops.sh/v2" ||
				failure.Kind != "CredentialPrepareError" ||
				failure.Category != test.want {
				t.Fatalf("protocol error = %#v", failure)
			}
		})
	}
}

func TestCredentialMaterializationContextKeyIncludesGrant(t *testing.T) {
	base := CredentialMaterializeRequest{
		Cluster:      "doops-edu",
		Instance:     "edu-coder",
		CredentialID: "cred_registry",
		GrantID:      "grant_a",
		Namespace:    "kz-ops",
		Use:          CredentialUseImagePull,
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}
	other := base
	other.GrantID = "grant_b"
	if credentialMaterializationContextKey(base) == credentialMaterializationContextKey(other) {
		t.Fatal("different grants share one materialization context key")
	}
}

func TestCredentialPlanBundleResolvesToExactCredentialID(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("b", 32))
	store := openCredentialTestStore(t)
	admin := createCredentialTestUser(t, store, "admin")
	deployer := createCredentialTestUser(t, store, "deployer")
	if err := store.GrantUser(admin.ID, ScopeGrant{
		Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatalf("grant credential admin: %v", err)
	}
	credential, err := store.CreateCredential(CredentialCreateRequest{
		Name: "registry", Scope: CredentialScopePlatform, Type: CredentialTypeRegistry, CreatedBy: admin.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	_, err = store.CreateCredentialBundle(CredentialBundleCreateRequest{
		Name: "release-shared", Scope: CredentialScopePlatform, CreatedBy: admin.ID,
		Items: []CredentialBundleItem{{
			CredentialID:       credential.ID,
			Use:                CredentialUseImagePull,
			Namespace:          "apps",
			Workload:           CredentialPlanWorkload{Kind: "Deployment", Name: "api"},
			RegistryRepository: "team/api",
			RegistryReference:  "sha256:manifest",
		}},
	})
	if err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	refs, err := hub.resolveCredentialPlanReferences(deployer.ID, CredentialPlan{
		BundleRefs: []string{"release-shared"},
	})
	if err != nil {
		t.Fatalf("resolve bundle references: %v", err)
	}
	if len(refs) != 1 || refs[0].CredentialID != credential.ID || refs[0].Name != credential.Name {
		t.Fatalf("resolved references = %#v", refs)
	}
}

func TestCredentialPrepareRejectsLegacyWorkflowAtGateway(t *testing.T) {
	store := openCredentialTestStore(t)
	deployer, token := createCredentialHTTPUser(t, store, "legacy-workflow-deployer", false)
	if err := store.GrantUser(deployer.ID, ScopeGrant{
		Cluster: "doops-edu", Instance: "edu-coder",
		Actions: []GatewayAction{ActionAsk, ActionCredentialUse},
	}); err != nil {
		t.Fatal(err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	response := credentialHTTPRequest(t, mux, token, http.MethodPost, "/v1/credentials/prepare", map[string]any{
		"cluster":           "doops-edu",
		"instance":          "edu-coder",
		"target":            "gw-edu-coder",
		"session_id":        "legacy-release",
		"workflow_path":     "deploy/release.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
		"workspace_commit":  credentialRunFixtureCommit,
		"mode":              "dry-run",
	})
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"category":"invalid_request"`) {
		t.Fatalf("legacy workflow response = HTTP %d %s", response.Code, response.Body.String())
	}
}

func TestCredentialPrepareGrantDenialStopsBeforeMaterialization(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("d", 32))
	store := openCredentialTestStore(t)
	deployer, deployerToken := createCredentialHTTPUser(t, store, "deployer", false)
	if err := store.GrantUser(deployer.ID, ScopeGrant{
		Cluster: "doops-edu", Instance: "edu-coder", Actions: []GatewayAction{ActionAsk, ActionCredentialUse},
	}); err != nil {
		t.Fatalf("grant deployer target actions: %v", err)
	}
	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute, OperationTimeout: time.Second})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	agentConn := dialFakeGatewayAgent(t, server.URL, "doops-edu", "edu-coder")
	defer agentConn.Close()

	var materializeCalls atomic.Int64
	var cleanupCalls atomic.Int64
	var payloadObserved atomic.Bool
	go serveCredentialRuntimeAgent(t, agentConn, &materializeCalls, &cleanupCalls, &payloadObserved, "", nil)
	waitForGatewayAgent(t, hub, "doops-edu", "edu-coder")

	response := credentialHTTPRequest(t, mux, deployerToken, http.MethodPost, "/v1/credentials/prepare", map[string]any{
		"cluster":           "doops-edu",
		"instance":          "edu-coder",
		"target":            "gw-edu-coder",
		"session_id":        "release-2",
		"workflow_path":     "deploy/environments.yaml",
		"source_repository": "https://example.test/app.git",
		"source_branch":     "main",
		"source_revision":   credentialRunFixtureCommit,
		"workspace_commit":  credentialRunFixtureCommit,
		"mode":              "apply",
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("ungranted credential prepare = HTTP %d, want 403: %s", response.Code, response.Body.String())
	}
	if materializeCalls.Load() != 0 || payloadObserved.Load() {
		t.Fatalf("grant denial reached materializer: calls=%d payload=%v", materializeCalls.Load(), payloadObserved.Load())
	}
}

func serveCredentialRuntimeAgent(
	t *testing.T,
	conn *websocket.Conn,
	materializeCalls *atomic.Int64,
	cleanupCalls *atomic.Int64,
	payloadObserved *atomic.Bool,
	initialVersionID string,
	currentVersionObserved *atomic.Value,
) {
	t.Helper()
	currentVersionID := initialVersionID
	if currentVersionObserved != nil {
		currentVersionObserved.Store(currentVersionID)
	}
	for {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		switch message["method"] {
		case "initialize":
			_ = conn.WriteJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      message["id"],
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"protocols": map[string]any{"credentialPlan": credentialPlanProtocolV2},
					},
				},
			})
		case "tools/call":
			params, _ := message["params"].(map[string]any)
			name, _ := params["name"].(string)
			arguments, _ := params["arguments"].(map[string]any)
			var result any
			switch name {
			case "doops_credential_plan":
				result = CredentialPlan{
					Template:    "oilan-agent-release",
					Project:     "oilan",
					Environment: "production",
					Target:      "gw-edu-coder",
					Cluster:     "doops-edu",
					Instance:    "edu-coder",
					Session:     stringValue(arguments["session_id"]),
					CredentialRefs: []CredentialPlanReference{{
						Name:      "cnb-oci-pull",
						Use:       CredentialUseImagePull,
						Namespace: "kz-ops",
						Workload:  CredentialPlanWorkload{Kind: "Deployment", Name: "doops-agent-live"},
					}},
				}
			case "doops_credential_materialize":
				materializeCalls.Add(1)
				if strings.Contains(string(mustJSON(t, arguments)), "runtime-canary-secret") {
					payloadObserved.Store(true)
				}
				versionID := stringValue(arguments["version_id"])
				result = CredentialMaterialization{
					CredentialID: stringValue(arguments["credential_id"]),
					VersionID:    versionID,
					ResourceName: credentialSecretName(
						stringValue(arguments["credential_id"]),
						stringValue(arguments["grant_id"]),
					),
					Namespace:         "kz-ops",
					SecretType:        "kubernetes.io/dockerconfigjson",
					Keys:              []string{".dockerconfigjson"},
					ResourceVersion:   "42",
					Digest:            "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
					Status:            "verified",
					PreviousVersionID: currentVersionID,
				}
				currentVersionID = versionID
				if currentVersionObserved != nil {
					currentVersionObserved.Store(currentVersionID)
				}
			case "doops_credential_cleanup":
				cleanupCalls.Add(1)
				currentVersionID = ""
				if currentVersionObserved != nil {
					currentVersionObserved.Store(currentVersionID)
				}
				result = map[string]any{"status": "removed"}
			default:
				result = map[string]any{"error": "unexpected tool"}
			}
			raw, _ := json.Marshal(result)
			_ = conn.WriteJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      message["id"],
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": string(raw)}},
				},
			})
		}
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func TestCredentialPlanHTTPStatusDistinguishesDeclarationFromTransportFailures(t *testing.T) {
	if got := credentialPlanHTTPStatus(errors.New("credential_declaration_invalid")); got != http.StatusUnprocessableEntity {
		t.Fatalf("declaration status = %d, want %d", got, http.StatusUnprocessableEntity)
	}
	if got := credentialPlanHTTPStatus(errors.New("credential_target_offline")); got != http.StatusServiceUnavailable {
		t.Fatalf("offline status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	if got := credentialPlanHTTPStatus(errors.New("credential_tool_failed")); got != http.StatusBadGateway {
		t.Fatalf("transport status = %d, want %d", got, http.StatusBadGateway)
	}
}
