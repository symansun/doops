package server

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
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
		ResourceName:  "doops-credential-test", ResourceVersion: "10",
		CredentialID: "cred_" + strings.Repeat("a", 24), GrantID: "credgrant_" + strings.Repeat("b", 24),
		VersionID: "credver_" + strings.Repeat("c", 24), Digest: "sha256:" + strings.Repeat("d", 64),
		HelmRelease: "app", HelmChart: "https://charts.example.test/charts/app.tgz",
	}
}

func TestHelmCredentialLeaseIsScopedMetadataAndExpiresWithPrompt(t *testing.T) {
	t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "workspaces"))
	binding := testManagedHelmBinding()
	materialized, err := materializeNativeHelmCredential(t.Context(), CredentialMaterializeRequest{
		SessionID: "release", CredentialID: binding.CredentialID, GrantID: binding.GrantID,
		VersionID: binding.VersionID, Scope: CredentialGrantScope{Use: CredentialUseHelmPull, Namespace: "apps"},
	}, nativeHelmAuth{URL: "https://charts.example.test", Username: "helm", Password: "synthetic-helm-canary"}, binding.Digest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = cleanupNativeHelmCredential(CredentialCleanupRequest{
			Namespace: "apps", CredentialID: binding.CredentialID, GrantID: binding.GrantID, AnyVersion: true,
		})
	})
	binding.ContextSocket, binding.ResourceVersion = materialized.ContextSocket, materialized.ResourceVersion
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
		if _, _, err := authorizedHelmCredential(context.Background(), params); err != nil {
			t.Fatalf("release metadata must not become credential authorization: %v", err)
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
	params.ExpectedImage = ""
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(command.args, " "), "--reset-values") || command.expectedImage != "" {
		t.Fatalf("full values command merged prior state or image overrides: %#v", command)
	}
}

func TestHelmRenderedImagesSupportIndependentVersionsAndExplicitExpectation(t *testing.T) {
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
	if _, err := validateHelmRenderedImages([]byte(manifest), "registry.example/app:release-20261003"); err != nil {
		t.Fatal(err)
	}
	multiService := strings.ReplaceAll(manifest, "nginx:1.25", "registry.example/app:release-20261002")
	if _, err := validateHelmRenderedImages([]byte(multiService), "registry.example/app:release-20261003"); err != nil {
		t.Fatalf("independently versioned service rejected: %v", err)
	}
	if _, err := validateHelmRenderedImages([]byte(multiService), ""); err != nil {
		t.Fatalf("chart-provided images rejected: %v", err)
	}
	for _, invalid := range []string{
		strings.ReplaceAll(manifest, "release-20261003", "release-20261002"),
		strings.ReplaceAll(manifest, "release-20261003", "latest"),
	} {
		if _, err := validateHelmRenderedImages([]byte(invalid), "registry.example/app:release-20261003"); err == nil {
			t.Fatal("invalid release images accepted")
		}
	}
}

func TestManagedHelmChartAllowsNewReleaseAndChartVersions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	chart := managedHelmTestArchive(t, managedHelmTestEntries())
	var valueRequests int
	server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/values.yaml" {
			valueRequests++
			fmt.Fprintf(w, "replicaCount: %d\n", valueRequests)
			return
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "helm" || password != "synthetic-helm-canary" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(chart)
	}))
	params := testHelmParams()
	params.Chart = server.URL + "/charts/app.tgz"
	params.ValueFiles = []string{server.URL + "/values.yaml"}
	params.SetStrings["cors.origins"] = "https://app.example"
	binding := testManagedHelmBinding()
	binding.HelmChart = params.Chart
	materialized, err := materializeNativeHelmCredential(t.Context(), CredentialMaterializeRequest{
		SessionID: params.SessionID, CredentialID: binding.CredentialID, GrantID: binding.GrantID,
		VersionID: binding.VersionID, Scope: CredentialGrantScope{
			Use: CredentialUseHelmPull, Namespace: params.Namespace,
			HelmRelease: params.Release, HelmChart: params.Chart,
		},
	}, nativeHelmAuth{URL: server.URL, Username: "helm", Password: "synthetic-helm-canary"}, fmt.Sprintf("sha256:%x", sha256.Sum256(chart)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = cleanupNativeHelmCredential(CredentialCleanupRequest{
			Namespace: params.Namespace, CredentialID: binding.CredentialID, GrantID: binding.GrantID, AnyVersion: true,
		})
	})
	binding.ContextSocket = materialized.ContextSocket
	binding.ResourceVersion = materialized.ResourceVersion
	binding.Digest = materialized.Digest
	cleanupLease, err := bindHelmCredentialsForPrompt(params.SessionID, []api.AgentCredentialBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupLease()
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	cleanupChart, err := prepareManagedHelmChart(t.Context(), params, &command)
	if err != nil {
		t.Fatalf("authorized chart with business overrides rejected: %v", err)
	}
	defer cleanupChart()
	if command.args[2] == params.Chart {
		t.Fatal("authorized chart was not staged")
	}
	for index, arg := range command.args {
		if arg == "--values" {
			body, err := os.ReadFile(command.args[index+1])
			if err != nil || string(body) != "replicaCount: 1\n" || valueRequests != 1 {
				t.Fatalf("values not staged once: %q requests=%d err=%v", body, valueRequests, err)
			}
		}
	}
	params.Release = "other"
	params.Namespace = "other-apps"
	params.Chart = server.URL + "/releases/assets/app-v2.tgz"
	if _, _, err := authorizedHelmCredential(t.Context(), params); err != nil {
		t.Fatalf("target-scoped credential must allow new deployment metadata: %v", err)
	}
	params.Release = "app"
	chart = managedHelmTestArchive(t, append(managedHelmTestEntries(), managedHelmTestEntry{
		header: tar.Header{Name: "zhiyong/README.md"}, body: []byte("changed chart"),
	}))
	command, err = buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	cleanupNewChart, err := prepareManagedHelmChart(t.Context(), params, &command)
	if err != nil {
		t.Fatalf("new Chart version was blocked by an old credential binding: %v", err)
	}
	defer cleanupNewChart()
	staged, err := os.ReadFile(command.args[2])
	if err != nil || string(staged) != string(chart) {
		t.Fatalf("new artifact bytes must be staged for dry-run and apply: %v", err)
	}
}
