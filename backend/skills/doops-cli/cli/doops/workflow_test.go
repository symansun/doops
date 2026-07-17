package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowPathInGitRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "deploy", "environments.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("apiVersion: doops.sh/v3\nkind: EnvironmentRegistry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotRoot, relative, err := workflowPathInGitRepository(path)
	if err != nil {
		t.Fatalf("resolve workflow path: %v", err)
	}
	if gotRoot != root || relative != "deploy/environments.yaml" {
		t.Fatalf("root=%q relative=%q", gotRoot, relative)
	}
}

func TestWorkflowDeclaredSourceSelectsEnvironmentAndService(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "deploy", "environments.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`
apiVersion: doops.sh/v3
kind: EnvironmentRegistry
environments:
  oilan:
    target:
      session: zhiyong-oilan-release
    source:
      repository: https://example.test/zhiyong.git
      branch: main
    services:
      frontend: deploy/environments/oilan/services/frontend.yaml
`), 0o600); err != nil {
		t.Fatal(err)
	}
	repository, branch, session, err := workflowDeclaredSource(path, map[string]string{
		"environment": "oilan",
		"service":     "frontend",
	})
	if err != nil {
		t.Fatalf("resolve declared source: %v", err)
	}
	if repository != "https://example.test/zhiyong.git" || branch != "main" ||
		session != "zhiyong-oilan-release" {
		t.Fatalf("repository=%q branch=%q session=%q", repository, branch, session)
	}
}
