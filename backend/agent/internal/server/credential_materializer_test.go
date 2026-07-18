package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaterializeRegistryCredentialUsesStdinAndVerifiesManifest(t *testing.T) {
	allowCredentialPrivateEndpointsForTest(t)
	bin, argsLog := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_TEST_GIT_SECRET", "")

	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`
	registryDigest := credentialTestManifestDigest(manifest)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/team/app/manifests/latest" {
			http.NotFound(w, r)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "deploy" || password != "materializer-canary-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", registryDigest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(manifest))
	}))
	defer registry.Close()

	result, err := materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:       "cred_registry",
		GrantID:            "grant_registry",
		VersionID:          "ver_registry",
		CredentialType:     CredentialTypeRegistry,
		Use:                CredentialUseImagePull,
		Namespace:          "kz-ops",
		Workload:           CredentialPlanWorkload{Kind: "Deployment", Name: "doops-agent-live"},
		RegistryRepository: "team/app",
		RegistryReference:  "latest",
		Payload: json.RawMessage(fmt.Sprintf(
			`{"server":%q,"username":"deploy","password":"materializer-canary-secret"}`,
			registry.URL,
		)),
	})
	if err != nil {
		t.Fatalf("materialize registry credential: %v", err)
	}
	if result.Status != "verified" ||
		result.SecretType != "kubernetes.io/dockerconfigjson" ||
		len(result.Keys) != 1 || result.Keys[0] != ".dockerconfigjson" ||
		result.ResourceVersion != "42" ||
		result.Digest != registryDigest {
		t.Fatalf("unexpected registry materialization: %#v", result)
	}
	assertCredentialCanaryNotInText(t, "materializer result", string(mustJSON(t, result)), "materializer-canary-secret")
	logged := readCredentialTestFile(t, argsLog)
	assertCredentialCanaryNotInText(t, "command arguments", logged, "materializer-canary-secret")
	if strings.Contains(logged, "[--patch-file] [-]") ||
		!strings.Contains(logged, "[--type=merge] [--patch]") {
		t.Fatalf("workload patch does not use a kubectl-supported inline merge patch: %s", logged)
	}
	if !strings.Contains(logged, "[get] [secret]") ||
		!strings.Contains(logged, "[-o] [json]") {
		t.Fatalf("Secret inspection does not use structured JSON output: %s", logged)
	}
}

func TestVerifyRegistryManifestUsesBearerChallenge(t *testing.T) {
	allowCredentialPrivateEndpointsForTest(t)
	const canary = "bearer-canary-secret"
	var tokenRequests atomic.Int64
	var manifestRequests atomic.Int64
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json"}`
	registryDigest := credentialTestManifestDigest(manifest)
	var registry *httptest.Server
	registry = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests.Add(1)
			username, password, ok := r.BasicAuth()
			if !ok || username != "deploy" || password != canary ||
				r.URL.Query().Get("service") != "registry.test" ||
				r.URL.Query().Get("scope") != "repository:team/app:pull" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"token":"short-lived-registry-token"}`))
		case "/v2/team/app/manifests/latest":
			manifestRequests.Add(1)
			accept := r.Header.Get("Accept")
			if !strings.Contains(accept, "application/vnd.oci.image.index.v1+json") ||
				!strings.Contains(accept, "application/vnd.docker.distribution.manifest.list.v2+json") {
				http.Error(w, "multi-platform media types required", http.StatusNotAcceptable)
				return
			}
			if r.Header.Get("Authorization") != "Bearer short-lived-registry-token" {
				w.Header().Set("WWW-Authenticate",
					fmt.Sprintf(`Bearer realm=%q,service="registry.test",scope="repository:team/app:pull"`, registry.URL+"/token"))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", registryDigest)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(manifest))
		default:
			http.NotFound(w, r)
		}
	}))
	defer registry.Close()

	digest, err := verifyRegistryManifest(
		context.Background(), registry.URL, "deploy", canary, "team/app", "latest",
	)
	if err != nil {
		t.Fatalf("verify registry manifest with bearer challenge: %v", err)
	}
	if digest != registryDigest || tokenRequests.Load() != 1 || manifestRequests.Load() != 2 {
		t.Fatalf("digest=%q tokenRequests=%d manifestRequests=%d",
			digest, tokenRequests.Load(), manifestRequests.Load())
	}
}

func TestVerifyRegistryManifestRejectsDigestMismatch(t *testing.T) {
	allowCredentialPrivateEndpointsForTest(t)
	manifest := `{"schemaVersion":2}`
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("a", 64))
		_, _ = w.Write([]byte(manifest))
	}))
	defer registry.Close()

	if _, err := verifyRegistryManifest(
		context.Background(), registry.URL, "deploy", "synthetic", "team/app", "latest",
	); err == nil || err.Error() != "registry_digest_mismatch" {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestVerifyRegistryManifestRejectsReferenceDigestMismatch(t *testing.T) {
	allowCredentialPrivateEndpointsForTest(t)
	manifest := `{"schemaVersion":2}`
	digest := credentialTestManifestDigest(manifest)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write([]byte(manifest))
	}))
	defer registry.Close()

	reference := "sha256:" + strings.Repeat("b", 64)
	if _, err := verifyRegistryManifest(
		context.Background(), registry.URL, "deploy", "synthetic", "team/app", reference,
	); err == nil || err.Error() != "registry_reference_digest_mismatch" {
		t.Fatalf("reference digest mismatch error = %v", err)
	}
}

func TestVerifyRegistryManifestRejectsUnavailableManifestRepresentations(t *testing.T) {
	allowCredentialPrivateEndpointsForTest(t)
	for name, response := range map[string]struct {
		status      int
		contentType string
		body        string
	}{
		"no content": {
			status: http.StatusNoContent,
		},
		"wrong media type": {
			status:      http.StatusOK,
			contentType: "text/plain",
			body:        `{"schemaVersion":2}`,
		},
		"invalid json": {
			status:      http.StatusOK,
			contentType: "application/vnd.oci.image.manifest.v1+json",
			body:        `not-json`,
		},
		"wrong schema": {
			status:      http.StatusOK,
			contentType: "application/vnd.oci.image.manifest.v1+json",
			body:        `{"schemaVersion":1}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if response.contentType != "" {
					w.Header().Set("Content-Type", response.contentType)
				}
				if response.body != "" {
					w.Header().Set("Docker-Content-Digest", credentialTestManifestDigest(response.body))
				}
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			}))
			defer registry.Close()

			if _, err := verifyRegistryManifest(
				context.Background(), registry.URL, "deploy", "synthetic", "team/app", "latest",
			); err == nil {
				t.Fatal("unavailable manifest representation was accepted")
			}
		})
	}
}

