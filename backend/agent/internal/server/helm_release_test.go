package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	helmTestDeployment = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/api:1\n"
	helmTestHookJob    = "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: registry-sync\n  annotations:\n    helm.sh/hook: pre-upgrade\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/sync:1\n"
)

// writeFakeHelm installs a helm that answers the dry-run with dryRunJSON (or
// fails with dryRunStderr), and the real run with mutationStderr when set.
func writeFakeHelm(t *testing.T, dryRunJSON, dryRunStderr, mutationStderr string) (string, string) {
	t.Helper()
	root := t.TempDir()
	marker := filepath.Join(root, "mutation-ran")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--dry-run=server" ]; then
    if [ -n "$DRY_STDERR" ]; then printf '%s\n' "$DRY_STDERR" >&2; exit 1; fi
    printf '%s' "$DRY_JSON"
    exit 0
  fi
done
touch "$MUTATION_MARKER"
if [ -n "$MUTATION_STDERR" ]; then printf 'chart NOTES token=leaked-stdout-canary\n'; printf '%s\n' "$MUTATION_STDERR" >&2; exit 1; fi
exit 1
`
	if err := os.WriteFile(filepath.Join(root, "helm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DRY_JSON", dryRunJSON)
	t.Setenv("DRY_STDERR", dryRunStderr)
	t.Setenv("MUTATION_STDERR", mutationStderr)
	t.Setenv("MUTATION_MARKER", marker)
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	return root, marker
}

func helmDryRunJSON(t *testing.T, manifest string, hooks ...string) string {
	t.Helper()
	type hook struct {
		Manifest string `json:"manifest"`
	}
	payload := struct {
		Manifest string `json:"manifest"`
		Hooks    []hook `json:"hooks"`
	}{Manifest: manifest}
	for _, h := range hooks {
		payload.Hooks = append(payload.Hooks, hook{Manifest: h})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Each failure names its own stable code and the concrete reason. A wrong code
// sends the operator after the wrong cause, which is how a YAML decode error
// was once read as an image mismatch.
func TestHelmUpgradeFailuresNameTheirStepAndReason(t *testing.T) {
	expected := "registry.example/api:1"
	cases := []struct {
		name           string
		dryRunJSON     string
		dryRunStderr   string
		mutationStderr string
		expectedImage  string
		wantPrefix     string
		wantReason     string
		wantMutation   bool
	}{
		{
			name:         "dry-run error carries Helm's first stderr line",
			dryRunStderr: "Error: values don't meet the specifications of the schema(s) in the following chart(s):\nzhiyong:\n- image: Invalid type",
			wantPrefix:   "helm_preflight_failed: ",
			wantReason:   "values don't meet the specifications",
		},
		{
			name:         "dry-run stderr secrets are redacted",
			dryRunStderr: "Error: template: password=hunter2-canary failed",
			wantPrefix:   "helm_preflight_failed: ",
			wantReason:   "REDACTED",
		},
		{
			name:       "dry-run output that is not JSON",
			dryRunJSON: "not json",
			wantPrefix: "helm_manifest_invalid: ",
			wantReason: "not JSON",
		},
		{
			name:       "undecodable manifest is invalid, not an image mismatch",
			dryRunJSON: `{"manifest":"kind: Deployment\nmetadata:\n  name: api\nmetadata:\n  name: again\n"}`,
			wantPrefix: "helm_manifest_invalid: ",
			wantReason: "YAML document 1",
		},
		{
			name:       "release with only a ConfigMap has no workload",
			dryRunJSON: `{"manifest":"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n"}`,
			wantPrefix: "helm_no_workload: ",
			wantReason: "no verifiable workload or ingress",
		},
		{
			name:          "expected image missing lists what was rendered",
			dryRunJSON:    "", // filled below
			expectedImage: "registry.example/api:2",
			wantPrefix:    "helm_image_mismatch: ",
			wantReason:    "Deployment/api=registry.example/api:1",
		},
		{
			name:           "real helm failure carries stderr, never chart stdout",
			dryRunJSON:     "", // filled below
			mutationStderr: "Error: UPGRADE FAILED: pre-upgrade hooks failed: job registry-sync failed: BackoffLimitExceeded",
			expectedImage:  expected,
			wantPrefix:     "helm_mutation_failed: ",
			wantReason:     "pre-upgrade hooks failed",
			wantMutation:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dryRun := tc.dryRunJSON
			if dryRun == "" && tc.dryRunStderr == "" {
				dryRun = helmDryRunJSON(t, helmTestDeployment, helmTestHookJob)
			}
			_, marker := writeFakeHelm(t, dryRun, tc.dryRunStderr, tc.mutationStderr)
			params := testHelmParams()
			params.ExpectedImage = tc.expectedImage
			command, err := buildHelmCommand(params)
			if err != nil {
				t.Fatal(err)
			}
			_, err = runHelmCommand(t.Context(), command, nil)
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) || !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("error = %v, want prefix %q containing %q", err, tc.wantPrefix, tc.wantReason)
			}
			for _, leak := range []string{"hunter2-canary", "leaked-stdout-canary"} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("error leaked %q: %v", leak, err)
				}
			}
			if _, statErr := os.Stat(marker); (statErr == nil) != tc.wantMutation {
				t.Fatalf("mutation ran = %t, want %t", statErr == nil, tc.wantMutation)
			}
		})
	}
}

func TestParseRenderedHelmWorkloadsCoversEveryWorkloadKind(t *testing.T) {
	statefulSet := "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: db\nspec:\n  template:\n    spec:\n      initContainers:\n        - image: registry.example/migrate:1\n      containers:\n        - image: registry.example/db:1\n"
	daemonSet := "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: agent\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/agent:1\n"
	cronJob := "apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: nightly\nspec:\n  jobTemplate:\n    spec:\n      template:\n        spec:\n          containers:\n            - image: registry.example/nightly:1\n"
	plainJob := "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: once\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/once:1\n"
	sameNameService := "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\n"
	sameNameJob := strings.ReplaceAll(helmTestHookJob, "registry-sync", "api")
	join := func(docs ...string) []byte { return []byte(strings.Join(docs, "\n---\n")) }

	cases := []struct {
		name          string
		manifest      []byte
		expectedImage string
		want          []string // Kind/Name=images
		wantErr       string
	}{
		{
			name:     "every workload kind with init containers first",
			manifest: join(helmTestDeployment, statefulSet, daemonSet, cronJob, helmTestHookJob),
			want: []string{
				"Deployment/api=registry.example/api:1",
				"StatefulSet/db=registry.example/migrate:1,registry.example/db:1",
				"DaemonSet/agent=registry.example/agent:1",
				"CronJob/nightly=registry.example/nightly:1",
				"Job/registry-sync=registry.example/sync:1",
			},
		},
		{
			name:          "expected image may sit in an init container",
			manifest:      join(statefulSet),
			expectedImage: "registry.example/migrate:1",
			want:          []string{"StatefulSet/db=registry.example/migrate:1,registry.example/db:1"},
		},
		{
			name:     "ordinary Jobs and non-workloads are skipped",
			manifest: join(helmTestDeployment, plainJob, sameNameService),
			want:     []string{"Deployment/api=registry.example/api:1"},
		},
		{
			name:     "same name with a different kind is not a duplicate",
			manifest: join(helmTestDeployment, sameNameJob),
			want:     []string{"Deployment/api=registry.example/api:1", "Job/api=registry.example/sync:1"},
		},
		{name: "same kind and name is a duplicate", manifest: join(helmTestDeployment, helmTestDeployment), wantErr: "helm_manifest_invalid: duplicate Deployment/api"},
		{name: "workload without containers", manifest: []byte("kind: Deployment\nmetadata:\n  name: empty\n"), wantErr: "helm_manifest_invalid: Deployment/empty has no containers"},
		{name: "container without image", manifest: []byte("kind: DaemonSet\nmetadata:\n  name: ds\nspec:\n  template:\n    spec:\n      containers:\n        - name: x\n"), wantErr: "helm_manifest_invalid: DaemonSet/ds has a container without an image"},
		{name: "List wrapper", manifest: []byte("kind: List\nitems: []\n"), wantErr: "helm_manifest_invalid: List wrappers"},
		{name: "nothing to verify", manifest: join(plainJob, sameNameService), wantErr: "helm_no_workload"},
		{name: "image mismatch", manifest: join(helmTestDeployment), expectedImage: "registry.example/api:2", wantErr: "helm_image_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workloads, err := validateHelmRenderedImages(tc.manifest, tc.expectedImage)
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
				got = append(got, w.Kind+"/"+w.Name+"="+strings.Join(w.Containers, ","))
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("workloads = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLiveWorkloadRolledOutPerKind(t *testing.T) {
	live := func(mutate func(*liveHelmWorkload)) liveHelmWorkload {
		var w liveHelmWorkload
		w.Metadata.Generation = 3
		w.Status.ObservedGeneration = 3
		mutate(&w)
		return w
	}
	two := int32(2)
	cases := []struct {
		name    string
		kind    string
		live    liveHelmWorkload
		wantErr string
	}{
		{"deployment ready", "Deployment", live(func(w *liveHelmWorkload) {
			w.Spec.Replicas = &two
			w.Status.UpdatedReplicas, w.Status.ReadyReplicas, w.Status.AvailableReplicas = 2, 2, 2
		}), ""},
		{"deployment still rolling", "Deployment", live(func(w *liveHelmWorkload) {
			w.Spec.Replicas = &two
			w.Status.UpdatedReplicas, w.Status.ReadyReplicas, w.Status.AvailableReplicas = 1, 2, 2
		}), "not fully Ready"},
		{"stale generation", "Deployment", live(func(w *liveHelmWorkload) { w.Status.ObservedGeneration = 2 }), "observedGeneration=2"},
		{"statefulset ready", "StatefulSet", live(func(w *liveHelmWorkload) {
			w.Spec.Replicas = &two
			w.Status.UpdatedReplicas, w.Status.ReadyReplicas = 2, 2
		}), ""},
		{"statefulset OnDelete ignores updated count", "StatefulSet", live(func(w *liveHelmWorkload) {
			w.Spec.Replicas = &two
			w.Spec.UpdateStrategy.Type = "OnDelete"
			w.Status.ReadyReplicas = 2
		}), ""},
		{"statefulset not ready", "StatefulSet", live(func(w *liveHelmWorkload) {
			w.Spec.Replicas = &two
			w.Status.UpdatedReplicas, w.Status.ReadyReplicas = 2, 1
		}), "not fully Ready"},
		{"daemonset ready", "DaemonSet", live(func(w *liveHelmWorkload) {
			w.Status.DesiredNumberScheduled, w.Status.UpdatedNumberScheduled = 3, 3
			w.Status.NumberReady, w.Status.NumberAvailable = 3, 3
		}), ""},
		{"daemonset unavailable", "DaemonSet", live(func(w *liveHelmWorkload) {
			w.Status.DesiredNumberScheduled, w.Status.UpdatedNumberScheduled = 3, 3
			w.Status.NumberReady, w.Status.NumberAvailable, w.Status.NumberUnavailable = 2, 2, 1
		}), "not fully Ready"},
		{"hook job complete", "Job", live(func(w *liveHelmWorkload) { w.Status.Succeeded = 1 }), ""},
		{"hook job failed", "Job", live(func(w *liveHelmWorkload) { w.Status.Failed = 1 }), "is not complete"},
		{"cronjob has no rollout", "CronJob", live(func(w *liveHelmWorkload) { w.Metadata.Generation = 0 }), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := liveWorkloadRolledOut(tc.kind, tc.live)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
