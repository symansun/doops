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

func prepareDoSQLFailureSession(t *testing.T, nodeScript string) []byte {
	t.Helper()
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "ws")
	session := "sql-failure"
	if err := os.MkdirAll(filepath.Join(workspaceRoot, session), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", workspaceRoot)
	t.Setenv("DOOPS_GATEWAY_CLUSTER", "cluster-a")
	t.Setenv("DOOPS_GATEWAY_INSTANCE", "node-a")

	binding := api.AgentCredentialBinding{
		Name: "orders-db", Use: string(CredentialUseDatabase), Namespace: "apps", ResourceName: "cred-secret",
		SecretType: "Opaque", Keys: []string{"connection_uri"}, ResourceVersion: "7",
		CredentialID: "cred_123456789012345678901234", GrantID: "credgrant_123456789012345678901234", VersionID: "credver_123456789012345678901234",
		DatabaseAssetID: "dbscan_cluster-a_node-a_apps_orders-postgres_postgres", EnvironmentID: "prod", DatabaseEngine: "postgres", DatabaseName: "orders",
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
	secretJSON := `{"metadata":{"resourceVersion":"7","labels":{"doops.sh/credential-id":"cred_123456789012345678901234","doops.sh/credential-grant-id":"credgrant_123456789012345678901234"},"annotations":{"doops.sh/credential-version":"credver_123456789012345678901234"}},"data":{"connection_uri":"` + base64.StdEncoding.EncodeToString([]byte("postgres://db-user:db-secret@db/orders")) + `"}}`
	writeExecutable(t, filepath.Join(bin, "kubectl"), "#!/bin/sh\ncase \"$*\" in *pods,services*) printf '%s' '{\"items\":[{\"kind\":\"StatefulSet\",\"metadata\":{\"namespace\":\"apps\",\"name\":\"orders-postgres\"},\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"image\":\"postgres:16\",\"env\":[{\"name\":\"POSTGRES_DB\",\"value\":\"orders\"}]}]}}}}]}';; *) printf '%s' '"+secretJSON+"';; esac\n")
	node := filepath.Join(bin, "node")
	writeExecutable(t, node, nodeScript)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOSQL_NODE", node)

	encoded, _ := json.Marshal(map[string]interface{}{
		"session_id": session, "operation": "query", "credential_ref": "orders-db",
		"database_asset_id": "dbscan_cluster-a_node-a_apps_orders-postgres_postgres",
		"environment_id":    "prod", "project_id": "ops", "engine": "postgres", "database_name": "orders",
		"statement": "select count(*) from users",
	})
	return encoded
}

func TestDoSQLFailureCarriesExecutorReasonWithoutSecrets(t *testing.T) {
	encoded := prepareDoSQLFailureSession(t, "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = \"--output\" ]; then out=\"$2\"; fi; shift; done\n"+
		"printf '%s' '{\"status\":\"failed\",\"error\":{\"name\":\"Error\",\"message\":\"psql: connection to postgres://db-user:db-secret@db/orders refused\"}}' > \"$out\"\nexit 1\n")
	_, err := handleDoSQLTool(t.Context(), encoded, false)
	if err == nil {
		t.Fatal("expected executor failure")
	}
	message := err.Error()
	if !strings.HasPrefix(message, "dosql_execution_failed: ") || !strings.Contains(message, "connection to") {
		t.Fatalf("executor reason missing: %s", message)
	}
	if strings.Contains(message, "db-secret") {
		t.Fatalf("credential leaked into failure: %s", message)
	}
}

func TestDoSQLFailureFallsBackToProcessOutputWhenResponseIsMissing(t *testing.T) {
	encoded := prepareDoSQLFailureSession(t, "#!/bin/sh\necho 'Cannot find module /app/skills/dosql/scripts/dosql-agent.mjs' >&2\nexit 1\n")
	_, err := handleDoSQLTool(t.Context(), encoded, false)
	if err == nil || !strings.Contains(err.Error(), "Cannot find module") {
		t.Fatalf("process output missing from failure: %v", err)
	}
}