func credentialTestManifestDigest(manifest string) string {
	sum := sha256.Sum256([]byte(manifest))
	return fmt.Sprintf("sha256:%x", sum)
}

func TestCredentialSecretNameIsGrantScoped(t *testing.T) {
	first := credentialSecretName("cred_registry", "grant_a")
	second := credentialSecretName("cred_registry", "grant_b")
	if first == second {
		t.Fatalf("different grants share credential Secret name %q", first)
	}
}

func TestMaterializeTLSCredentialValidatesKeyPair(t *testing.T) {
	bin, argsLog := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	certificate, privateKey := credentialTestCertificate(t, "pkcs8")

	result, err := materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:   "cred_tls",
		GrantID:        "grant_tls",
		VersionID:      "ver_tls",
		CredentialType: CredentialTypeTLS,
		Use:            CredentialUseTLS,
		Namespace:      "kz-ops",
		Payload: mustJSON(t, map[string]string{
			"certificate": certificate,
			"privateKey":  privateKey,
		}),
	})
	if err != nil {
		t.Fatalf("materialize TLS credential: %v", err)
	}
	if result.Status != "verified" ||
		result.SecretType != "kubernetes.io/tls" ||
		result.Fingerprint == "" ||
		result.ExpiresAt == "" {
		t.Fatalf("unexpected TLS materialization: %#v", result)
	}
	assertCredentialCanaryNotInText(t, "command arguments", readCredentialTestFile(t, argsLog), privateKey)

	for _, format := range []string{"pkcs1", "sec1"} {
		format := format
		t.Run(format, func(t *testing.T) {
			certificate, privateKey := credentialTestCertificate(t, format)
			if _, _, err := validateTLSCredential(certificate, privateKey); err != nil {
				t.Fatalf("validate %s TLS private key: %v", format, err)
			}
		})
	}

	_, wrongKey := credentialTestCertificate(t, "pkcs8")
	_, err = materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:   "cred_tls_bad",
		GrantID:        "grant_tls_bad",
		VersionID:      "ver_tls_bad",
		CredentialType: CredentialTypeTLS,
		Use:            CredentialUseTLS,
		Namespace:      "kz-ops",
		Payload: mustJSON(t, map[string]string{
			"certificate": certificate,
			"privateKey":  wrongKey,
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "tls_key_pair_invalid") {
		t.Fatalf("mismatched TLS key error = %v", err)
	}
	assertCredentialCanaryNotInText(t, "TLS error", err.Error(), wrongKey)
}

