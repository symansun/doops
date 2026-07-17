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
    source:
      repository: https://example.test/zhiyong.git
      branch: main
    target:
      name: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
      session: zhiyong-oilan-release
    services:
      frontend: deploy/environments/oilan/services/frontend.yaml
`), 0o600); err != nil {
		t.Fatal(err)
	}
	declaration, err := workflowDeclaredDeployment(path, map[string]string{
		"environment": "oilan",
		"service":     "frontend",
	})
	if err != nil {
		t.Fatalf("resolve declared source: %v", err)
	}
	if declaration.SourceRepository != "https://example.test/zhiyong.git" ||
		declaration.SourceBranch != "main" ||
		declaration.TargetName != "gw-edu-coder" ||
		declaration.TargetCluster != "doops-edu" ||
		declaration.TargetInstance != "edu-coder" ||
		declaration.TargetSession != "zhiyong-oilan-release" {
		t.Fatalf("declaration=%#v", declaration)
	}
}

func TestWorkflowCredentialPrepareRequestBindsImmutableWorkspaceCommit(t *testing.T) {
	revision := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	request := workflowCredentialPrepareRequest(
		Server{Name: "gw-release", Cluster: "doops", Instance: "release-runner"},
		"release",
		"deploy/environments.yaml",
		workflowDeclaration{
			SourceRepository: "https://example.test/app.git",
			SourceBranch:     "main",
		},
		revision,
		map[string]string{"environment": "oilan", "service": "api"},
		"apply",
	)
	if request.SourceRevision != revision || request.WorkspaceCommit != revision {
		t.Fatalf("prepare request is not bound to one immutable revision: %#v", request)
	}
}
