package server

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(command.args, " "), "--reset-values") {
		t.Fatalf("DoOps must not replace Helm's values merge behavior: %#v", command.args)
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
	public := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unrelated private repository credential sent to public chart origin")
		}
		_, _ = w.Write(chart)
	}))
	params.Chart = public.URL + "/loki.tgz"
	params.ValueFiles = nil
	auth, digest, err := authorizedHelmCredential(t.Context(), params)
	if err != nil || auth != nil || digest != "" {
		t.Fatalf("unrelated origin must not receive credential payload: auth present=%t digest=%q err=%v", auth != nil, digest, err)
	}
	publicCommand, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublic, err := prepareManagedHelmChart(t.Context(), params, &publicCommand)
	if err != nil {
		t.Fatalf("public chart blocked by bound private credential: %v", err)
	}
	defer cleanupPublic()
	if publicCommand.args[2] == params.Chart {
		t.Fatal("public chart was not staged")
	}
	params.Chart = server.URL + "/releases/assets/app-v2.tgz"
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

func bindHelmCandidateRepositories(t *testing.T, origins ...string) []api.AgentCredentialBinding {
	t.Helper()
	var bindings []api.AgentCredentialBinding
	for index, origin := range origins {
		binding := testManagedHelmBinding()
		binding.Name = fmt.Sprintf("helm-candidate-%d", index)
		binding.CredentialID = fmt.Sprintf("cred_%024x", index+1)
		binding.GrantID = fmt.Sprintf("credgrant_%024x", index+1)
		binding.VersionID = fmt.Sprintf("credver_%024x", index+1)
		binding.Digest = fmt.Sprintf("sha256:%064x", index+1)
		materialized, err := materializeNativeHelmCredential(t.Context(), CredentialMaterializeRequest{
			SessionID: "release", CredentialID: binding.CredentialID, GrantID: binding.GrantID,
			VersionID: binding.VersionID, Scope: CredentialGrantScope{Use: CredentialUseHelmPull, Namespace: "apps"},
		}, nativeHelmAuth{
			URL: origin, Username: fmt.Sprintf("synthetic-helm-user-%d", index),
			Password: fmt.Sprintf("synthetic-helm-password-%d", index),
		}, binding.Digest)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = cleanupNativeHelmCredential(CredentialCleanupRequest{
				Namespace: "apps", CredentialID: binding.CredentialID, GrantID: binding.GrantID, AnyVersion: true,
			})
		})
		binding.ContextSocket, binding.ResourceVersion = materialized.ContextSocket, materialized.ResourceVersion
		bindings = append(bindings, binding)
	}
	cleanup, err := bindHelmCredentialsForPrompt("release", bindings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return bindings
}

