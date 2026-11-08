package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A chart can render a workload into another namespace than the release
// (test/zhiyong renders DaemonSet labs/lab-image-prewarm). Verification must read
// it from that namespace, or a successful release is reported as failed.
func TestHelmWorkloadsKeepTheirRenderedNamespace(t *testing.T) {
	inRelease := "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: prewarm\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/prewarm:1\n"
	inLabs := strings.Replace(inRelease, "  name: prewarm\n", "  name: prewarm\n  namespace: labs\n", 1)
	inOilan := strings.Replace(inRelease, "  name: prewarm\n", "  name: prewarm\n  namespace: oilan\n", 1)
	join := func(docs ...string) []byte { return []byte(strings.Join(docs, "\n---\n")) }

	parseCases := []struct {
		name     string
		manifest []byte
		want     []string // namespace/Kind/Name, "" namespace means release namespace
		wantErr  string
	}{
		{"no namespace stays the release namespace", join(inRelease), []string{"/DaemonSet/prewarm"}, ""},
		{"rendered namespace is kept", join(inLabs), []string{"labs/DaemonSet/prewarm"}, ""},
		{"same name in different namespaces is not a duplicate", join(inRelease, inLabs, inOilan),
			[]string{"/DaemonSet/prewarm", "labs/DaemonSet/prewarm", "oilan/DaemonSet/prewarm"}, ""},
		{"same name in the same namespace is a duplicate", join(inLabs, inLabs), nil,
			"helm_manifest_invalid: duplicate DaemonSet/labs/prewarm"},
	}
	for _, tc := range parseCases {
		t.Run(tc.name, func(t *testing.T) {
			workloads, err := validateHelmRenderedImages(tc.manifest, "")
			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want prefix %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(workloads))
			for _, w := range workloads {
				got = append(got, w.Namespace+"/"+w.Kind+"/"+w.Name)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("workloads = %q, want %q", got, tc.want)
			}
		})
	}

	// The fake kubectl only serves the DaemonSet from namespace "labs".
	verifyCases := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{"workload rendered into labs is read from labs", inLabs, ""},
		{"workload without namespace is read from the release namespace", inRelease, "read final DaemonSet test/prewarm"},
	}
	for _, tc := range verifyCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "manifest.yaml"), []byte(tc.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			writeScript := func(name, body string) {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeScript("helm", "#!/bin/sh\ncat \"$MANIFEST\"\n")
			writeScript("kubectl", `#!/bin/sh
ns=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--namespace" ]; then ns="$2"; fi
  shift
done
if [ "$ns" != "labs" ]; then echo 'Error from server (NotFound)' >&2; exit 1; fi
printf '{"metadata":{"generation":3},"spec":{"template":{"spec":{"containers":[{"image":"registry.example/prewarm:1"}]}}},"status":{"observedGeneration":3,"desiredNumberScheduled":3,"updatedNumberScheduled":3,"numberReady":3,"numberAvailable":3}}\n'
`)
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("MANIFEST", filepath.Join(root, "manifest.yaml"))
			command := helmCommand{cwd: root, release: "zhiyong", namespace: "test"}
			_, err := verifyHelmDeploymentState(t.Context(), command)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verification failed: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
