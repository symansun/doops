package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestDoSQLReadUsesBoundCredentialMetadataAndReturnsEvidenceSummary(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "ws")
	session := "sql-read"
	if err := os.MkdirAll(filepath.Join(workspaceRoot, session), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspaceRoot)

	binding := api.AgentCredentialBinding{
		Name: "orders-db", Use: string(CredentialUseDatabase), Namespace: "apps", ResourceName: "cred-secret",
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "7",
		CredentialID: "cred_123456789012345678901234", GrantID: "credgrant_123456789012345678901234", VersionID: "credver_123456789012345678901234",
		DatabaseAssetID: "db_orders", EnvironmentID: "prod", DatabaseEngine: "postgres", DatabaseName: "orders",
	}
	leasePath := dosqlCredentialLeasePath(session)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0700); err != nil {
		t.Fatal(err)
	}
	leaseJSON, _ := json.Marshal(dosqlCredentialLease{SessionID: session, Bindings: []api.AgentCredentialBinding{binding}})
	if err := os.WriteFile(leasePath, append(leaseJSON, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	kubectl := filepath.Join(bin, "kubectl")
	secretJSON := `{"metadata":{"resourceVersion":"7","labels":{"doops.sh/credential-id":"cred_123456789012345678901234","doops.sh/credential-grant-id":"credgrant_123456789012345678901234"},"annotations":{"doops.sh/credential-version":"credver_123456789012345678901234"}},"data":{"connection_uri":"` + base64.StdEncoding.EncodeToString([]byte("postgres://db-user:db-secret@db/orders")) + `"}}`
	writeExecutable(t, kubectl, "#!/bin/sh\nprintf '%s' '"+secretJSON+"'\n")
	node := filepath.Join(bin, "node")
	writeExecutable(t, node, "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = \"--output\" ]; then out=\"$2\"; fi; shift; done\nprintf '%s' '{\"status\":\"succeeded\",\"result\":{\"status\":\"verified\",\"executionResult\":{\"affectedRows\":1,\"transactionId\":\"dbtx_test\"}}}' > \"$out\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOSQL_NODE", node)

	request := map[string]interface{}{
		"session_id": session, "operation": "query", "credential_ref": "orders-db", "database_asset_id": "db_orders",
		"environment_id": "prod", "project_id": "ops", "engine": "postgres", "database_name": "orders",
		"statement": "select count(*) from orders",
	}
	encoded, _ := json.Marshal(request)
	result, err := handleDoSQLTool(t.Context(), encoded, false)
	if err != nil {
		t.Fatalf("read query: %v", err)
	}
	if !strings.Contains(result, `"status":"verified"`) || !strings.Contains(result, "dbtx_test") {
		t.Fatalf("unexpected bounded result: %s", result)
	}
	if strings.Contains(result, "db-secret") || strings.Contains(result, "postgres://") {
		t.Fatalf("credential payload leaked into result: %s", result)
	}
}

func TestDoSQLMutationUsesBoundCredentialAndReturnsEvidenceSummary(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "ws")
	session := "sql-mutation"
	if err := os.MkdirAll(filepath.Join(workspaceRoot, session), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspaceRoot)

	binding := api.AgentCredentialBinding{
		Name: "orders-db", Use: string(CredentialUseDatabase), Namespace: "apps", ResourceName: "cred-secret",
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "7",
		CredentialID: "cred_123456789012345678901234", GrantID: "credgrant_123456789012345678901234", VersionID: "credver_123456789012345678901234",
		DatabaseAssetID: "db_orders", EnvironmentID: "test", DatabaseEngine: "postgres", DatabaseName: "orders",
	}
	leasePath := dosqlCredentialLeasePath(session)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0700); err != nil {
		t.Fatal(err)
	}
	leaseJSON, _ := json.Marshal(dosqlCredentialLease{SessionID: session, Bindings: []api.AgentCredentialBinding{binding}})
	if err := os.WriteFile(leasePath, append(leaseJSON, '\n'), 0600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	kubectl := filepath.Join(bin, "kubectl")
	secretJSON := `{"metadata":{"resourceVersion":"7","labels":{"doops.sh/credential-id":"cred_123456789012345678901234","doops.sh/credential-grant-id":"credgrant_123456789012345678901234"},"annotations":{"doops.sh/credential-version":"credver_123456789012345678901234"}},"data":{"connection_uri":"` + base64.StdEncoding.EncodeToString([]byte("postgres://db-user:db-secret@db/orders")) + `"}}`
	writeExecutable(t, kubectl, "#!/bin/sh\nprintf '%s' '"+secretJSON+"'\n")
	node := filepath.Join(bin, "node")
	writeExecutable(t, node, "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = \"--output\" ]; then out=\"$2\"; fi; shift; done\nprintf '%s' '{\"status\":\"succeeded\",\"result\":{\"status\":\"verified\",\"executionResult\":{\"affectedRows\":1,\"transactionId\":\"dbtx_mutation_test\"}}}' > \"$out\"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOSQL_NODE", node)

	request := map[string]interface{}{
		"session_id": session, "operation": "execute", "confirm": true, "credential_ref": "orders-db",
		"database_asset_id": "db_orders", "environment_id": "test", "project_id": "ops",
		"engine": "postgres", "database_name": "orders", "statement": "alter table orders add column external_id text",
		"verification_statement": "select count(*) from orders", "change_request_id": "chg_orders_001",
		"approval_artifact_path": ".dosql/operations/dbop_test.approval.json",
	}
	encoded, _ := json.Marshal(request)
	result, err := handleDoSQLTool(t.Context(), encoded, true)
	if err != nil {
		t.Fatalf("mutation execute: %v", err)
	}
	if !strings.Contains(result, `"status":"verified"`) || !strings.Contains(result, "dbtx_mutation_test") {
		t.Fatalf("unexpected bounded mutation result: %s", result)
	}
	if strings.Contains(result, "db-secret") || strings.Contains(result, "postgres://") {
		t.Fatalf("credential payload leaked into mutation result: %s", result)
	}
}

