package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// helmTestIngress is the shape of a second Harbor entry point: one host, two
	// backend Services, TLS from a Secret.
	helmTestIngress = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: harbor-oilan-ingress
  namespace: harbor-system
spec:
  ingressClassName: oilan-edge
  tls:
    - hosts:
        - repo.oilan.ai
      secretName: oilan-ai-apex-tls
  rules:
    - host: repo.oilan.ai
      http:
        paths:
          - path: /v2/
            pathType: Prefix
            backend:
              service:
                name: harbor-core
                port:
                  number: 80
          - path: /
            pathType: Prefix
            backend:
              service:
                name: harbor-portal
                port:
                  number: 80
`
	// helmTestIngressNoNamespace takes the release namespace.
	helmTestIngressNoNamespace = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: entry
spec:
  rules:
    - host: repo.example
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api
                port:
                  number: 8080
`
)

// An Ingress-only release has no pod to roll out, but it is still release
// content the verifier can check: the live object must route exactly like the
// rendered one and its backend Services must exist.
func TestValidateHelmRenderedContentAcceptsAnIngressOnlyRelease(t *testing.T) {
	content, err := validateHelmRenderedContent([]byte(helmTestIngress), "")
	if err != nil {
		t.Fatalf("an Ingress-only release must be publishable: %v", err)
	}
	if len(content.Workloads) != 0 {
		t.Fatalf("workloads = %d, want 0", len(content.Workloads))
	}
	if len(content.Ingresses) != 1 {
		t.Fatalf("ingresses = %d, want 1", len(content.Ingresses))
	}
	ingress := content.Ingresses[0]
	if ingress.Kind != "Ingress" || ingress.Name != "harbor-oilan-ingress" || ingress.Namespace != "harbor-system" {
		t.Fatalf("ingress = %+v, want Ingress harbor-system/harbor-oilan-ingress", ingress)
	}
	wantBackends := map[string]int32{"harbor-core": 80, "harbor-portal": 80}
	if len(ingress.Backends) != len(wantBackends) {
		t.Fatalf("backends = %v, want %v", ingress.Backends, wantBackends)
	}
	for name, port := range wantBackends {
		if ingress.Backends[name] != port {
			t.Fatalf("backend %s = %d, want %d", name, ingress.Backends[name], port)
		}
	}
	if !strings.Contains(ingress.Signature, "host=repo.oilan.ai path=/v2/") {
		t.Fatalf("signature does not carry the route: %q", ingress.Signature)
	}
	if !strings.Contains(ingress.Signature, "tls secret=oilan-ai-apex-tls") {
		t.Fatalf("signature does not carry the TLS material: %q", ingress.Signature)
	}
}

// A release that renders neither a workload nor an Ingress still has nothing
// the verifier can check, so it stays refused.
func TestValidateHelmRenderedContentStillRefusesAReleaseWithNothingToCheck(t *testing.T) {
	plainJob := "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: once\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/once:1\n"
	service := "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\n"
	manifest := []byte(plainJob + "\n---\n" + service)
	if _, err := validateHelmRenderedContent(manifest, ""); err == nil {
		t.Fatal("a release with nothing to verify must be refused")
	} else if !strings.Contains(err.Error(), "helm_no_workload") {
		t.Fatalf("error = %v, want helm_no_workload", err)
	}
}

// An Ingress without a route serves nothing, so it is malformed.
func TestParseRenderedHelmIngressesRejectsAnIngressWithoutRoutes(t *testing.T) {
	manifest := []byte("apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: empty\nspec:\n  ingressClassName: oilan-edge\n")
	if _, err := validateHelmRenderedContent(manifest, ""); err == nil {
		t.Fatal("an Ingress without routes must be refused")
	} else if !strings.Contains(err.Error(), "has no routes") {
		t.Fatalf("error = %v, want the no-routes reason", err)
	}
}

