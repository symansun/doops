package server

import (
	"context"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestResolveDirectDoSQLBindingAcceptsUniqueBindingName(t *testing.T) {
	bindings := []api.AgentCredentialBinding{{
		Name:         "lab-cred",
		ResourceName: "lab-secret",
		Use:          "database",
	}}
	read := func(context.Context, api.AgentCredentialBinding) (map[string]string, error) {
		return map[string]string{"SPRING_DATASOURCE_URL": "jdbc:mysql://db:3306/lab_management"}, nil
	}

	got, err := resolveDirectDoSQLBinding(context.Background(), bindings, "lab-cred", read)
	if err != nil {
		t.Fatalf("resolve by binding name: %v", err)
	}
	if got.Binding.Name != "lab-cred" || got.Database != "lab_management" {
		t.Fatalf("resolved = %#v, want binding lab-cred database lab_management", got)
	}

	if _, err := resolveDirectDoSQLBinding(context.Background(), bindings, "missing", read); err == nil ||
		!strings.Contains(err.Error(), "dosql_database_not_bound") {
		t.Fatalf("unknown database error = %v, want dosql_database_not_bound", err)
	}
}
