package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func testManagedHelmBinding() api.AgentCredentialBinding {
	return api.AgentCredentialBinding{
		Name: "helm-test", Use: "helmPull", Namespace: "apps", ContextKind: "helm-native",
		ContextSocket: filepath.Join(os.TempDir(), "doops-native-helm-test", "broker.sock"),
		ResourceName: "doops-credential-test", ResourceVersion: "10",
		CredentialID: "cred_" + strings.Repeat("a", 24), GrantID: "credgrant_" + strings.Repeat("b", 24),
		VersionID: "credver_" + strings.Repeat("c", 24), Digest: "sha256:" + strings.Repeat("d", 64),
		HelmRelease: "app", HelmChart: "https://charts.example.test/charts/app.tgz",
	}
}

func TestHelmCredentialLeaseIsScopedMetadataAndExpiresWithPrompt(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "workspaces"))
	binding := testManagedHelmBinding()
	cleanup, err := bindHelmCredentialsForPrompt("release", []api.AgentCredentialBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path := helmCredentialLeasePath("release")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("metadata lease permissions: %v %v", info, err)
	}
	body, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(body), "password") {
		t.Fatalf("lease must contain metadata only: %v", err)
	}
	for _, mutate := range []func(*api.HelmParams){
		func(p *api.HelmParams) { p.Namespace = "other" },
		func(p *api.HelmParams) { p.Release = "other" },
		func(p *api.HelmParams) { p.Chart = "https://charts.example.test/charts/other.tgz" },
	} {
		params := api.HelmParams{SessionID: "release", Namespace: "apps", Release: "app", Chart: binding.HelmChart}
		mutate(&params)
		if _, _, err := authorizedHelmCredential(context.Background(), params); err == nil || err.Error() != "helm_credential_scope_mismatch" {
			t.Fatalf("cross-scope credential access: %v", err)
		}
	}
	if _, err := bindHelmCredentialsForPrompt("release", nil); err == nil {
		t.Fatal("an unrelated apply must not reuse an existing credential lease")
	}
	cleanup()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata lease survived prompt: %v", err)
	}
	if _, _, err := authorizedHelmCredential(context.Background(), api.HelmParams{SessionID: "../release"}); err == nil {
		t.Fatal("invalid session must be rejected")
	}
}

func TestHelmFullChartValuesDoNotMergeExistingRelease(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", t.TempDir())
	params := testHelmParams()
	params.SetStrings = nil
	params.ValueFiles = nil
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	if !command.fullChartValues || command.preserveReleaseValues ||
		!strings.Contains(strings.Join(command.args, " "), "--reset-values") || command.expectedImage != "" {
		t.Fatalf("full values command merged prior state or image overrides: %#v", command)
	}
}

func TestHelmFullValuesRejectMixedReleaseTagsBeforeMutation(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
        - name: app
          image: registry.example/app:release-20261003
        - name: proxy
          image: nginx:1.25
`
	if err := validateHelmReleaseTagManifest([]byte(manifest), "release-20261003"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.ReplaceAll(manifest, "release-20261003", "release-20261002"),
		strings.ReplaceAll(manifest, "nginx:1.25", "registry.example/other:release-20261002"),
		strings.ReplaceAll(manifest, "release-20261003", "latest"),
	} {
		if err := validateHelmReleaseTagManifest([]byte(invalid), "release-20261003"); err == nil {
			t.Fatal("invalid release images accepted")
		}
	}
}
