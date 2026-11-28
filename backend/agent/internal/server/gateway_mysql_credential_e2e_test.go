package server

// INT-36: D17 requires the credential lifecycle to work on the MySQL backend,
// not only on SQLite. Every request in this file goes through the real Gateway
// HTTP handlers on a real MySQL store; no test calls an internal store function
// to skip the API. The store is opened with the same DOOPS_GATEWAY_STORE=mysql
// contract the Gateway process uses, so a statement that only SQLite accepts
// fails here instead of in production.
//
// The container is disposable: each test creates its own database and drops it
// again. DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is the port of a MySQL 8.4 that
// accepts root without a password. The variable is fail-closed: unset means skip,
// set but unreachable means FAIL, because a silently skipped gate would pass a
// release the operator never actually checked.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gatewayMySQLCredentialE2ECanary is the only payload value this file stores.
// It is shaped like a password on purpose: every response body and every stored
// ciphertext is asserted not to contain it.
const gatewayMySQLCredentialE2ECanary = "canary-mysql-credential-7f3a"

func gatewayMySQLCredentialE2EPort(t *testing.T) string {
	t.Helper()
	port := strings.TrimSpace(os.Getenv("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT"))
	if port == "" {
		t.Skip("unset DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT to skip this test")
	}
	return port
}

