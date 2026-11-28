package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestResolveDoSQLPromptResolutionUsesBindingAndUniqueAsset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "s1"), 0700); err != nil {
		t.Fatal(err)
	}
	bindings := []api.AgentCredentialBinding{{
		Name:         "lab-cred",
		ResourceName: "lab-secret",
		Namespace:    "test",
		Use:          "database",
	}}
	read := func(context.Context, api.AgentCredentialBinding) (map[string]string, error) {
		return map[string]string{"SPRING_DATASOURCE_URL": "jdbc:mysql://db:3306/lab_management"}, nil
	}
	discover := func(context.Context) (string, string, []doSQLDatabaseCandidate, error) {
		return "cluster-a", "node-a", []doSQLDatabaseCandidate{{
			DatabaseAssetID: "dbscan_cluster-a_node-a_test_mysql_mysql",
			Cluster:         "cluster-a",
			Instance:        "node-a",
			Namespace:       "test",
			Engine:          "mysql",
			DatabaseName:    "",
			Source:          "Deployment",
			Status:          "ready",
		}}, nil
	}

	resolution, hint, err := resolveDoSQLPromptResolution(context.Background(), bindings, "lab_management", read, discover)
	if err != nil {
		t.Fatalf("resolve prompt: %v", err)
	}
	if resolution.CredentialRef != "lab-cred" || resolution.Engine != "mysql" ||
		resolution.DatabaseName != "lab_management" ||
		resolution.DatabaseAssetID != "dbscan_cluster-a_node-a_test_mysql_mysql" {
		t.Fatalf("resolution = %#v", resolution)
	}
	if !strings.Contains(hint, "credential_ref=lab-cred") || !strings.Contains(hint, "database_asset_id=dbscan_") {
		t.Fatalf("hint = %q", hint)
	}

	cleanup, err := writeDoSQLResolution("s1", resolution)
	if err != nil {
		t.Fatalf("write resolution: %v", err)
	}
	defer cleanup()

	params := doSQLParams{
		SessionID:       "s1",
		CredentialRef:   "guessed-credential",
		Engine:          "postgres",
		DatabaseName:    "guessed-db",
		DatabaseAssetID: "dbscan_other_cluster",
	}
	applyDoSQLResolution(&params)
	if params.CredentialRef != "lab-cred" || params.Engine != "mysql" ||
		params.DatabaseName != "lab_management" || params.DatabaseAssetID != "dbscan_cluster-a_node-a_test_mysql_mysql" {
		t.Fatalf("params after resolution = %#v", params)
	}
}
