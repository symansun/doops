package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func (p upgradeProcess) agentNamespace(requested string) (string, error) {
	namespace := strings.TrimSpace(p.namespace)
	if namespace == "" {
		return "", fmt.Errorf("self-upgrade requires the agent pod namespace")
	}
	if err := validateK8sName("namespace", namespace, 63); err != nil {
		return "", err
	}
	requested = strings.TrimSpace(requested)
	if requested != "" && requested != namespace {
		return "", fmt.Errorf("self-upgrade namespace must be the agent namespace %s", namespace)
	}
	return namespace, nil
}

func (p upgradeProcess) managedRelease(ctx context.Context, namespace, requested, workload string) (string, string, error) {
	if strings.TrimSpace(p.cluster) == "" || strings.TrimSpace(p.instance) == "" {
		return "", "", fmt.Errorf("self-upgrade requires DOOPS_GATEWAY_CLUSTER and DOOPS_GATEWAY_INSTANCE")
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if err := validateK8sName("release", requested, 53); err != nil {
			return "", "", err
		}
	}
	out, err := p.command(ctx, "", "helm", "list", "-n", namespace, "-o", "json")
	if err != nil {
		return "", "", commandError("list helm releases", out, err)
	}
	var listed []struct {
		Name  string `json:"name"`
		Chart string `json:"chart"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return "", "", fmt.Errorf("parse helm releases: %w", err)
	}
	matches := make([]string, 0, 1)
	for _, release := range listed {
		chart := strings.TrimSpace(release.Chart)
		if chart != selfUpgradeChartPrefix && !strings.HasPrefix(chart, selfUpgradeChartPrefix+"-") {
			continue
		}
		if requested != "" && release.Name != requested {
			continue
		}
		matches = append(matches, release.Name)
	}
	if len(matches) == 0 {
		return "", "", errNoManagedRelease
	}
	if len(matches) != 1 {
		return "", "", fmt.Errorf("expected exactly one managed doops-agent Helm release in %s, found %d", namespace, len(matches))
	}
	release := matches[0]
	if err := validateK8sName("release", release, 53); err != nil {
		return "", "", err
	}
	valuesOut, err := p.command(ctx, "", "helm", "get", "values", release, "-n", namespace, "-o", "json")
	if err != nil {
		return "", "", commandError("read helm values", valuesOut, err)
	}
	var values struct {
		Gateway struct {
			Cluster  string `json:"cluster"`
			Instance string `json:"instance"`
		} `json:"gateway"`
		Workload struct {
			Name string `json:"name"`
		} `json:"workload"`
	}
	if err := json.Unmarshal(valuesOut, &values); err != nil {
		return "", "", fmt.Errorf("parse helm values: %w", err)
	}
	if values.Gateway.Cluster != p.cluster || values.Gateway.Instance != p.instance {
		return "", "", fmt.Errorf("helm release %s identity does not match this agent", release)
	}
	liveWorkload := strings.TrimSpace(values.Workload.Name)
	if err := validateK8sName("workload", liveWorkload, 63); err != nil {
		return "", "", err
	}
	if err := workloadMatches(workload, liveWorkload); err != nil {
		return "", "", err
	}
	return release, liveWorkload, nil
}

func workloadMatches(requested, live string) error {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil
	}
	requested = strings.TrimPrefix(requested, "deployment/")
	if strings.Contains(requested, "/") {
		return fmt.Errorf("self-upgrade supports the managed Helm Deployment only")
	}
	if requested != live {
		return fmt.Errorf("workload %s does not match the Helm release workload %s", requested, live)
	}
	return nil
}

type selfUpgradePod struct {
	image              string
	serviceAccountName string
	runtimeClassName   string
	nodeName           string
	imagePullSecrets   []string
	mount              jobVolumeMount
	volume             json.RawMessage
}

func (p upgradeProcess) podSpec(ctx context.Context, namespace string) (selfUpgradePod, error) {
	if strings.TrimSpace(p.hostname) == "" {
		return selfUpgradePod{}, fmt.Errorf("self-upgrade requires the agent pod name")
	}
	if err := validateK8sName("pod", p.hostname, 253); err != nil {
		return selfUpgradePod{}, err
	}
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "get", "pod", p.hostname, "-o", "json")
	if err != nil {
		return selfUpgradePod{}, commandError("read agent pod", out, err)
	}
	var pod struct {
		Spec struct {
			NodeName           string `json:"nodeName"`
			ServiceAccountName string `json:"serviceAccountName"`
			RuntimeClassName   string `json:"runtimeClassName"`
			ImagePullSecrets   []struct {
				Name string `json:"name"`
			} `json:"imagePullSecrets"`
			Containers []struct {
				Name         string `json:"name"`
				Image        string `json:"image"`
				VolumeMounts []struct {
					Name      string `json:"name"`
					MountPath string `json:"mountPath"`
					SubPath   string `json:"subPath"`
					ReadOnly  bool   `json:"readOnly"`
				} `json:"volumeMounts"`
			} `json:"containers"`
			Volumes []json.RawMessage `json:"volumes"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(out, &pod); err != nil {
		return selfUpgradePod{}, fmt.Errorf("parse agent pod: %w", err)
	}
	var containerImage string
	var selectedMount jobVolumeMount
	foundMount := false
	for _, container := range pod.Spec.Containers {
		if container.Name != selfUpgradeContainer {
			continue
		}
		containerImage = container.Image
		for _, mount := range container.VolumeMounts {
			if mount.MountPath != "/root/.kube" && mount.MountPath != "/root/.kube/config" {
				continue
			}
			selectedMount = jobVolumeMount{
				Name:      mount.Name,
				MountPath: mount.MountPath,
				SubPath:   mount.SubPath,
				ReadOnly:  mount.ReadOnly,
			}
			foundMount = true
			if mount.MountPath == "/root/.kube/config" {
				break
			}
		}
	}
	if containerImage == "" {
		return selfUpgradePod{}, fmt.Errorf("agent pod has no %s container", selfUpgradeContainer)
	}
	var volume json.RawMessage
	if foundMount {
		volume, err = volumeByName(pod.Spec.Volumes, selectedMount.Name)
		if err != nil {
			return selfUpgradePod{}, err
		}
		if _, err = nodeNameForVolume(volume, pod.Spec.NodeName); err != nil {
			return selfUpgradePod{}, err
		}
	} else if sa := strings.TrimSpace(pod.Spec.ServiceAccountName); sa == "" || sa == "default" {
		// Without a kubeconfig volume the Job authenticates as the Agent's own
		// ServiceAccount; the namespace default account carries no Agent authority.
		return selfUpgradePod{}, fmt.Errorf("agent pod has no /root/.kube volume and no dedicated ServiceAccount")
	}
	if strings.TrimSpace(pod.Spec.NodeName) == "" {
		return selfUpgradePod{}, fmt.Errorf("self-upgrade requires the agent pod node name")
	}
	secrets := make([]string, 0, len(pod.Spec.ImagePullSecrets))
	for _, secret := range pod.Spec.ImagePullSecrets {
		if strings.TrimSpace(secret.Name) != "" {
			secrets = append(secrets, secret.Name)
		}
	}
	return selfUpgradePod{
		image:              containerImage,
		serviceAccountName: pod.Spec.ServiceAccountName,
		runtimeClassName:   pod.Spec.RuntimeClassName,
		nodeName:           pod.Spec.NodeName,
		imagePullSecrets:   secrets,
		mount:              selectedMount,
		volume:             volume,
	}, nil
}

func volumeByName(volumes []json.RawMessage, name string) (json.RawMessage, error) {
	for _, raw := range volumes {
		var meta struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("parse pod volume: %w", err)
		}
		if meta.Name == name {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("pod volume %s was not found", name)
}

func nodeNameForVolume(volume json.RawMessage, nodeName string) (string, error) {
	var kinds map[string]json.RawMessage
	if err := json.Unmarshal(volume, &kinds); err != nil {
		return "", fmt.Errorf("parse kubeconfig volume: %w", err)
	}
	if _, ok := kinds["emptyDir"]; ok {
		return "", fmt.Errorf("kubeconfig volume cannot be shared with the upgrade Job")
	}
	if _, ok := kinds["hostPath"]; ok {
		if strings.TrimSpace(nodeName) == "" {
			return "", fmt.Errorf("hostPath kubeconfig requires the agent pod node name")
		}
		return nodeName, nil
	}
	return "", nil
}

func detectAgentUpgradeMode() string {
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return "k8s"
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker"
	}
	if _, err := exec.LookPath("nerdctl"); err == nil {
		return "docker"
	}
	return "unsupported"
}

func readNamespaceFile() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
