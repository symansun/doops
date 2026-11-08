package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Helm --wait returns once ready >= replicas - maxUnavailable, so a Deployment
// with maxUnavailable >= 1 can still be rolling when Helm reports success
// (test/zhiyong-lab-api: ready=0 right after Helm returned). Verification must
// wait for the rollout instead of failing a release that converges seconds later.
func TestHelmVerificationWaitsForRollout(t *testing.T) {
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/api:2\n"
	ready := `{"metadata":{"generation":3},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"registry.example/api:2"}]}}},"status":{"observedGeneration":3,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1}}`
	rolling := `{"metadata":{"generation":3},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"registry.example/api:2"}]}}},"status":{"observedGeneration":3,"updatedReplicas":1,"readyReplicas":0,"availableReplicas":0,"unavailableReplicas":1}}`
	wrongImage := strings.Replace(ready, "api:2", "api:1", 1)

	cases := []struct {
		name       string
		responses  []string // kubectl answers in order; the last one repeats
		timeout    time.Duration
		wantErr    string
		wantMinGet int
	}{
		{"ready at once", []string{ready}, time.Second, "", 1},
		{"ready after rolling", []string{rolling, rolling, ready}, 5 * time.Second, "", 3},
		{"still rolling at timeout", []string{rolling}, time.Second, "is not fully Ready (desired=1 updated=1 ready=0 available=0 unavailable=1)", 2},
		{"wrong image fails without waiting", []string{wrongImage}, 5 * time.Second, "spec images", 1},
	}
	previous := helmRolloutPollInterval
	helmRolloutPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { helmRolloutPollInterval = previous })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, body string, mode os.FileMode) {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("manifest.yaml", manifest, 0o600)
			for i, response := range tc.responses {
				write("live-"+string(rune('0'+i)), response, 0o600)
			}
			write("helm", "#!/bin/sh\ncat \"$ROOT/manifest.yaml\"\n", 0o755)
			write("kubectl", `#!/bin/sh
n=$(cat "$ROOT/count" 2>/dev/null || echo 0)
echo $((n+1)) > "$ROOT/count"
last=$(( $LAST ))
[ "$n" -gt "$last" ] && n=$last
cat "$ROOT/live-$n"
`, 0o755)
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("ROOT", root)
			t.Setenv("LAST", string(rune('0'+len(tc.responses)-1)))

			_, err := verifyHelmDeploymentState(t.Context(), helmCommand{
				cwd: root, release: "app", namespace: "apps", rolloutTimeout: tc.timeout,
			})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("verification failed: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			countBytes, _ := os.ReadFile(filepath.Join(root, "count"))
			got, convErr := strconv.Atoi(strings.TrimSpace(string(countBytes)))
			if convErr != nil || got < tc.wantMinGet {
				t.Fatalf("kubectl reads = %q, want at least %d", countBytes, tc.wantMinGet)
			}
			if tc.name == "wrong image fails without waiting" && got != 1 {
				t.Fatalf("a wrong image was polled %d times; it can never resolve", got)
			}
		})
	}
}