func TestMaterializeOpaqueCredentialRequiresExactKeySet(t *testing.T) {
	bin, _ := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:   "cred_opaque",
		GrantID:        "grant_opaque",
		VersionID:      "ver_opaque",
		CredentialType: CredentialTypeOpaque,
		Use:            CredentialUseOpaqueSecret,
		Namespace:      "app",
		RequiredKeys:   []string{"API_KEY", "ENDPOINT"},
		Payload:        json.RawMessage(`{"data":{"API_KEY":"opaque-canary-secret","ENDPOINT":"https://service.example.com"}}`),
	})
	if err != nil {
		t.Fatalf("materialize opaque credential: %v", err)
	}
	if result.Status != "verified" || strings.Join(result.Keys, ",") != "API_KEY,ENDPOINT" {
		t.Fatalf("unexpected opaque materialization: %#v", result)
	}

	_, err = materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:   "cred_opaque_bad",
		GrantID:        "grant_opaque_bad",
		VersionID:      "ver_opaque_bad",
		CredentialType: CredentialTypeOpaque,
		Use:            CredentialUseOpaqueSecret,
		Namespace:      "app",
		RequiredKeys:   []string{"API_KEY", "ENDPOINT"},
		Payload:        json.RawMessage(`{"data":{"API_KEY":"opaque-canary-secret","EXTRA":"not-allowed"}}`),
	})
	if err == nil || !strings.Contains(err.Error(), "credential_key_mismatch") {
		t.Fatalf("opaque key mismatch error = %v", err)
	}
	assertCredentialCanaryNotInText(t, "opaque error", err.Error(), "opaque-canary-secret")
}

func TestMaterializeCredentialRejectsTamperedSecretContentAndMetadata(t *testing.T) {
	for _, mutation := range []string{"data", "grant"} {
		t.Run(mutation, func(t *testing.T) {
			bin, _ := installCredentialTestCommands(t)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("DOOPS_TEST_SECRET_MUTATION", mutation)

			_, err := materializeCredential(context.Background(), CredentialMaterializeRequest{
				CredentialID:   "cred_opaque",
				GrantID:        "grant_opaque",
				VersionID:      "ver_opaque",
				CredentialType: CredentialTypeOpaque,
				Use:            CredentialUseOpaqueSecret,
				Namespace:      "app",
				RequiredKeys:   []string{"API_KEY"},
				Payload:        json.RawMessage(`{"data":{"API_KEY":"opaque-canary-secret"}}`),
			})
			if err == nil || err.Error() != "credential_secret_verification_failed" {
				t.Fatalf("tampered Secret error = %v", err)
			}
			assertCredentialCanaryNotInText(t, "tampered Secret error", err.Error(), "opaque-canary-secret")
		})
	}
}