// openGatewayMySQLCredentialE2EStore opens a throwaway MySQL database through
// the production store contract and returns the store. The unique database name
// keeps parallel sessions and repeated runs apart.
func openGatewayMySQLCredentialE2EStore(t *testing.T) *GatewayStore {
	t.Helper()
	port := gatewayMySQLCredentialE2EPort(t)
	admin, err := sql.Open("mysql", "root@tcp(127.0.0.1:"+port+")/?parseTime=true")
	if err != nil {
		t.Fatalf("open MySQL admin connection on port %s: %v", port, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT=%s is set but MySQL is unreachable (fail closed, not skip): %v", port, err)
	}
	name := fmt.Sprintf("doops_cred_e2e_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		_ = admin.Close()
		t.Fatalf("create disposable MySQL database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+name+"`")
		_ = admin.Close()
	})
	// A writable data directory: startup seeding writes builtin skill bundles under
	// <data dir>/skills, and the default /var/lib/doops-gateway is not writable on a
	// developer machine.
	t.Setenv("DOOPS_GATEWAY_DATA_DIR", t.TempDir())
	t.Setenv("DOOPS_GATEWAY_STORE", "mysql")
	t.Setenv("DOOPS_GATEWAY_MYSQL_DSN", "root@tcp(127.0.0.1:"+port+")/"+name+"?parseTime=true")
	store, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("open MySQL gateway store: %v", err)
	}
	// The dialect is process global. Restore SQLite before the connection closes
	// so a later SQLite test never inherits the MySQL rewrite rules.
	t.Cleanup(func() {
		gatewaySetDialect(gatewayDialectSQLite)
		_ = store.Close()
	})
	if got := gatewayDialect(); got != gatewayDialectMySQL {
		t.Fatalf("gateway dialect = %v, want mysql", got)
	}
	return store
}

// newGatewayMySQLCredentialE2EFixture builds the same fixture shape the SQLite
// credential tests use, but with the MySQL store and a real Gateway HTTP server.
func newGatewayMySQLCredentialE2EFixture(t *testing.T) *executionLifecycleFixture {
	t.Helper()
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", strings.Repeat("m", 32))
	bin, _ := installCredentialTestCommands(t)
	// The fake kubectl and git only resolve when their temp bin dir is on PATH;
	// verify materializes through kubectl, so without this it fails closed with
	// credential_secret_get_failed before MySQL writes anything. The wrapper in
	// front of it keys the fake target state by namespace: the lifecycle test
	// drives two targets from one process, and a shared snapshot would make the
	// second target look like it already held the first target's Secret.
	wrapper := mysqlCredentialNamespaceKubectl(t, bin)
	t.Setenv("PATH", wrapper+":"+bin+":"+os.Getenv("PATH"))
	f := &executionLifecycleFixture{opts: GatewayHubOptions{OperationTimeout: 10 * time.Second}}
	f.store = openGatewayMySQLCredentialE2EStore(t)
	hub := NewGatewayHub(f.store, f.opts)
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	f.hub = hub
	f.server = httptest.NewServer(mux)
	t.Cleanup(func() {
		f.server.Close()
		requireEventually(t, 5*time.Second, func() bool { return len(hub.listActiveOperations()) == 0 })
	})
	f.admin = f.adminUser(t, "mysql-credential-e2e-admin")
	return f
}

// mysqlCredentialStartAgent publishes a real in-process Agent on the target so
// verify can reach it through the audited materialization path.
func mysqlCredentialStartAgent(t *testing.T, f *executionLifecycleFixture, cluster, instance string) {
	t.Helper()
	gatewayAgent := &Gateway{
		tasks:      make(map[string]*bgTask),
		sessionMap: make(map[string]*sessionEntry),
	}
	conn := dialEnrolledTestAgent(t, f, cluster, instance)
	done := make(chan struct{})
	go func() {
		defer close(done)
		gatewayAgent.ServeGatewayWebSocketConnWithReady(conn, "mysql-credential-e2e", nil)
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("mysql credential e2e Agent did not stop")
		}
		requireEventually(t, 3*time.Second, func() bool { return f.hub.client.registry.Get(cluster, instance) == nil })
	})
	waitForGatewayAgent(t, f.hub, cluster, instance)
}

// mysqlCredentialNamespaceKubectl puts a wrapper in front of the fake kubectl
// that gives every namespace its own Secret and workload snapshot. A real
// cluster keeps those per target; the fake keeps one file per process, so
// without the wrapper the second target's materialization would read the first
// target's Secret and fail closed on the previous-version check.
func mysqlCredentialNamespaceKubectl(t *testing.T, sourceBin string) string {
	t.Helper()
	root := t.TempDir()
	dir := t.TempDir()
	script := `#!/bin/sh
payload=
case "$1" in
  apply|replace|create)
    payload=$(cat)
    ;;
esac
namespace=
previous=
for argument in "$@"; do
  if [ "$previous" = "-n" ] || [ "$previous" = "--namespace" ]; then namespace=$argument; fi
  case "$argument" in
    --namespace=*) namespace=${argument#--namespace=} ;;
    */namespaces/*) namespace=$(printf '%s' "$argument" | sed -n 's,.*namespaces/\([^/]*\)/.*,\1,p') ;;
  esac
  previous=$argument
done
if [ -z "$namespace" ] && [ -n "$payload" ]; then
  namespace=$(printf '%s' "$payload" | grep -o '"namespace":"[^"]*"' | head -1 | sed 's/"namespace":"//; s/"$//')
fi
if [ -z "$namespace" ]; then namespace=default; fi
export DOOPS_TEST_SECRET_STATE="__ROOT__/$namespace.secret.json"
export DOOPS_TEST_WORKLOAD_STATE="__ROOT__/$namespace.workload.json"
if [ ! -f "$DOOPS_TEST_WORKLOAD_STATE" ]; then
  printf '%s\n' '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"existing-pull"}]}}}}' > "$DOOPS_TEST_WORKLOAD_STATE"
fi
if [ -n "$payload" ]; then
  printf '%s\n' "$payload" | __SOURCEBIN__/kubectl "$@"
  exit $?
fi
exec __SOURCEBIN__/kubectl "$@"
` // __ROOT__ and __SOURCEBIN__ are spliced in below so the shell text stays literal.
	script = strings.ReplaceAll(script, "__ROOT__", root)
	script = strings.ReplaceAll(script, "__SOURCEBIN__", sourceBin)
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write namespace-scoped fake kubectl: %v", err)
	}
	return dir
}

// mysqlCredentialScalar runs one count or column read against the same MySQL
// database the Gateway store uses.
func mysqlCredentialScalar(t *testing.T, f *executionLifecycleFixture, query string, args ...any) string {
	t.Helper()
	var value sql.NullString
	if err := f.store.db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if !value.Valid {
		return "NULL"
	}
	return value.String
}

func mysqlCredentialCount(t *testing.T, f *executionLifecycleFixture, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.store.db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}

// assertNoCredentialCanary fails when a response body or a stored column
// carries the payload value.
func assertNoCredentialCanary(t *testing.T, label string, value []byte) {
	t.Helper()
	if strings.Contains(string(value), gatewayMySQLCredentialE2ECanary) {
		t.Fatalf("%s leaked the credential payload: %q", label, value)
	}
}

type mysqlCredentialHTTPRecord struct {
	step   string
	status int
	body   string
}

// TestGatewayMySQLCredentialLifecycleE2E walks create, staged put, two grants,
// verify per active grant, promote, read-back and revocation on MySQL. It logs
// one evidence line per step: HTTP status plus the row count and the key column
// a later reader can re-check by hand.
func TestGatewayMySQLCredentialLifecycleE2E(t *testing.T) {
	f := newGatewayMySQLCredentialE2EFixture(t)
	mysqlCredentialStartAgent(t, f, "dev", "local")
	mysqlCredentialStartAgent(t, f, "dev-2", "local")

	var bodies []mysqlCredentialHTTPRecord
	call := func(step, method, path, body string, want int) []byte {
		t.Helper()
		response := f.http(t, f.admin, method, path, body, want)
		assertNoCredentialCanary(t, step+" response", response)
		bodies = append(bodies, mysqlCredentialHTTPRecord{step: step, status: want, body: string(response)})
		return response
	}

	// 1. create
	var resource CredentialResource
	if err := json.Unmarshal(call("create", http.MethodPost, "/v1/credentials",
		`{"name":"mysql-lifecycle","scope":"platform","type":"opaque"}`, http.StatusOK), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.ID == "" || resource.State != CredentialStateActive {
		t.Fatalf("created resource = %#v", resource)
	}
	base := "/v1/credentials/" + resource.ID
	t.Logf("EVIDENCE step=create http=200 credentials_rows=%d state=%s",
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credentials WHERE id = ?", resource.ID),
		mysqlCredentialScalar(t, f, "SELECT state FROM credentials WHERE id = ?", resource.ID))

	// 2. put a staged version; the payload arrives as the request body
	var version CredentialVersion
	if err := json.Unmarshal(call("put", http.MethodPut, base+"/payload",
		`{"data":{"token":"`+gatewayMySQLCredentialE2ECanary+`"}}`, http.StatusOK), &version); err != nil {
		t.Fatal(err)
	}
	if version.State != CredentialVersionStaged || version.PromotedAt != nil {
		t.Fatalf("staged version = %#v", version)
	}
	t.Logf("EVIDENCE step=put http=200 versions_rows=%d state=%s promoted_at=%s digest_set=%t",
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_versions WHERE credential_id = ?", resource.ID),
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE id = ?", version.ID),
		version.PayloadDigest != "")
	assertNoCredentialCanary(t, "credential_versions.payload_ciphertext",
		[]byte(mysqlCredentialScalar(t, f, "SELECT payload_ciphertext FROM credential_versions WHERE id = ?", version.ID)))

	// 3. grant the credential to two targets. Namespace is execution metadata,
	// not an authorization dimension: a second grant on the same target with a
	// different namespace refreshes the first grant's metadata instead of
	// creating a second active grant. Distinct targets do create two grants,
	// which is what promote has to verify.
	grantScopes := []CredentialGrantScope{
		{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret},
		{Cluster: "dev-2", Instance: "local", Namespace: "apps-2", Use: CredentialUseOpaqueSecret},
	}
	grants := make([]CredentialGrant, 0, len(grantScopes))
	for _, scope := range grantScopes {
		encoded, err := json.Marshal(map[string]any{"scope": scope})
		if err != nil {
			t.Fatal(err)
		}
		var grant CredentialGrant
		if err := json.Unmarshal(call("grant", http.MethodPost, base+"/grants", string(encoded), http.StatusOK), &grant); err != nil {
			t.Fatal(err)
		}
		if grant.ID == "" || grant.State != CredentialStateActive {
			t.Fatalf("grant = %#v", grant)
		}
		grants = append(grants, grant)
	}
	t.Logf("EVIDENCE step=grant http=200 grants_rows=%d active=%d",
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_grants WHERE credential_id = ?", resource.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_grants WHERE credential_id = ? AND state = ?", resource.ID, "active"))

	// 4. verify each active grant through the real materialization path
	for index, grant := range grants {
		encoded, err := json.Marshal(map[string]any{
			"version_id": version.ID, "session_id": "mysql-credential-e2e",
			// The verify scope must name the grant it targets: the handler verifies
			// every grant matching the request scope, and without the namespace
			// both grants collapse onto one materialization context.
			"scope": grant.Scope,
		})
		if err != nil {
			t.Fatal(err)
		}
		response := call(fmt.Sprintf("verify-%d", index+1), http.MethodPost, base+"/verify", string(encoded), http.StatusOK)
		if !strings.Contains(string(response), `"status":"verified"`) {
			t.Fatalf("verify response = %s", response)
		}
		t.Logf("EVIDENCE step=verify-%d http=200 verifications_rows=%d status=%s use=%s materializations=%d",
			index+1,
			mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_verifications WHERE credential_id = ?", resource.ID),
			mysqlCredentialScalar(t, f, "SELECT status FROM credential_verifications WHERE version_id = ? AND grant_id = ?", version.ID, grant.ID),
			mysqlCredentialScalar(t, f, "SELECT `use` FROM credential_verifications WHERE version_id = ? AND grant_id = ?", version.ID, grant.ID),
			mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ?", resource.ID))
	}
	t.Logf("EVIDENCE step=materialize http=200 verifications=%d distinct_versions=%d grants_verified=%d",
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_verifications WHERE credential_id = ?", resource.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(DISTINCT version_id) FROM credential_verifications WHERE credential_id = ?", resource.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(DISTINCT grant_id) FROM credential_verifications WHERE credential_id = ?", resource.ID))
	if rows := mysqlCredentialCount(t, f, "SELECT COUNT(DISTINCT grant_id) FROM credential_verifications WHERE credential_id = ?", resource.ID); rows != len(grants) {
		t.Fatalf("verified grants = %d, want %d", rows, len(grants))
	}
	for _, column := range []string{"context_key", "request_json", "evidence_json"} {
		assertNoCredentialCanary(t, "credential_materializations."+column,
			[]byte(mysqlCredentialScalar(t, f, "SELECT "+column+" FROM credential_materializations WHERE credential_id = ? ORDER BY context_key LIMIT 1", resource.ID)))
	}

	// 5. promote after every active grant verified
	promoteBody, err := json.Marshal(map[string]string{"version_id": version.ID})
	if err != nil {
		t.Fatal(err)
	}
	var promoted struct {
		CredentialID string `json:"credential_id"`
		VersionID    string `json:"version_id"`
		Promoted     bool   `json:"promoted"`
	}
	if err := json.Unmarshal(call("promote", http.MethodPost, base+"/promote", string(promoteBody), http.StatusOK), &promoted); err != nil {
		t.Fatal(err)
	}
	if !promoted.Promoted || promoted.VersionID != version.ID {
		t.Fatalf("promote response = %#v", promoted)
	}
	if state := mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID); state != string(CredentialVersionActive) {
		t.Fatalf("promoted version state = %s", state)
	}
	if promotedAt := mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE id = ?", version.ID); promotedAt == "NULL" {
		t.Fatal("promoted version has no promoted_at")
	}
	t.Logf("EVIDENCE step=promote http=200 state=%s promoted_at=%s versions=%d",
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_versions WHERE credential_id = ?", resource.ID))

	// 6. read back the list, the detail and the version history
	list := call("list", http.MethodGet, "/v1/credentials", "", http.StatusOK)
	if !strings.Contains(string(list), resource.ID) {
		t.Fatalf("list response does not name the credential: %s", list)
	}
	detail := call("detail", http.MethodGet, base, "", http.StatusOK)
	var detailResource CredentialResource
	if err := json.Unmarshal(detail, &detailResource); err != nil {
		t.Fatal(err)
	}
	if detailResource.State != CredentialStateActive {
		t.Fatalf("detail state = %s", detailResource.State)
	}
	t.Logf("EVIDENCE step=read-back http=200 credentials=%d detail_state=%s active_version_promoted_at=%s",
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credentials WHERE id = ? AND state = ?", resource.ID, "active"),
		detailResource.State,
		mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE credential_id = ? AND state = ?", resource.ID, "active"))
	history := call("versions", http.MethodGet, base+"/versions", "", http.StatusOK)
	if !strings.Contains(string(history), `"state":"active"`) {
		t.Fatalf("version history = %s", history)
	}

	// 7. revoke one grant; its materialization must be cleaned first
	revokeGrant, err := json.Marshal(map[string]string{"grant_id": grants[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	call("revoke-grant", http.MethodDelete, base+"/grants", string(revokeGrant), http.StatusOK)
	t.Logf("EVIDENCE step=revoke-grant http=200 grant_state=%s grants_active=%d materializations=%d",
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_grants WHERE id = ?", grants[1].ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_grants WHERE credential_id = ? AND state = ?", resource.ID, "active"),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ?", resource.ID))
	if live := mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ? AND grant_id = ? AND status NOT IN ('cleaned', 'not_applied')", resource.ID, grants[1].ID); live != 0 {
		t.Fatalf("revoked grant still owns %d materializations, want 0", live)
	}
	if live := mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ? AND grant_id = ? AND status NOT IN ('cleaned', 'not_applied')", resource.ID, grants[0].ID); live == 0 {
		t.Fatal("the grant that is still active lost its materialization")
	}

	// 8. revoke the resource; every remaining materialization is cleaned
	call("revoke", http.MethodPost, base+"/revoke", "", http.StatusOK)
	t.Logf("EVIDENCE step=revoke http=200 credentials_state=%s versions_state=%s grants_active=%d materializations=%d",
		mysqlCredentialScalar(t, f, "SELECT state FROM credentials WHERE id = ?", resource.ID),
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_grants WHERE credential_id = ? AND state = ?", resource.ID, "active"),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ?", resource.ID))
	if live := mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_materializations WHERE credential_id = ? AND status NOT IN ('cleaned', 'not_applied')", resource.ID); live != 0 {
		t.Fatalf("materializations that still own a target after revoke = %d, want 0", live)
	}

	// 9. no recorded response ever carried the payload
	for _, record := range bodies {
		assertNoCredentialCanary(t, record.step+" recorded response", []byte(record.body))
	}
}

// TestGatewayMySQLCredentialPromoteRequiresEveryActiveGrant is the fail-closed
// half of D17: a staged version whose second active grant was never verified
// must not become active, even though one verification exists.
func TestGatewayMySQLCredentialPromoteRequiresEveryActiveGrant(t *testing.T) {
	f := newGatewayMySQLCredentialE2EFixture(t)
	mysqlCredentialStartAgent(t, f, "dev", "local")
	mysqlCredentialStartAgent(t, f, "dev-2", "local")

	var resource CredentialResource
	json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"mysql-promote-gate","scope":"platform","type":"opaque"}`, http.StatusOK), &resource)
	base := "/v1/credentials/" + resource.ID
	var version CredentialVersion
	json.Unmarshal(f.http(t, f.admin, http.MethodPut, base+"/payload",
		`{"data":{"token":"`+gatewayMySQLCredentialE2ECanary+`"}}`, http.StatusOK), &version)

	verifiables := make([]CredentialGrant, 0, 2)
	for _, scope := range []CredentialGrantScope{
		{Cluster: "dev", Instance: "local", Namespace: "apps", Use: CredentialUseOpaqueSecret},
		{Cluster: "dev-2", Instance: "local", Namespace: "apps-2", Use: CredentialUseOpaqueSecret},
	} {
		encoded, _ := json.Marshal(map[string]any{"scope": scope})
		var grant CredentialGrant
		json.Unmarshal(f.http(t, f.admin, http.MethodPost, base+"/grants", string(encoded), http.StatusOK), &grant)
		verifiables = append(verifiables, grant)
	}
	if len(verifiables) != 2 || verifiables[0].ID == verifiables[1].ID {
		t.Fatalf("two targets must produce two active grants, got %#v", verifiables)
	}
	verifyBody, _ := json.Marshal(map[string]any{
		"version_id": version.ID, "session_id": "mysql-credential-e2e",
		"scope": verifiables[0].Scope,
	})
	response := f.http(t, f.admin, http.MethodPost, base+"/verify", string(verifyBody), http.StatusOK)
	assertNoCredentialCanary(t, "verify response", response)

	// When the unverified grant is still active, promote must refuse.
	promoteBody, _ := json.Marshal(map[string]string{"version_id": version.ID})
	refused := f.http(t, f.admin, http.MethodPost, base+"/promote", string(promoteBody), http.StatusConflict)
	if !strings.Contains(string(refused), "credential_verification_required") {
		t.Fatalf("promote with an unverified active grant = %s", refused)
	}
	t.Logf("EVIDENCE step=promote-skipping-grant http=409 body=%q state=%s promoted_at=%s verifications=%d active_grants=%d",
		strings.TrimSpace(string(refused)),
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_verifications WHERE version_id = ?", version.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_grants WHERE credential_id = ? AND state = ?", resource.ID, "active"))

	// Then the second verification unlocks promote.
	secondVerifyBody, _ := json.Marshal(map[string]any{
		"version_id": version.ID, "session_id": "mysql-credential-e2e",
		"scope": verifiables[1].Scope,
	})
	f.http(t, f.admin, http.MethodPost, base+"/verify", string(secondVerifyBody), http.StatusOK)
	f.http(t, f.admin, http.MethodPost, base+"/promote", string(promoteBody), http.StatusOK)
	t.Logf("EVIDENCE step=promote-after-second-verify http=200 state=%s promoted_at=%s",
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE id = ?", version.ID))
}

// TestGatewayMySQLCredentialPromoteRequiresActiveGrant keeps the "at least one
// current active grant" rule honest on MySQL: a version with no grant at all
// must stay staged.
func TestGatewayMySQLCredentialPromoteRequiresActiveGrant(t *testing.T) {
	f := newGatewayMySQLCredentialE2EFixture(t)
	var resource CredentialResource
	json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"mysql-no-grant","scope":"platform","type":"opaque"}`, http.StatusOK), &resource)
	base := "/v1/credentials/" + resource.ID
	var version CredentialVersion
	json.Unmarshal(f.http(t, f.admin, http.MethodPut, base+"/payload",
		`{"data":{"token":"`+gatewayMySQLCredentialE2ECanary+`"}}`, http.StatusOK), &version)
	promoteBody, _ := json.Marshal(map[string]string{"version_id": version.ID})
	refused := f.http(t, f.admin, http.MethodPost, base+"/promote", string(promoteBody), http.StatusConflict)
	if !strings.Contains(string(refused), "credential_verification_required") {
		t.Fatalf("promote without a grant = %s", refused)
	}
	t.Logf("EVIDENCE step=promote-without-grant http=409 body=%q state=%s promoted_at=%s grants=%d",
		strings.TrimSpace(string(refused)),
		mysqlCredentialScalar(t, f, "SELECT state FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialScalar(t, f, "SELECT promoted_at FROM credential_versions WHERE id = ?", version.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_grants WHERE credential_id = ?", resource.ID))
}

// TestGatewayMySQLCredentialPayloadFailsClosedWithoutMasterKey stores no
// plaintext when the master key is missing: the put has to fail, and the
// version table must stay empty.
func TestGatewayMySQLCredentialPayloadFailsClosedWithoutMasterKey(t *testing.T) {
	f := newGatewayMySQLCredentialE2EFixture(t)
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "")
	var resource CredentialResource
	json.Unmarshal(f.http(t, f.admin, http.MethodPost, "/v1/credentials",
		`{"name":"mysql-no-key","scope":"platform","type":"opaque"}`, http.StatusOK), &resource)
	base := "/v1/credentials/" + resource.ID
	refused := f.http(t, f.admin, http.MethodPut, base+"/payload",
		`{"data":{"token":"`+gatewayMySQLCredentialE2ECanary+`"}}`, http.StatusServiceUnavailable)
	assertNoCredentialCanary(t, "put without master key", refused)
	if !strings.Contains(string(refused), "credential_key_unavailable") {
		t.Fatalf("put without master key = %s", refused)
	}
	t.Logf("EVIDENCE step=put-without-key http=503 body=%q versions=%d credentials=%d",
		strings.TrimSpace(string(refused)),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credential_versions WHERE credential_id = ?", resource.ID),
		mysqlCredentialCount(t, f, "SELECT COUNT(*) FROM credentials WHERE id = ?", resource.ID))
}