// The rendered and the live side must be compared through the same signature,
// so both spellings of the backend produce the same signature.
func TestHelmIngressSignatureIsCanonical(t *testing.T) {
	modern := helmIngressSpec{
		IngressClassName: "oilan-edge",
		TLS: []helmIngressTLS{
			{Hosts: []string{"repo.oilan.ai"}, SecretName: "oilan-ai-apex-tls"},
		},
		Rules: []helmIngressRule{
			{Host: "repo.oilan.ai", HTTP: &helmIngressHTTP{Paths: []helmIngressPath{
				{Path: "/v2/", PathType: "Prefix", Backend: helmIngressBackend{
					Service: &helmIngressService{Name: "harbor-core", Port: helmIngressServicePort{Number: 80}}}},
				{Path: "/", PathType: "Prefix", Backend: helmIngressBackend{
					Service: &helmIngressService{Name: "harbor-portal", Port: helmIngressServicePort{Number: 80}}}},
			}}},
		},
	}
	// The live side spells the same backend the pre-v1 way.
	legacy := modern
	legacy.Rules = []helmIngressRule{
		{Host: "repo.oilan.ai", HTTP: &helmIngressHTTP{Paths: []helmIngressPath{
			{Path: "/v2/", PathType: "Prefix", Backend: helmIngressBackend{
				ServiceName: "harbor-core", ServicePort: 80}},
			{Path: "/", PathType: "Prefix", Backend: helmIngressBackend{
				ServiceName: "harbor-portal", ServicePort: 80}},
		}}},
	}
	if helmIngressSignature(modern) != helmIngressSignature(legacy) {
		t.Fatalf("the same routing produced two signatures:\n%s\n%s", helmIngressSignature(modern), helmIngressSignature(legacy))
	}
}

// The live Ingress must route exactly like the rendered one. A different host
// or a different backend is a failed release.
func TestVerifyLiveIngressComparesTheRouting(t *testing.T) {
	root := t.TempDir()
	live := `{"metadata":{"name":"harbor-oilan-ingress","namespace":"harbor-system"},"spec":{"ingressClassName":"oilan-edge","tls":[{"hosts":["repo.oilan.ai"],"secretName":"oilan-ai-apex-tls"}],"rules":[{"host":"repo.oilan.ai","http":{"paths":[{"path":"/v2/","pathType":"Prefix","backend":{"service":{"name":"harbor-core","port":{"number":80}}}}]}}]}}`
	script := `#!/bin/sh
case "$*" in
  *"get ingress"*) printf '%s' "$LIVE_INGRESS" ;;
  *"get service"*) printf '{}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LIVE_INGRESS", live)

	content, err := validateHelmRenderedContent([]byte(helmTestIngress), "")
	if err != nil {
		t.Fatal(err)
	}
	command := helmCommand{release: "entry", namespace: "harbor-system", cwd: root}

	// The live object drops the / route, so it routes differently.
	err = verifyLiveIngress(t.Context(), command, content.Ingresses[0])
	if err == nil {
		t.Fatal("an Ingress that routes differently must fail verification")
	}
	if !strings.Contains(err.Error(), "routes differently") {
		t.Fatalf("error = %v, want the routing reason", err)
	}
}

// A backend Service that does not exist leaves the Ingress serving errors, so
// the release must not pass.
func TestVerifyLiveIngressFailsWhenTheBackendServiceIsMissing(t *testing.T) {
	root := t.TempDir()
	live := `{"metadata":{"name":"harbor-oilan-ingress","namespace":"harbor-system"},"spec":{"ingressClassName":"oilan-edge","tls":[{"hosts":["repo.oilan.ai"],"secretName":"oilan-ai-apex-tls"}],"rules":[{"host":"repo.oilan.ai","http":{"paths":[{"path":"/v2/","pathType":"Prefix","backend":{"service":{"name":"harbor-core","port":{"number":80}}}},{"path":"/","pathType":"Prefix","backend":{"service":{"name":"harbor-portal","port":{"number":80}}}}]}}]}}`
	script := `#!/bin/sh
case "$*" in
  *"get ingress"*) printf '%s' "$LIVE_INGRESS" ;;
  *"get service harbor-core"*) exit 1 ;;
  *"get service harbor-portal"*) printf '{"metadata":{"name":"harbor-portal"},"spec":{"ports":[{"port":80}]}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LIVE_INGRESS", live)

	content, err := validateHelmRenderedContent([]byte(helmTestIngress), "")
	if err != nil {
		t.Fatal(err)
	}
	command := helmCommand{release: "entry", namespace: "harbor-system", cwd: root}
	err = verifyLiveIngress(t.Context(), command, content.Ingresses[0])
	if err == nil {
		t.Fatal("a missing backend Service must fail verification")
	}
	if !strings.Contains(err.Error(), "backend Service harbor-system/harbor-core does not exist") {
		t.Fatalf("error = %v, want the missing-backend reason", err)
	}
}