func TestManagedHelmMultipleCredentialsDownloadByOrigin(t *testing.T) {
	for _, tc := range []struct {
		name              string
		matches           [2]bool
		status            int
		wantCredential    int
		wantRequests      int64
		wantRedirects     int64
		wantError         string
		workspace         bool
		brokerUnavailable bool
	}{
		{name: "public with two private credentials", status: 200, wantCredential: -1, wantRequests: 1},
		{name: "first origin matches", matches: [2]bool{true, false}, status: 200, wantRequests: 1},
		{name: "second origin matches", matches: [2]bool{false, true}, status: 200, wantCredential: 1, wantRequests: 1},
		{name: "same origin is ambiguous before download", matches: [2]bool{true, true}, status: 200,
			wantError: "helm_credential_binding_ambiguous: multiple credentials match the chart HTTPS origin (candidates: helm-candidate-0 [cred_000000000000000000000001], helm-candidate-1 [cred_000000000000000000000002]); select one with --credential <name-or-id>"},
		{name: "workspace ignores repository ambiguity", matches: [2]bool{true, true}, status: 200, workspace: true},
		{name: "workspace never requests repository credentials", status: 200, workspace: true, brokerUnavailable: true},
		{name: "authenticated 401 never retries anonymously", matches: [2]bool{false, true}, status: 401,
			wantCredential: 1, wantRequests: 1, wantError: "helm_artifact_http_401"},
		{name: "authenticated 403 never retries anonymously", matches: [2]bool{true, false}, status: 403,
			wantRequests: 1, wantError: "helm_artifact_http_403"},
		{name: "authenticated redirect never reaches another origin", matches: [2]bool{true, false}, status: 302,
			wantRequests: 1, wantError: "helm_artifact_http_302"},
		{name: "public redirect never acquires destination credentials", status: 302,
			wantCredential: -1, wantRequests: 1, wantRedirects: 1},
		{name: "broker failure never becomes anonymous", status: 200, brokerUnavailable: true,
			wantError: "helm_native_context_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "workspaces")
			t.Setenv("DOOPS_WORKSPACE_ROOT", root)
			writeWorkspaceHelmChart(t, root)
			chart := managedHelmTestArchive(t, managedHelmTestEntries())
			var requests, redirects atomic.Int64
			other := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirects.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("private authentication crossed an origin boundary")
				}
				_, _ = w.Write(chart)
			}))
			server := managedHelmTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				user, password, ok := r.BasicAuth()
				if tc.wantCredential < 0 {
					if r.Header.Get("Authorization") != "" {
						t.Error("public download received private authentication")
					}
				} else if !ok || user != fmt.Sprintf("synthetic-helm-user-%d", tc.wantCredential) ||
					password != fmt.Sprintf("synthetic-helm-password-%d", tc.wantCredential) {
					t.Error("download did not use the unique matching credential")
				}
				if tc.status == http.StatusFound {
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie"})
					http.Redirect(w, r, other.URL+"/app.tgz", tc.status)
					return
				}
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_, _ = w.Write(chart)
				} else {
					fmt.Fprint(w, "synthetic-helm-password-0 synthetic-helm-password-1")
				}
			}))
			origins := []string{other.URL + "/repository-a", other.URL + "/repository-b"}
			for index, matches := range tc.matches {
				if matches {
					origins[index] = server.URL + "/repository"
				}
			}
			bindings := bindHelmCandidateRepositories(t, origins...)
			if tc.brokerUnavailable {
				_, err := cleanupNativeHelmCredential(CredentialCleanupRequest{
					Namespace: "apps", CredentialID: bindings[0].CredentialID, GrantID: bindings[0].GrantID, AnyVersion: true,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			params := testHelmParams()
			params.Chart, params.ValueFiles = server.URL+"/download/app.tgz", nil
			if tc.workspace {
				params = testWorkspaceHelmParams()
			}
			command, err := buildHelmCommand(params)
			if err != nil {
				t.Fatal(err)
			}
			cleanup, err := prepareManagedHelmChart(t.Context(), params, &command)
			if cleanup != nil {
				t.Cleanup(cleanup)
			}
			if tc.wantError != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantError) {
					t.Fatalf("download error=%v, want %s", err, tc.wantError)
				}
				if command.args[2] != params.Chart {
					t.Fatal("rejected chart must not be staged")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !tc.workspace {
					staged, err := os.ReadFile(command.args[2])
					if err != nil || !bytes.Equal(staged, chart) {
						t.Fatalf("staged chart differs from downloaded package: %v", err)
					}
				}
			}
			if requests.Load() != tc.wantRequests || redirects.Load() != tc.wantRedirects {
				t.Fatalf("requests=%d redirects=%d, want %d/%d", requests.Load(), redirects.Load(), tc.wantRequests, tc.wantRedirects)
			}
			lease, readErr := os.ReadFile(helmCredentialLeasePath("release"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			prompt, promptErr := credentialBindingContext(bindings)
			if promptErr != nil {
				t.Fatal(promptErr)
			}
			for label, text := range map[string]string{
				"metadata lease": string(lease), "prompt bindings": prompt,
				"Helm arguments": strings.Join(command.args, " "), "download error": fmt.Sprint(err),
			} {
				for index := range bindings {
					for _, canary := range []string{
						fmt.Sprintf("synthetic-helm-user-%d", index), fmt.Sprintf("synthetic-helm-password-%d", index),
					} {
						assertCredentialCanaryNotInText(t, label, text, canary)
					}
				}
			}
		})
	}
}

func TestHelmCredentialOriginNormalizesHTTPSHostAndPort(t *testing.T) {
	for _, tc := range []struct {
		name, chart string
		wantMatch   bool
	}{
		{"default HTTPS port", "https://charts.example.test/app.tgz", true},
		{"explicit HTTPS port", "https://charts.example.test:443/other/app.tgz", true},
		{"different port", "https://charts.example.test:8443/app.tgz", false},
		{"different hostname", "https://public.example.test/app.tgz", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "workspaces"))
			bindings := bindHelmCandidateRepositories(t, "https://other.example.test", "https://CHARTS.example.test:443/repository")
			auth, digest, err := authorizedHelmCredential(t.Context(), api.HelmParams{SessionID: "release", Chart: tc.chart})
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantMatch {
				if auth == nil || auth.Username != "synthetic-helm-user-1" || digest != bindings[1].Digest {
					t.Fatal("normalized HTTPS origin did not select the second credential")
				}
			} else if auth != nil || digest != "" {
				t.Fatal("unrelated origin received a credential")
			}
		})
	}
}