func TestMaterializeHelmAndGitCredentialsVerifyRemoteWithoutLeaking(t *testing.T) {
	allowCredentialPrivateEndpointsForTest(t)
	bin, argsLog := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_TEST_GIT_SECRET", "git-canary-secret")

	helmRepo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "helm" || password != "helm-canary-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("apiVersion: v1\nentries: {}\n"))
	}))
	defer helmRepo.Close()

	helmResult, err := materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:   "cred_helm",
		GrantID:        "grant_helm",
		VersionID:      "ver_helm",
		CredentialType: CredentialTypeHelmRepository,
		Use:            CredentialUseHelmPull,
		Namespace:      "kz-ops",
		Payload: mustJSON(t, map[string]string{
			"url":      helmRepo.URL,
			"username": "helm",
			"password": "helm-canary-secret",
		}),
	})
	if err != nil {
		t.Fatalf("materialize Helm credential: %v", err)
	}
	if helmResult.Status != "verified" || helmResult.Digest == "" {
		t.Fatalf("unexpected Helm materialization: %#v", helmResult)
	}

	gitResult, err := materializeCredential(context.Background(), CredentialMaterializeRequest{
		CredentialID:   "cred_git",
		GrantID:        "grant_git",
		VersionID:      "ver_git",
		CredentialType: CredentialTypeGitToken,
		Use:            CredentialUseGitCheckout,
		Namespace:      "kz-ops",
		Payload:        json.RawMessage(`{"url":"https://127.0.0.1/repo.git","username":"git","token":"git-canary-secret"}`),
	})
	if err != nil {
		t.Fatalf("materialize Git credential: %v", err)
	}
	if gitResult.Status != "verified" || gitResult.Digest == "" {
		t.Fatalf("unexpected Git materialization: %#v", gitResult)
	}
	serialized := string(mustJSON(t, []CredentialMaterialization{helmResult, gitResult}))
	assertCredentialCanaryNotInText(t, "Helm/Git results", serialized, "helm-canary-secret")
	assertCredentialCanaryNotInText(t, "Helm/Git results", serialized, "git-canary-secret")
	args := readCredentialTestFile(t, argsLog)
	assertCredentialCanaryNotInText(t, "Helm/Git command arguments", args, "helm-canary-secret")
	assertCredentialCanaryNotInText(t, "Helm/Git command arguments", args, "git-canary-secret")
}