// An Ingress that routes exactly like the rendered one, with its backends
// present, passes.
func TestVerifyLiveIngressAcceptsAMatchingIngress(t *testing.T) {
	root := t.TempDir()
	live := `{"metadata":{"name":"harbor-oilan-ingress","namespace":"harbor-system"},"spec":{"ingressClassName":"oilan-edge","tls":[{"hosts":["repo.oilan.ai"],"secretName":"oilan-ai-apex-tls"}],"rules":[{"host":"repo.oilan.ai","http":{"paths":[{"path":"/v2/","pathType":"Prefix","backend":{"service":{"name":"harbor-core","port":{"number":80}}}},{"path":"/","pathType":"Prefix","backend":{"service":{"name":"harbor-portal","port":{"number":80}}}}]}}]}}`
	script := `#!/bin/sh
case "$*" in
  *"get ingress"*) printf '%s' "$LIVE_INGRESS" ;;
  *"get service"*) printf '{"metadata":{"name":"svc"},"spec":{"ports":[{"port":80}]}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LIVE_INGRESS", live)

	content, err := validateHelmRenderedContent([]byte(helmTestIngress), "")
	if err != nil {
		t.Fatal(err)
	}
	command := helmCommand{release: "entry", namespace: "harbor-system", cwd: root}
	if err := verifyLiveIngress(t.Context(), command, content.Ingresses[0]); err != nil {
		t.Fatalf("a matching Ingress must pass verification: %v", err)
	}
}

// The whole pipeline of an Ingress-only release: the server-side dry-run
// renders the Ingress, Helm creates the revision, and the live Ingress is read
// back and compared. This is the path the repo.oilan.ai entry point needs,
// because it has no pod of its own to roll out.
func TestIngressOnlyReleaseRunsTheWholePipeline(t *testing.T) {
	root := t.TempDir()
	helm := filepath.Join(root, "helm")
	kubectl := filepath.Join(root, "kubectl")
	descriptionFile := filepath.Join(root, "description")
	ingressManifest := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: harbor-oilan-ingress
  namespace: harbor-system
spec:
  ingressClassName: oilan-edge
  tls:
    - hosts:
        - repo.oilan.ai
      secretName: oilan-ai-apex-tls
  rules:
    - host: repo.oilan.ai
      http:
        paths:
          - path: /v2/
            pathType: Prefix
            backend:
              service:
                name: harbor-core
                port:
                  number: 80
          - path: /
            pathType: Prefix
            backend:
              service:
                name: harbor-portal
                port:
                  number: 80
`
	liveIngress := `{"metadata":{"name":"harbor-oilan-ingress","namespace":"harbor-system"},"spec":{"ingressClassName":"oilan-edge","tls":[{"hosts":["repo.oilan.ai"],"secretName":"oilan-ai-apex-tls"}],"rules":[{"host":"repo.oilan.ai","http":{"paths":[{"path":"/v2/","pathType":"Prefix","backend":{"service":{"name":"harbor-core","port":{"number":80}}}},{"path":"/","pathType":"Prefix","backend":{"service":{"name":"harbor-portal","port":{"number":80}}}}]}}]}}`
	if err := os.WriteFile(helm, []byte(`#!/bin/sh
set -eu
if [ "$1" = "get" ] && [ "$2" = "manifest" ]; then
  cat <<'YAML'
`+ingressManifest+`YAML
  exit 0
fi
if [ "$1" = "status" ]; then
  desc=""
  if [ -f "${DESCRIPTION_FILE}" ]; then desc="$(cat "${DESCRIPTION_FILE}")"; fi
  printf '{"version":1,"info":{"description":"%s"}}\n' "$desc"
  exit 0
fi
for arg in "$@"; do
  if [ "$arg" = "--dry-run=server" ]; then
    printf '%s' "${DRY_JSON}"
    exit 0
  fi
done
previous=""
for arg in "$@"; do
  if [ "$previous" = "--description" ]; then printf '%s' "$arg" >"${DESCRIPTION_FILE}"; fi
  previous="$arg"
done
desc="$(cat "${DESCRIPTION_FILE}")"
printf '{"version":1,"info":{"description":"%s","notes":"chart-output-canary"}}\n' "$desc"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kubectl, []byte(`#!/bin/sh
set -eu
case "$*" in
  *"get ingress"*) cat <<'JSON'
`+liveIngress+`
JSON
    ;;
  *"get service"*) printf '{"metadata":{"name":"svc"},"spec":{"ports":[{"port":80}]}}' ;;
  *) exit 1 ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DESCRIPTION_FILE", descriptionFile)
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	t.Setenv("DRY_JSON", helmDryRunJSON(t, ingressManifest))

	params := testHelmParams()
	// An Ingress-only release carries no container image, so it states no
	// expected image.
	params.ExpectedImage = ""
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatalf("build helm command: %v", err)
	}
	command.namespace = "harbor-system"
	verification, err := runHelmCommand(context.Background(), command, func(notificationEvent) {})
	if err != nil {
		t.Fatalf("an Ingress-only release must publish: %v", err)
	}
	if !strings.Contains(verification, "final Helm workload spec verified") {
		t.Fatalf("verification summary missing: %q", verification)
	}
	if !strings.Contains(verification, "1 ingress(es)") {
		t.Fatalf("verification summary does not count the ingress: %q", verification)
	}
	if strings.Contains(verification, "chart-output-canary") {
		t.Fatalf("chart output crossed the user-facing verification boundary: %q", verification)
	}
}

