package server

import (
	"errors"
	"testing"
)

// Helm's --output=json returns hook manifests without a leading "---". They
// must still decode as separate documents next to the release manifest.
func TestPreflightManifestKeepsHooksAsSeparateDocuments(t *testing.T) {
	deployment := "---\n# Source: app/templates/api.yaml\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/api:release-1\n"
	hookJob := "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: registry-sync\n  annotations:\n    helm.sh/hook: pre-upgrade\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/sync:1\n"
	hooks := func(manifests ...string) []struct {
		Manifest string `json:"manifest"`
	} {
		out := make([]struct {
			Manifest string `json:"manifest"`
		}, len(manifests))
		for i, m := range manifests {
			out[i].Manifest = m
		}
		return out
	}
	cases := []struct {
		name          string
		manifest      string
		hooks         []string
		expectedImage string
		wantNames     []string
		wantMismatch  bool
		wantDecodeErr bool
	}{
		{"hook without separator after a deployment", deployment, []string{hookJob}, "", []string{"api", "registry-sync"}, false, false},
		{"hook with its own separator", deployment, []string{"---\n" + hookJob}, "", []string{"api", "registry-sync"}, false, false},
		{"expected image present in the deployment", deployment, []string{hookJob}, "registry.example/api:release-1", []string{"api", "registry-sync"}, false, false},
		{"expected image absent is a real mismatch", deployment, []string{hookJob}, "registry.example/api:other", nil, true, false},
		{"empty hook is skipped", deployment, []string{"  \n"}, "", []string{"api"}, false, false},
		{"broken manifest is a decode error, not an image mismatch", deployment + "  bad: [\n", nil, "", nil, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			combined := appendHelmHookManifests([]byte(tc.manifest), hooks(tc.hooks...))
			workloads, err := validateHelmRenderedImages(combined, tc.expectedImage)
			switch {
			case tc.wantMismatch:
				if !errors.Is(err, errHelmExpectedImageMissing) {
					t.Fatalf("err = %v, want expected-image mismatch", err)
				}
				return
			case tc.wantDecodeErr:
				if err == nil || errors.Is(err, errHelmExpectedImageMissing) {
					t.Fatalf("err = %v, want a decode error distinct from image mismatch", err)
				}
				return
			case err != nil:
				t.Fatalf("validate: %v", err)
			}
			if len(workloads) != len(tc.wantNames) {
				t.Fatalf("workloads = %#v, want %v", workloads, tc.wantNames)
			}
			for i, name := range tc.wantNames {
				if workloads[i].Name != name {
					t.Fatalf("workload %d = %q, want %q", i, workloads[i].Name, name)
				}
			}
		})
	}
}