func TestRestoreCredentialTargetUsesSupportedPatchAndConfirmsState(t *testing.T) {
	bin, argsLog := installCredentialTestCommands(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := restoreCredentialTarget(context.Background(), "apps", "doops-cred-restore", credentialTargetSnapshot{
		WorkloadKind:   "deployment",
		WorkloadName:   "app",
		ImagePullNames: []string{"existing-pull"},
	}); err != nil {
		t.Fatalf("restore credential target: %v", err)
	}
	logged := readCredentialTestFile(t, argsLog)
	if strings.Contains(logged, "[--patch-file=-]") ||
		!strings.Contains(logged, "[--type=merge] [--patch]") {
		t.Fatalf("restore did not use a supported inline merge patch: %s", logged)
	}
}

func TestCleanupCredentialDeletesOnlyMatchingDoOpsOwnedSecret(t *testing.T) {
	bin := t.TempDir()
	logPath := filepath.Join(bin, "kubectl.log")
	secretPath := filepath.Join(bin, "secret.json")
	workloadPath := filepath.Join(bin, "workload.json")
	kubectl := `#!/bin/sh
printf '%s\n' "$*" >> "$DOOPS_TEST_ARGS_LOG"
if [ "$1" = "get" ] && [ "$2" = "secret" ]; then
  if [ -f "$DOOPS_TEST_SECRET_STATE" ]; then
    cat "$DOOPS_TEST_SECRET_STATE"
  fi
elif [ "$1" = "get" ]; then
  cat "$DOOPS_TEST_WORKLOAD_STATE"
elif [ "$1" = "patch" ]; then
  for arg in "$@"; do
    if [ "$arg" = "--patch-file=-" ]; then
      exit 64
    fi
  done
  patch=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--patch" ]; then
      shift
      patch=$1
      break
    fi
    shift
  done
  if [ -z "$patch" ]; then
    exit 64
  fi
  case "$patch" in
    *"$DOOPS_TEST_CREDENTIAL_SECRET"*)
      action=restore
      ;;
    *)
      action=remove
      ;;
  esac
  if [ "$action" = "remove" ] && [ "$DOOPS_TEST_PATCH_MODE" = "fail_before_remove" ]; then
    exit 1
  fi
  if [ "$action" = "restore" ] && [ "$DOOPS_TEST_PATCH_MODE" = "fail_restore" ]; then
    exit 1
  fi
  if [ "$action" = "restore" ]; then
    printf '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"existing-pull"},{"name":"%s"}]}}}}\n' \
      "$DOOPS_TEST_CREDENTIAL_SECRET" > "$DOOPS_TEST_WORKLOAD_STATE"
  else
    printf '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"existing-pull"}]}}}}\n' \
      > "$DOOPS_TEST_WORKLOAD_STATE"
  fi
  if [ "$action" = "remove" ] && [ "$DOOPS_TEST_PATCH_MODE" = "fail_after_remove" ]; then
    exit 1
  fi
  if [ "$action" = "restore" ] && [ "$DOOPS_TEST_PATCH_MODE" = "fail_after_restore" ]; then
    exit 1
  fi
elif [ "$1" = "delete" ]; then
  delete_options=$(cat)
  case "$delete_options" in
    *'"uid":"uid-cleanup"'*) ;;
    *) exit 64 ;;
  esac
  case "$delete_options" in
    *'"resourceVersion":"42"'*) ;;
    *)
      exit 64
      ;;
  esac
  if [ "$DOOPS_TEST_DELETE_MODE" = "fail_before" ]; then
    exit 1
  fi
  if [ "$DOOPS_TEST_DELETE_MODE" = "replace" ]; then
    sed 's/"uid":"uid-cleanup"/"uid":"uid-replacement"/' "$DOOPS_TEST_SECRET_STATE" \
      > "$DOOPS_TEST_SECRET_STATE.tmp"
    mv "$DOOPS_TEST_SECRET_STATE.tmp" "$DOOPS_TEST_SECRET_STATE"
    exit 1
  fi
  rm -f "$DOOPS_TEST_SECRET_STATE"
  if [ "$DOOPS_TEST_DELETE_MODE" = "fail_after" ]; then
    exit 1
  fi
fi
`
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(kubectl), 0755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_TEST_ARGS_LOG", logPath)
	t.Setenv("DOOPS_TEST_SECRET_STATE", secretPath)
	t.Setenv("DOOPS_TEST_WORKLOAD_STATE", workloadPath)
	credentialID := "cred_cleanup"
	grantID := "grant_cleanup"
	versionID := "credver_cleanup"
	secretName := credentialSecretName(credentialID, grantID)
	t.Setenv("DOOPS_TEST_CREDENTIAL_SECRET", secretName)
	t.Setenv("DOOPS_TEST_PATCH_MODE", "")
	t.Setenv("DOOPS_TEST_DELETE_MODE", "")
	writeSecret := func(version string) {
		t.Helper()
		if err := os.WriteFile(secretPath, []byte(fmt.Sprintf(
			`{"metadata":{"uid":"uid-cleanup","resourceVersion":"42","labels":{"app.kubernetes.io/managed-by":"doops","doops.sh/credential-id":%q,"doops.sh/credential-grant-id":%q},"annotations":{"doops.sh/credential-version":%q}}}`,
			credentialID, grantID, version,
		)), 0600); err != nil {
			t.Fatalf("write Secret state: %v", err)
		}
	}
	writeWorkload := func(includeCredential bool) {
		t.Helper()
		names := `[{"name":"existing-pull"}]`
		if includeCredential {
			names = fmt.Sprintf(`[{"name":"existing-pull"},{"name":%q}]`, secretName)
		}
		if err := os.WriteFile(workloadPath, []byte(fmt.Sprintf(
			`{"spec":{"template":{"spec":{"imagePullSecrets":%s}}}}`, names,
		)), 0600); err != nil {
			t.Fatalf("write workload state: %v", err)
		}
	}

	writeSecret(versionID)
	writeWorkload(true)
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err != nil {
		t.Fatalf("cleanup credential: %v", err)
	}
	logged := readCredentialTestFile(t, logPath)
	if !strings.Contains(logged, "delete --raw /api/v1/namespaces/apps/secrets/"+secretName+" -f -") {
		t.Fatalf("cleanup did not delete the owned Secret: %s", logged)
	}
	if _, err := os.Stat(secretPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Secret state still exists after cleanup: %v", err)
	}
	if workload := readCredentialTestFile(t, workloadPath); strings.Contains(workload, secretName) {
		t.Fatalf("workload still references cleaned Secret: %s", workload)
	}

	if err := os.WriteFile(logPath, nil, 0600); err != nil {
		t.Fatalf("reset command log: %v", err)
	}
	writeWorkload(true)
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err != nil {
		t.Fatalf("cleanup missing Secret workload reference: %v", err)
	}
	logged = readCredentialTestFile(t, logPath)
	if !strings.Contains(logged, "patch deployment app") {
		t.Fatalf("cleanup skipped workload reference after Secret disappeared: %s", logged)
	}

	writeSecret("other")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
	}); err == nil || !strings.Contains(err.Error(), "ownership_mismatch") {
		t.Fatalf("mismatched ownership cleanup error = %v", err)
	}

	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		AnyVersion:   true,
		Namespace:    "apps",
	}); err != nil {
		t.Fatalf("revoke cleanup must accept the current DoOps-owned version: %v", err)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_PATCH_MODE", "fail_before_remove")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err == nil || err.Error() != "credential_target_mutation_failed" {
		t.Fatalf("patch failure before mutation error = %v", err)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_PATCH_MODE", "fail_after_remove")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err != nil {
		t.Fatalf("cleanup must accept a patch error after the desired state is observed: %v", err)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_PATCH_MODE", "")
	t.Setenv("DOOPS_TEST_DELETE_MODE", "fail_after")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err != nil {
		t.Fatalf("cleanup must accept a delete error after absence is observed: %v", err)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_DELETE_MODE", "replace")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err == nil || err.Error() != "credential_target_outcome_unknown" {
		t.Fatalf("same-name Secret replacement error = %v", err)
	}
	if workload := readCredentialTestFile(t, workloadPath); strings.Contains(workload, secretName) {
		t.Fatalf("cleanup rebound workload to a replacement Secret: %s", workload)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_DELETE_MODE", "fail_before")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err == nil || err.Error() != "credential_secret_delete_failed" {
		t.Fatalf("delete failure with successful compensation error = %v", err)
	}
	if workload := readCredentialTestFile(t, workloadPath); !strings.Contains(workload, secretName) {
		t.Fatalf("cleanup did not restore workload after confirmed delete failure: %s", workload)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_PATCH_MODE", "fail_after_restore")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err == nil || err.Error() != "credential_secret_delete_failed" {
		t.Fatalf("restore error after observed compensation error = %v", err)
	}

	writeSecret(versionID)
	writeWorkload(true)
	t.Setenv("DOOPS_TEST_PATCH_MODE", "fail_restore")
	if err := cleanupCredential(context.Background(), CredentialCleanupRequest{
		SessionID:    "cleanup-session",
		CredentialID: credentialID,
		GrantID:      grantID,
		VersionID:    versionID,
		Namespace:    "apps",
		Workload:     CredentialPlanWorkload{Kind: "Deployment", Name: "app"},
	}); err == nil || err.Error() != "credential_target_outcome_unknown" {
		t.Fatalf("failed cleanup compensation error = %v", err)
	}
}