func TestDoSQLRejectsCredentialScopeMismatchBeforeExecutor(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "ws")
	session := "sql-denied"
	if err := os.MkdirAll(filepath.Join(workspaceRoot, session), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspaceRoot)
	leasePath := dosqlCredentialLeasePath(session)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0700); err != nil {
		t.Fatal(err)
	}
	binding := api.AgentCredentialBinding{Name: "orders-db", Use: string(CredentialUseDatabase), Namespace: "apps", ResourceName: "cred-secret", SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "7", CredentialID: "cred_123456789012345678901234", GrantID: "credgrant_123456789012345678901234", VersionID: "credver_123456789012345678901234", DatabaseAssetID: "db_orders", EnvironmentID: "prod", DatabaseEngine: "postgres", DatabaseName: "orders"}
	leaseJSON, _ := json.Marshal(dosqlCredentialLease{SessionID: session, Bindings: []api.AgentCredentialBinding{binding}})
	if err := os.WriteFile(leasePath, append(leaseJSON, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]interface{}{"session_id": session, "operation": "query", "credential_ref": "orders-db", "database_asset_id": "db_other", "environment_id": "prod", "project_id": "ops", "engine": "postgres", "database_name": "orders", "statement": "select 1"})
	if _, err := handleDoSQLTool(t.Context(), request, false); err == nil || !strings.Contains(err.Error(), "scope_mismatch") {
		t.Fatalf("scope mismatch was not rejected: %v", err)
	}
}

func TestDoSQLDiscoveryFindsDatabaseCandidatesWithoutReadingSecrets(t *testing.T) {
	bin := t.TempDir()
	kubectl := filepath.Join(bin, "kubectl")
	writeExecutable(t, kubectl, `#!/bin/sh
cat <<'JSON'
{"items":[
  {"kind":"Service","metadata":{"namespace":"data","name":"orders-postgres"},"spec":{"ports":[{"port":5432}]}},
  {"kind":"StatefulSet","metadata":{"namespace":"data","name":"orders-postgres"},"spec":{"template":{"spec":{"containers":[{"name":"postgres","image":"postgres:16","env":[{"name":"POSTGRES_DB","value":"orders"},{"name":"POSTGRES_PASSWORD","valueFrom":{"secretKeyRef":{"name":"db-secret","key":"password"}}}]}]}}}},
  {"kind":"StatefulSet","metadata":{"namespace":"data","name":"billing-mysql"},"spec":{"template":{"spec":{"containers":[{"name":"mysql","image":"mysql:8.4"}]}}}}
]}
JSON
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_GATEWAY_CLUSTER", "cluster-a")
	t.Setenv("DOOPS_GATEWAY_INSTANCE", "node-a")
	request, _ := json.Marshal(map[string]interface{}{"session_id": "discover", "operation": "discover"})
	result, err := handleDoSQLTool(t.Context(), request, false)
	if err != nil {
		t.Fatalf("discover databases: %v", err)
	}
	if strings.Contains(result, "POSTGRES_PASSWORD") || strings.Contains(result, "db-secret") {
		t.Fatalf("discovery leaked secret metadata: %s", result)
	}
	for _, expected := range []string{"dbscan_cluster-a_node-a_data_orders-postgres_postgres", "orders", "billing-mysql", "needs_database_name_or_credential"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("discovery result missing %q: %s", expected, result)
		}
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}