type helmCandidateSpec struct {
	name     string
	id       string
	origin   string
	username string
	password string
}

func bindNamedHelmCandidateRepositories(t *testing.T, specs ...helmCandidateSpec) []api.AgentCredentialBinding {
	t.Helper()
	bindings := make([]api.AgentCredentialBinding, 0, len(specs))
	for index, spec := range specs {
		binding := testManagedHelmBinding()
		binding.Name = spec.name
		binding.CredentialID = spec.id
		binding.GrantID = fmt.Sprintf("credgrant_%024x", index+101)
		binding.VersionID = fmt.Sprintf("credver_%024x", index+101)
		binding.Digest = fmt.Sprintf("sha256:%064x", index+101)
		materialized, err := materializeNativeHelmCredential(t.Context(), CredentialMaterializeRequest{
			SessionID: "release", CredentialID: binding.CredentialID, GrantID: binding.GrantID,
			VersionID: binding.VersionID, Scope: CredentialGrantScope{Use: CredentialUseHelmPull, Namespace: "apps"},
		}, nativeHelmAuth{
			URL: spec.origin, Username: spec.username, Password: spec.password,
		}, binding.Digest)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = cleanupNativeHelmCredential(CredentialCleanupRequest{
				Namespace: "apps", CredentialID: binding.CredentialID, GrantID: binding.GrantID, AnyVersion: true,
			})
		})
		binding.ContextSocket, binding.ResourceVersion = materialized.ContextSocket, materialized.ResourceVersion
		bindings = append(bindings, binding)
	}
	cleanup, err := bindHelmCredentialsForPrompt("release", bindings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return bindings
}

