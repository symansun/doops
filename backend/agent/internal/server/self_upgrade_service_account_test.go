package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func selfUpgradeTestPodJSON(serviceAccount string, withKubeMount bool) []byte {
	mounts := []map[string]any{{"name": "doagent-config", "mountPath": "/opt/doagent_config"}}
	volumes := []map[string]any{{"name": "doagent-config", "configMap": map[string]any{"name": "doops-agent-settings"}}}
	if withKubeMount {
		mounts = append(mounts, map[string]any{"name": "kube", "mountPath": "/root/.kube", "readOnly": true})
		volumes = append(volumes, map[string]any{"name": "kube", "secret": map[string]any{"secretName": "cluster-admin"}})
	}
	pod := map[string]any{"spec": map[string]any{
		"nodeName":           "node-a",
		"serviceAccountName": serviceAccount,
		"containers": []map[string]any{{
			"name": "doops-agent", "image": "registry.example/doops:old", "volumeMounts": mounts,
		}},
		"volumes": volumes,
	}}
	data, _ := json.Marshal(pod)
	return data
}

func TestSelfUpgradePodSpecChoosesKubeconfigSource(t *testing.T) {
	for _, tc := range []struct {
		name           string
		serviceAccount string
		kubeMount      bool
		wantErr        string
		wantVolume     bool
	}{
		{"mounted kubeconfig", "default", true, "", true},
		{"dedicated service account", "doops-agent", false, "", false},
		{"default service account without mount", "default", false, "no dedicated ServiceAccount", false},
		{"empty service account without mount", "", false, "no dedicated ServiceAccount", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := upgradeProcess{
				hostname: "doops-agent-0",
				run: func(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
					return selfUpgradeTestPodJSON(tc.serviceAccount, tc.kubeMount), nil
				},
			}
			pod, err := proc.podSpec(context.Background(), "doops-system")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (pod.volume != nil) != tc.wantVolume {
				t.Fatalf("volume present = %v, want %v", pod.volume != nil, tc.wantVolume)
			}
			if pod.serviceAccountName != tc.serviceAccount {
				t.Fatalf("service account = %q", pod.serviceAccountName)
			}
		})
	}
}

func TestSelfUpgradeJobUsesServiceAccountWithoutKubeconfigVolume(t *testing.T) {
	pod := selfUpgradePod{
		image:              "registry.example/doops:old",
		nodeName:           "node-a",
		serviceAccountName: "doops-agent",
	}
	manifest, err := selfUpgradeJobManifest("doops-self-upgrade-aaaaaaaa", "doops-system", pod, selfUpgradePlan{
		image: testPinnedImage(), repository: "registry.example/doops", digest: "sha256:" + testPinnedDigest,
		release: "doops-agent", workload: "doops-agent", script: selfUpgradeApplyScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	var job struct {
		Spec struct {
			Template struct {
				Spec struct {
					ServiceAccountName string            `json:"serviceAccountName"`
					Volumes            []json.RawMessage `json:"volumes"`
					Containers         []struct {
						Env          []map[string]string `json:"env"`
						VolumeMounts []json.RawMessage   `json:"volumeMounts"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(manifest, &job); err != nil {
		t.Fatal(err)
	}
	spec := job.Spec.Template.Spec
	if spec.ServiceAccountName != "doops-agent" {
		t.Fatalf("job must run as the Agent ServiceAccount, got %q", spec.ServiceAccountName)
	}
	if len(spec.Volumes) != 0 || len(spec.Containers[0].VolumeMounts) != 0 {
		t.Fatalf("job must not mount a kubeconfig volume: %s", manifest)
	}
	source := ""
	for _, item := range spec.Containers[0].Env {
		if item["name"] == "DOOPS_UPGRADE_KUBECONFIG_SOURCE" {
			source = item["value"]
		}
	}
	if source != "serviceAccount" {
		t.Fatalf("DOOPS_UPGRADE_KUBECONFIG_SOURCE = %q", source)
	}
}

func TestSelfUpgradePreludeWritesServiceAccountKubeconfig(t *testing.T) {
	if !strings.Contains(selfUpgradeKubeconfigPrelude, `configure_kubeconfig.py --service-account "$KUBECONFIG"`) {
		t.Fatal("prelude must build the kubeconfig from the ServiceAccount when no volume is mounted")
	}
	if !strings.Contains(selfUpgradeKubeconfigPrelude, `kubeconfig volume is missing`) {
		t.Fatal("mounted mode must still fail when the volume is missing")
	}
}