func installCredentialTestCommands(t *testing.T) (string, string) {
	t.Helper()
	bin := t.TempDir()
	argsLog := filepath.Join(bin, "args.log")
	secretState := filepath.Join(bin, "secret.json")
	kubectl := `#!/bin/sh
printf 'kubectl' >> "$DOOPS_TEST_ARGS_LOG"
for arg in "$@"; do printf ' [%s]' "$arg" >> "$DOOPS_TEST_ARGS_LOG"; done
printf '\n' >> "$DOOPS_TEST_ARGS_LOG"
case "$1" in
  apply)
    sed 's/"metadata":{/"metadata":{"resourceVersion":"42",/' > "$DOOPS_TEST_SECRET_STATE"
    if [ "$DOOPS_TEST_SECRET_MUTATION" = "data" ]; then
      sed 's/"API_KEY":"[^"]*"/"API_KEY":"e30="/' "$DOOPS_TEST_SECRET_STATE" > "$DOOPS_TEST_SECRET_STATE.tmp"
      mv "$DOOPS_TEST_SECRET_STATE.tmp" "$DOOPS_TEST_SECRET_STATE"
    elif [ "$DOOPS_TEST_SECRET_MUTATION" = "grant" ]; then
      sed 's/"doops.sh\/credential-grant-id":"[^"]*"/"doops.sh\/credential-grant-id":"other"/' \
        "$DOOPS_TEST_SECRET_STATE" > "$DOOPS_TEST_SECRET_STATE.tmp"
      mv "$DOOPS_TEST_SECRET_STATE.tmp" "$DOOPS_TEST_SECRET_STATE"
    fi
    ;;
  patch)
    patch=
    while [ "$#" -gt 0 ]; do
      arg=$1
      if [ "$arg" = "--patch-file=-" ]; then
        exit 64
      fi
      if [ "$arg" = "--patch" ]; then
        shift
        patch=$1
        break
      fi
      shift
    done
    if [ -z "$patch" ]; then
      exit 64
    fi
    ;;
  replace)
    cat > "$DOOPS_TEST_SECRET_STATE"
    ;;
  delete)
    rm -f "$DOOPS_TEST_SECRET_STATE"
    ;;
  get)
    if [ "$2" = "secret" ]; then
      for arg in "$@"; do
        if [ "$arg" = "--ignore-not-found" ]; then
          exit 0
        fi
      done
      cat "$DOOPS_TEST_SECRET_STATE"
    else
      printf '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"existing-pull"}]}}}}\n'
    fi
    ;;
esac
`
	git := `#!/bin/sh
printf 'git' >> "$DOOPS_TEST_ARGS_LOG"
for arg in "$@"; do printf ' [%s]' "$arg" >> "$DOOPS_TEST_ARGS_LOG"; done
printf '\n' >> "$DOOPS_TEST_ARGS_LOG"
test -n "$GIT_ASKPASS"
test -x "$GIT_ASKPASS"
test "$DOOPS_GIT_SECRET" = "$DOOPS_TEST_GIT_SECRET"
printf '0123456789abcdef\trefs/heads/main\n'
`
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(kubectl), 0755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(git), 0755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("DOOPS_TEST_ARGS_LOG", argsLog)
	t.Setenv("DOOPS_TEST_SECRET_STATE", secretState)
	return bin, argsLog
}

