package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorizeDoSQLDatabaseAssetErrorListsCandidates(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "kubectl"), `#!/bin/sh
cat <<'JSON'
{"items":[{"kind":"StatefulSet","metadata":{"namespace":"apps","name":"orders-postgres"},"spec":{"template":{"spec":{"containers":[{"name":"postgres","image":"postgres:16","env":[{"name":"POSTGRES_DB","value":"orders"}]}]}}}}]}
JSON
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_GATEWAY_CLUSTER", "cluster-a")
	t.Setenv("DOOPS_GATEWAY_INSTANCE", "node-a")

	_, err := authorizeDoSQLDatabaseAsset(context.Background(), doSQLParams{
		Engine: "postgres", DatabaseName: "orders", DatabaseAssetID: "db_other",
	}, "apps")
	if err == nil || !strings.Contains(err.Error(), "database_asset_not_found") ||
		!strings.Contains(err.Error(), "dbscan_cluster-a_node-a_apps_orders-postgres_postgres") {
		t.Fatalf("error does not list available candidates: %v", err)
	}
}
