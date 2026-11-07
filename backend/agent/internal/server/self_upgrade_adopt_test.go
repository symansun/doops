package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var errNotFoundForTest = errors.New("exit status 1")

func adoptTestProcess(t *testing.T, releases string, applied *string) upgradeProcess {
	t.Helper()
	pod := `{"spec":{"nodeName":"node-a","serviceAccountName":"doops-agent","containers":[{"name":"doops-agent","image":"registry.example/doops:old","volumeMounts":[]}],"volumes":[]}}`
	return upgradeProcess{
		namespace: "doops-system", hostname: "doops-agent-abc", cluster: "doops-jm", instance: "jm-228",
		run: func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
			joined := name + " " + strings.Join(args, " ")
			switch {
			case strings.HasPrefix(joined, "helm list"):
				return []byte(releases), nil
			case strings.Contains(joined, "get pod doops-agent-abc"):
				return []byte(pod), nil
			case strings.Contains(joined, "get jobs"):
				return []byte(`{"items":[]}`), nil
			case strings.Contains(joined, "get job "):
				return []byte(`Error from server (NotFound)`), errNotFoundForTest
			case strings.Contains(joined, "apply -f -"):
				*applied = stdin
				return []byte("job created"), nil
			}
			return []byte("[]"), nil
		},
	}
}

func TestStartSelfUpgradeAdoptsWhenNoHelmReleaseExists(t *testing.T) {
	pinned := "registry.example/doops@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, image, workload, wantErr, wantRelease string
	}{
		{"default workload", pinned, "", "", "doops-agent"},
		{"named workload", pinned, "deployment/agent-x", "", "agent-x"},
		{"tag image refused", "registry.example/doops:v1", "", "requires a repository@sha256", ""},
		{"non-deployment refused", pinned, "statefulset/x", "Deployment workload only", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var applied string
			proc := adoptTestProcess(t, "[]", &applied)
			out, err := proc.startSelfUpgradeJob(context.Background(), agentUpgradeParams{Image: tc.image, Mode: "k8s", Workload: tc.workload})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || applied != "" {
					t.Fatalf("err=%v applied=%q", err, applied)
				}
				return
			}
			if err != nil || !strings.Contains(out, "release="+tc.wantRelease) {
				t.Fatalf("out=%q err=%v", out, err)
			}
			var job map[string]any
			if err := json.Unmarshal([]byte(applied), &job); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(applied, "/app/adopt_agent.py run") ||
				!strings.Contains(applied, `"value":"doops-jm"`) ||
				!strings.Contains(applied, `"value":"jm-228"`) ||
				!strings.Contains(applied, `"serviceAccountName":"doops-agent"`) ||
				!strings.Contains(applied, "DOOPS_UPGRADE_KUBECONFIG_SOURCE") {
				t.Fatalf("adoption Job is incomplete: %s", applied)
			}
		})
	}
}

func TestStartSelfUpgradeKeepsHelmPathWhenReleaseExists(t *testing.T) {
	var applied string
	proc := adoptTestProcess(t, `[{"name":"a","chart":"doops-agent-0.2.1"},{"name":"b","chart":"doops-agent-0.2.1"}]`, &applied)
	_, err := proc.startSelfUpgradeJob(context.Background(), agentUpgradeParams{
		Image: "registry.example/doops@sha256:" + strings.Repeat("a", 64), Mode: "k8s",
	})
	if err == nil || !strings.Contains(err.Error(), "found 2") || strings.Contains(applied, "adopt_agent.py") {
		t.Fatalf("ambiguous releases must not fall back to adoption: %v", err)
	}
}