func credentialTestCertificate(t *testing.T, format string) (string, string) {
	t.Helper()
	var privateKey crypto.Signer
	switch format {
	case "pkcs1":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate RSA private key: %v", err)
		}
		privateKey = key
	case "pkcs8", "sec1":
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate EC private key: %v", err)
		}
		privateKey = key
	default:
		t.Fatalf("unsupported test private key format %q", format)
	}
	now := time.Now().UTC()
	template := x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: "credential.test"},
		DNSNames:     []string{"credential.test"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, privateKey.Public(), privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	var keyBlock *pem.Block
	switch format {
	case "pkcs1":
		keyBlock = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey.(*rsa.PrivateKey))}
	case "sec1":
		keyDER, err := x509.MarshalECPrivateKey(privateKey.(*ecdsa.PrivateKey))
		if err != nil {
			t.Fatalf("marshal EC private key: %v", err)
		}
		keyBlock = &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}
	case "pkcs8":
		keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			t.Fatalf("marshal PKCS#8 private key: %v", err)
		}
		keyBlock = &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key := pem.EncodeToMemory(keyBlock)
	return string(certificate), string(key)
}

func readCredentialTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertCredentialCanaryNotInText(t *testing.T, subject, text, canary string) {
	t.Helper()
	if canary != "" && strings.Contains(text, canary) {
		t.Fatalf("%s exposed credential canary", subject)
	}
}

func allowCredentialPrivateEndpointsForTest(t *testing.T) {
	t.Helper()
	previous := credentialPrivateEndpointAllowed
	credentialPrivateEndpointAllowed = func(string) bool { return true }
	t.Cleanup(func() {
		credentialPrivateEndpointAllowed = previous
	})
}
