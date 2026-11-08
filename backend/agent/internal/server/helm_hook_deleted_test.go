package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helm deletes a hook Job with hook-delete-policy hook-succeeded as soon as it
// succeeds, and only continues the release after a successful hook. A missing
// Job is therefore success evidence for that policy, and only for that policy.
func TestHelmHookJobDeletedOnSuccessCountsAsComplete(t *testing.T) {
	hook := func(policy string) string {
		annotations := "    helm.sh/hook: pre-upgrade\n"
		if policy != "" {
			annotations += "    helm.sh/hook-delete-policy: " + policy + "\n"
		}
		return "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: registry-sync\n  annotations:\n" + annotations +
			"spec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/sync:1\n"
	}
	cases := []struct {
		name     string
		manifest string
		kubectl  string // fake kubectl behavior: notfound, forbidden
		wantErr  string
	}{
		{"hook-succeeded Job already deleted", hook("before-hook-creation,hook-succeeded"), "notfound", ""},
		{"Job without hook-succeeded must still exist", hook("before-hook-creation"), "notfound", "read final Job test/registry-sync"},
		{"Job without any delete policy must still exist", hook(""), "notfound", "read final Job test/registry-sync"},
		{"other read errors are never treated as deleted", hook("hook-succeeded"), "forbidden", "read final Job test/registry-sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, body string, mode os.FileMode) {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("manifest.yaml", tc.manifest, 0o600)
			write("helm", "#!/bin/sh\ncat \"$MANIFEST\"\n", 0o755)
			write("kubectl", `#!/bin/sh
if [ "$KUBECTL_MODE" = "notfound" ]; then
  echo 'Error from server (NotFound): jobs.batch "registry-sync" not found' >&2
else
  echo 'Error from server (Forbidden): jobs.batch is forbidden' >&2
fi
exit 1
`, 0o755)
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("MANIFEST", filepath.Join(root, "manifest.yaml"))
			t.Setenv("KUBECTL_MODE", tc.kubectl)

			_, err := verifyHelmDeploymentState(t.Context(), helmCommand{cwd: root, release: "zhiyong", namespace: "test"})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("deleted hook-succeeded Job rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