// An Ingress pointing at a port the backend Service does not serve answers
// with errors, so that must fail the release too.
func TestVerifyLiveIngressFailsWhenTheBackendServiceDoesNotServeThePort(t *testing.T) {
	root := t.TempDir()
	live := `{"metadata":{"name":"harbor-oilan-ingress","namespace":"harbor-system"},"spec":{"ingressClassName":"oilan-edge","tls":[{"hosts":["repo.oilan.ai"],"secretName":"oilan-ai-apex-tls"}],"rules":[{"host":"repo.oilan.ai","http":{"paths":[{"path":"/v2/","pathType":"Prefix","backend":{"service":{"name":"harbor-core","port":{"number":80}}}},{"path":"/","pathType":"Prefix","backend":{"service":{"name":"harbor-portal","port":{"number":80}}}}]}}]}}`
	script := `#!/bin/sh
case "$*" in
  *"get ingress"*) printf '%s' "$LIVE_INGRESS" ;;
  *"get service"*) printf '{"metadata":{"name":"svc"},"spec":{"ports":[{"port":8080}]}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LIVE_INGRESS", live)

	content, err := validateHelmRenderedContent([]byte(helmTestIngress), "")
	if err != nil {
		t.Fatal(err)
	}
	command := helmCommand{release: "entry", namespace: "harbor-system", cwd: root}
	err = verifyLiveIngress(t.Context(), command, content.Ingresses[0])
	if err == nil {
		t.Fatal("a backend Service that does not serve the routed port must fail verification")
	}
	if !strings.Contains(err.Error(), "does not expose port 80") {
		t.Fatalf("error = %v, want the port reason", err)
	}
}