// Given/When/Then coverage for helmPull origin ambiguity diagnostics:
//   - a unique match succeeds while unrelated origins (204) are ignored;
//   - 2 or 3 same-origin matches fail closed with every matching candidate listed
//     deterministically (name ascending, then ID ascending; empty name -> ID only);
//   - unrelated origins never appear in the candidate list;
//   - no canary username, password, token, socket path, or URL leaks into errors;
//   - binding only a single explicitly selected credential succeeds.
func TestAuthorizedHelmCredentialAmbiguousDiagnosticsAndDisambiguation(t *testing.T) {
	const (
		targetOrigin    = "https://charts.example.test"
		unrelatedOrigin = "https://other.example.test"
		chartURL        = "https://charts.example.test/releases/app-1.2.3.tgz"
	)
	credID := func(n int) string { return fmt.Sprintf("cred_%024x", n) }

	t.Run("single match with unrelated origin succeeds", func(t *testing.T) {
		t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "workspaces"))
		bindings := bindNamedHelmCandidateRepositories(t,
			helmCandidateSpec{
				name: "unrelated-repo", id: credID(1), origin: unrelatedOrigin,
				username: "synthetic-helm-user-unrelated", password: "synthetic-helm-token-unrelated",
			},
			helmCandidateSpec{
				name: "primary-repo", id: credID(2), origin: targetOrigin,
				username: "synthetic-helm-user-primary", password: "synthetic-helm-password-primary",
			},
		)
		auth, digest, err := authorizedHelmCredential(t.Context(), api.HelmParams{SessionID: "release", Chart: chartURL})
		if err != nil {
			t.Fatalf("unexpected error for unique origin match: %v", err)
		}
		if auth == nil || auth.Username != "synthetic-helm-user-primary" || digest != bindings[1].Digest {
			t.Fatalf("expected unique matching credential, got authPresent=%t digest=%q", auth != nil, digest)
		}
	})

	for _, tc := range []struct {
		name              string
		specs             []helmCandidateSpec
		wantExactError    string
		wantExcludedTexts []string
	}{
		{
			name: "two matching credentials sorted deterministically by name then id",
			specs: []helmCandidateSpec{
				{
					name: "zeta-charts", id: credID(2), origin: targetOrigin + "/team-z",
					username: "synthetic-helm-user-zeta", password: "synthetic-helm-password-zeta",
				},
				{
					name: "alpha-charts", id: credID(1), origin: targetOrigin + "/team-a",
					username: "synthetic-helm-user-alpha", password: "synthetic-helm-password-alpha",
				},
			},
			wantExactError: fmt.Sprintf(
				"helm_credential_binding_ambiguous: multiple credentials match the chart HTTPS origin (candidates: alpha-charts [%s], zeta-charts [%s]); select one with --credential <name-or-id>",
				credID(1), credID(2),
			),
		},
		{
			name: "three matching credentials all collected while unrelated 204 origin is excluded",
			specs: []helmCandidateSpec{
				{
					name: "charlie-charts", id: credID(3), origin: targetOrigin + "/c",
					username: "synthetic-helm-user-charlie", password: "synthetic-helm-token-charlie",
				},
				{
					name: "unrelated-origin", id: credID(99), origin: unrelatedOrigin + "/other",
					username: "synthetic-helm-user-other", password: "synthetic-helm-password-other",
				},
				{
					name: "alpha-charts", id: credID(1), origin: targetOrigin + "/a",
					username: "synthetic-helm-user-alpha", password: "synthetic-helm-password-alpha",
				},
				{
					name: "bravo-charts", id: credID(2), origin: targetOrigin + "/b",
					username: "synthetic-helm-user-bravo", password: "synthetic-helm-password-bravo",
				},
			},
			wantExactError: fmt.Sprintf(
				"helm_credential_binding_ambiguous: multiple credentials match the chart HTTPS origin (candidates: alpha-charts [%s], bravo-charts [%s], charlie-charts [%s]); select one with --credential <name-or-id>",
				credID(1), credID(2), credID(3),
			),
			wantExcludedTexts: []string{"unrelated-origin", credID(99)},
		},
		{
			name: "same name ties break by credential id and empty name emits id only",
			specs: []helmCandidateSpec{
				{
					name: "shared-helm", id: credID(20), origin: targetOrigin + "/two",
					username: "synthetic-helm-user-two", password: "synthetic-helm-password-two",
				},
				{
					name: "", id: credID(30), origin: targetOrigin + "/unnamed",
					username: "synthetic-helm-user-unnamed", password: "synthetic-helm-token-unnamed",
				},
				{
					name: "shared-helm", id: credID(10), origin: targetOrigin + "/one",
					username: "synthetic-helm-user-one", password: "synthetic-helm-password-one",
				},
			},
			wantExactError: fmt.Sprintf(
				"helm_credential_binding_ambiguous: multiple credentials match the chart HTTPS origin (candidates: %s, shared-helm [%s], shared-helm [%s]); select one with --credential <name-or-id>",
				credID(30), credID(10), credID(20),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "workspaces"))
			bindings := bindNamedHelmCandidateRepositories(t, tc.specs...)
			auth, digest, err := authorizedHelmCredential(t.Context(), api.HelmParams{SessionID: "release", Chart: chartURL})
			if err == nil {
				t.Fatalf("expected ambiguity error, got nil (authPresent=%t digest=%q)", auth != nil, digest)
			}
			if auth != nil || digest != "" {
				t.Fatalf("ambiguous match must fail closed without returning auth or digest")
			}
			if err.Error() != tc.wantExactError {
				t.Fatalf("error = %q, want %q", err.Error(), tc.wantExactError)
			}
			// Verify MCP redaction keeps the non-sensitive candidate list intact.
			if redacted := redactHelmDiagnostic(err.Error()); redacted != err.Error() {
				t.Fatalf("redactHelmDiagnostic altered ambiguous candidates: got %q, want %q", redacted, err.Error())
			}
			for _, excluded := range tc.wantExcludedTexts {
				if strings.Contains(err.Error(), excluded) {
					t.Fatalf("unrelated credential %q leaked into error: %q", excluded, err.Error())
				}
			}
			for i, spec := range tc.specs {
				for _, canary := range []string{
					spec.username, spec.password, spec.origin, bindings[i].ContextSocket, "broker.sock", "doops-native-helm-", "https://",
				} {
					assertCredentialCanaryNotInText(t, "helm ambiguous error", err.Error(), canary)
				}
			}
		})
	}

	t.Run("explicitly binding a single selected credential succeeds", func(t *testing.T) {
		t.Setenv("DOOPS_WORKSPACE_ROOT", filepath.Join(t.TempDir(), "workspaces"))
		bindings := bindNamedHelmCandidateRepositories(t,
			helmCandidateSpec{
				name: "bravo-charts", id: credID(2), origin: targetOrigin + "/b",
				username: "synthetic-helm-user-bravo", password: "synthetic-helm-password-bravo",
			},
		)
		auth, digest, err := authorizedHelmCredential(t.Context(), api.HelmParams{SessionID: "release", Chart: chartURL})
		if err != nil {
			t.Fatalf("explicitly selected credential failed: %v", err)
		}
		if auth == nil || auth.Username != "synthetic-helm-user-bravo" || digest != bindings[0].Digest {
			t.Fatalf("expected explicitly selected credential, got authPresent=%t digest=%q", auth != nil, digest)
		}
	})
}
