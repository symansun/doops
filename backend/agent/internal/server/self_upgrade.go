package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Self-upgrade is a Kubernetes Job, not a Helm command in the agent process.
// The Job uses the kubeconfig volume the agent already mounts, so the operator
// does not pass a kubeconfig. Chart handoff stays maxSurge=1 and maxUnavailable=0.
// If the new Pod does not stay ready, the Job itself rolls back; that controller
// is not the process the rollout deletes.

const (
	interruptedUpgradeRetiredReason = "interrupted agent upgrade was retired on gateway startup"
	selfUpgradeLabelKey             = "app.kubernetes.io/name"
	selfUpgradeLabelValue           = "doops-self-upgrade"
	selfUpgradeChartPrefix          = "doops-agent"
	selfUpgradeContainer            = "doops-agent"
	selfUpgradeChartPath            = "/app/deploy/helm/doops-agent"
)

var (
	imageRepositoryPattern  = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	selfUpgradeJobPattern   = regexp.MustCompile(`^doops-self-upgrade-[a-f0-9]{8}$`)
	selfUpgradeRBJobPattern = regexp.MustCompile(`^doops-self-upgrade-rb-[a-f0-9]{8}$`)
)

type upgradeExec func(ctx context.Context, stdin string, name string, args ...string) ([]byte, error)

type upgradeProcess struct {
	namespace string
	hostname  string
	cluster   string
	instance  string
	run       upgradeExec
}

func currentUpgradeProcess() upgradeProcess {
	host, _ := os.Hostname()
	return upgradeProcess{
		namespace: readNamespaceFile(),
		hostname:  host,
		cluster:   os.Getenv("DOOPS_GATEWAY_CLUSTER"),
		instance:  os.Getenv("DOOPS_GATEWAY_INSTANCE"),
		run:       defaultUpgradeExec,
	}
}

func defaultUpgradeExec(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.CombinedOutput()
}

func (p upgradeProcess) command(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	run := p.run
	if run == nil {
		run = defaultUpgradeExec
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return run(cctx, stdin, name, args...)
}

func handleAgentUpgrade(argBytes json.RawMessage) (string, error) {
	var args agentUpgradeParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid agent upgrade params: %w", err)
	}
	return upgradeAgent(context.Background(), args, currentUpgradeProcess())
}

func validateAgentUpgradeDispatch(args agentUpgradeParams) error {
	if strings.TrimSpace(args.ObserveJob) != "" && args.Rollback {
		return fmt.Errorf("observe_job cannot be combined with rollback")
	}
	if strings.TrimSpace(args.ObserveJob) != "" || args.Rollback {
		return nil
	}
	_, _, err := parsePinnedImage(args.Image)
	return err
}

func upgradeAgent(ctx context.Context, args agentUpgradeParams, proc upgradeProcess) (string, error) {
	if strings.TrimSpace(args.ObserveJob) != "" && args.Rollback {
		return "", fmt.Errorf("observe_job cannot be combined with rollback")
	}
	if args.DryRun && (args.Rollback || strings.TrimSpace(args.ObserveJob) != "") {
		return "", fmt.Errorf("dry_run cannot be combined with observe_job or rollback")
	}
	switch {
	case args.DryRun:
		return dryRunAgentUpgrade(args)
	case strings.TrimSpace(args.ObserveJob) != "":
		return proc.observeSelfUpgrade(ctx, args)
	case args.Rollback:
		return proc.startRollbackJob(ctx, args)
	default:
		return proc.startSelfUpgradeJob(ctx, args)
	}
}

func dryRunAgentUpgrade(args agentUpgradeParams) (string, error) {
	image := strings.TrimSpace(args.Image)
	if image == "" {
		return "", fmt.Errorf("image is required")
	}
	if _, _, err := parsePinnedImage(image); err != nil {
		return "", err
	}
	mode := strings.TrimSpace(args.Mode)
	if mode == "" || mode == "auto" {
		mode = detectAgentUpgradeMode()
	}
	switch mode {
	case "k8s":
		namespace := firstNonEmptyString(args.Namespace, readNamespaceFile(), "oilan-system")
		workload := firstNonEmptyString(args.Workload, "deployment/doops-agent")
		container := firstNonEmptyString(args.Container, selfUpgradeContainer)
		return fmt.Sprintf(
			"dry-run: image=%s runtime=k8s namespace=%s workload=%s container=%s; an in-cluster Job will helm-upgrade that workload and roll back if the new Pod does not stay ready",
			image, namespace, workload, container,
		), nil
	case "daemonset", "docker", "container", "unsupported", "bare":
		return "", fmt.Errorf("self-upgrade supports the managed Helm Deployment only; existing unmanaged workloads require an explicit Helm migration, not automatic adoption by scripts/release-managed-agent.sh")
	default:
		return "", fmt.Errorf("unsupported agent upgrade mode %q", mode)
	}
}

func (p upgradeProcess) startSelfUpgradeJob(ctx context.Context, args agentUpgradeParams) (string, error) {
	mode := strings.TrimSpace(args.Mode)
	if mode == "" || mode == "auto" {
		mode = detectAgentUpgradeMode()
	}
	if mode == "docker" || mode == "container" || mode == "daemonset" || strings.HasPrefix(strings.TrimSpace(args.Workload), "daemonset/") {
		return "", fmt.Errorf("self-upgrade supports the managed Helm Deployment only")
	}
	if mode != "k8s" {
		return "", fmt.Errorf("unsupported agent upgrade mode %q", mode)
	}
	repository, digest, err := parsePinnedImage(args.Image)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Container) != "" && strings.TrimSpace(args.Container) != selfUpgradeContainer {
		return "", fmt.Errorf("self-upgrade container must be %s", selfUpgradeContainer)
	}
	namespace, err := p.agentNamespace(args.Namespace)
	if err != nil {
		return "", err
	}
	release, workload, err := p.managedRelease(ctx, namespace, args.Release, args.Workload)
	if err != nil {
		return "", err
	}
	pod, err := p.podSpec(ctx, namespace)
	if err != nil {
		return "", err
	}
	jobName := "doops-self-upgrade-" + strings.TrimPrefix(digest, "sha256:")[:8]
	if err := p.replaceFinishedJob(ctx, namespace, jobName); err != nil {
		return "", err
	}
	manifest, err := selfUpgradeJobManifest(jobName, namespace, pod, selfUpgradePlan{
		image:      repository + "@" + digest,
		repository: repository,
		digest:     digest,
		release:    release,
		workload:   workload,
		script:     selfUpgradeApplyScript,
	})
	if err != nil {
		return "", err
	}
	if err := p.applyJob(ctx, namespace, manifest); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"self-upgrade-accepted job=%s namespace=%s release=%s workload=%s image=%s@%s",
		jobName, namespace, release, workload, repository, digest,
	), nil
}

func (p upgradeProcess) startRollbackJob(ctx context.Context, args agentUpgradeParams) (string, error) {
	if strings.TrimSpace(args.Image) != "" {
		return "", fmt.Errorf("rollback does not take an image")
	}
	namespace, err := p.agentNamespace(args.Namespace)
	if err != nil {
		return "", err
	}
	release, workload, err := p.managedRelease(ctx, namespace, args.Release, args.Workload)
	if err != nil {
		return "", err
	}
	pod, err := p.podSpec(ctx, namespace)
	if err != nil {
		return "", err
	}
	jobName := "doops-self-upgrade-rb-" + randomHex(4)
	if err := p.replaceFinishedJob(ctx, namespace, jobName); err != nil {
		return "", err
	}
	manifest, err := selfUpgradeJobManifest(jobName, namespace, pod, selfUpgradePlan{
		image:    pod.image,
		release:  release,
		workload: workload,
		script:   selfUpgradeRollbackScript,
	})
	if err != nil {
		return "", err
	}
	if err := p.applyJob(ctx, namespace, manifest); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"self-upgrade-accepted job=%s namespace=%s release=%s workload=%s image=rollback",
		jobName, namespace, release, workload,
	), nil
}

func (p upgradeProcess) observeSelfUpgrade(ctx context.Context, args agentUpgradeParams) (string, error) {
	jobName := strings.TrimSpace(args.ObserveJob)
	if !selfUpgradeJobPattern.MatchString(jobName) && !selfUpgradeRBJobPattern.MatchString(jobName) {
		return "", fmt.Errorf("observe_job must name a doops self-upgrade job")
	}
	namespace, err := p.agentNamespace(args.Namespace)
	if err != nil {
		return "", err
	}
	phase, found, err := p.jobPhase(ctx, namespace, jobName)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("self-upgrade job %s was not found", jobName)
	}
	logs := p.jobLogs(ctx, namespace, jobName)
	switch phase {
	case "succeeded":
		return fmt.Sprintf("self-upgrade-succeeded job=%s namespace=%s\n%s", jobName, namespace, logs), nil
	case "failed":
		return fmt.Sprintf("self-upgrade-failed job=%s namespace=%s\n%s", jobName, namespace, logs), nil
	default:
		return fmt.Sprintf("self-upgrade-running job=%s namespace=%s", jobName, namespace), nil
	}
}

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
	if !foundMount {
		return selfUpgradePod{}, fmt.Errorf("agent pod has no /root/.kube volume for the in-cluster kubeconfig")
	}
	volume, err := volumeByName(pod.Spec.Volumes, selectedMount.Name)
	if err != nil {
		return selfUpgradePod{}, err
	}
	nodeName, err := nodeNameForVolume(volume, pod.Spec.NodeName)
	if err != nil {
		return selfUpgradePod{}, err
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
		nodeName:           nodeName,
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

func (p upgradeProcess) replaceFinishedJob(ctx context.Context, namespace, jobName string) error {
	active, err := p.activeSelfUpgrade(ctx, namespace)
	if err != nil {
		return err
	}
	if active != "" {
		return fmt.Errorf("self-upgrade job %s is already running", active)
	}
	phase, found, err := p.jobPhase(ctx, namespace, jobName)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if phase == "running" {
		return fmt.Errorf("self-upgrade job %s is already running", jobName)
	}
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "delete", "job", jobName, "--ignore-not-found")
	if err != nil {
		return commandError("delete finished self-upgrade job", out, err)
	}
	return nil
}

func (p upgradeProcess) activeSelfUpgrade(ctx context.Context, namespace string) (string, error) {
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "get", "jobs", "-l", selfUpgradeLabelKey+"="+selfUpgradeLabelValue, "-o", "json")
	if err != nil {
		return "", commandError("list self-upgrade jobs", out, err)
	}
	var listed struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Active int `json:"active"`
			} `json:"status"`
		} `json:"items"`
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return "", fmt.Errorf("parse self-upgrade jobs: %w", err)
	}
	for _, item := range listed.Items {
		if item.Status.Active > 0 {
			return item.Metadata.Name, nil
		}
	}
	return "", nil
}

func (p upgradeProcess) jobPhase(ctx context.Context, namespace, jobName string) (string, bool, error) {
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "get", "job", jobName, "-o", "json")
	if err != nil {
		if isNotFound(out, err) {
			return "", false, nil
		}
		return "", false, commandError("read self-upgrade job", out, err)
	}
	var job struct {
		Status struct {
			Active    int `json:"active"`
			Succeeded int `json:"succeeded"`
			Failed    int `json:"failed"`
		} `json:"status"`
	}
	if err := json.Unmarshal(out, &job); err != nil {
		return "", false, fmt.Errorf("parse self-upgrade job: %w", err)
	}
	switch {
	case job.Status.Succeeded > 0:
		return "succeeded", true, nil
	case job.Status.Failed > 0:
		return "failed", true, nil
	default:
		return "running", true, nil
	}
}

func (p upgradeProcess) jobLogs(ctx context.Context, namespace, jobName string) string {
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "logs", "job/"+jobName, "--tail=80")
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		text = strings.TrimSpace(err.Error())
	}
	return tailText(text, 4000)
}

func (p upgradeProcess) applyJob(ctx context.Context, namespace string, manifest []byte) error {
	out, err := p.command(ctx, string(manifest), "kubectl", "-n", namespace, "apply", "-f", "-")
	if err != nil {
		return commandError("create self-upgrade job", out, err)
	}
	return nil
}

type selfUpgradePlan struct {
	image      string
	repository string
	digest     string
	release    string
	workload   string
	script     string
}

type jobVolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SubPath   string `json:"subPath,omitempty"`
	ReadOnly  bool   `json:"readOnly"`
}

func selfUpgradeJobManifest(jobName, namespace string, pod selfUpgradePod, plan selfUpgradePlan) ([]byte, error) {
	if strings.TrimSpace(pod.image) == "" && plan.image == "" {
		return nil, fmt.Errorf("self-upgrade job image is required")
	}
	image := plan.image
	if image == "" {
		image = pod.image
	}
	env := []map[string]string{
		{"name": "DOOPS_UPGRADE_NAMESPACE", "value": namespace},
		{"name": "DOOPS_UPGRADE_RELEASE", "value": plan.release},
		{"name": "DOOPS_UPGRADE_WORKLOAD", "value": plan.workload},
	}
	if plan.repository != "" {
		env = append(env,
			map[string]string{"name": "DOOPS_UPGRADE_REPOSITORY", "value": plan.repository},
			map[string]string{"name": "DOOPS_UPGRADE_DIGEST", "value": plan.digest},
			map[string]string{"name": "DOOPS_UPGRADE_IMAGE", "value": plan.repository + "@" + plan.digest},
		)
	}
	pullSecrets := make([]map[string]string, 0, len(pod.imagePullSecrets))
	for _, name := range pod.imagePullSecrets {
		pullSecrets = append(pullSecrets, map[string]string{"name": name})
	}
	podSpec := map[string]interface{}{
		"restartPolicy": "Never",
		"containers": []map[string]interface{}{
			{
				"name":            "controller",
				"image":           image,
				"imagePullPolicy": "IfNotPresent",
				"command":         []string{"/bin/sh", "-c", plan.script},
				"env":             env,
				"securityContext": map[string]bool{"privileged": false},
				"volumeMounts":    []jobVolumeMount{pod.mount},
			},
		},
		"volumes": []json.RawMessage{pod.volume},
	}
	if pod.serviceAccountName != "" {
		podSpec["serviceAccountName"] = pod.serviceAccountName
	}
	if len(pullSecrets) > 0 {
		podSpec["imagePullSecrets"] = pullSecrets
	}
	if pod.nodeName != "" {
		podSpec["nodeName"] = pod.nodeName
	}
	manifest := map[string]interface{}{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]interface{}{
			"name":      jobName,
			"namespace": namespace,
			"labels": map[string]string{
				selfUpgradeLabelKey: selfUpgradeLabelValue,
			},
			"annotations": map[string]string{
				"doops.sh/release":  plan.release,
				"doops.sh/workload": plan.workload,
			},
		},
		"spec": map[string]interface{}{
			"backoffLimit":            0,
			"ttlSecondsAfterFinished": 600,
			"activeDeadlineSeconds":   1200,
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{
						selfUpgradeLabelKey: selfUpgradeLabelValue,
					},
				},
				"spec": podSpec,
			},
		},
	}
	return json.Marshal(manifest)
}

func parsePinnedImage(image string) (string, string, error) {
	image = strings.TrimSpace(image)
	at := strings.LastIndex(image, "@sha256:")
	if at <= 0 {
		return "", "", fmt.Errorf("agent upgrade image must be repository@sha256:<64 hex digits>")
	}
	repository := image[:at]
	digest := image[at+1:]
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(digest) {
		return "", "", fmt.Errorf("agent upgrade image must be repository@sha256:<64 hex digits>")
	}
	if !imageRepositoryPattern.MatchString(repository) {
		return "", "", fmt.Errorf("agent upgrade image repository is invalid")
	}
	return repository, digest, nil
}

func validateK8sName(kind, name string, max int) error {
	if len(name) == 0 || len(name) > max || !k8sNamePattern.MatchString(name) {
		return fmt.Errorf("invalid %s %q", kind, name)
	}
	return nil
}

func isNotFound(out []byte, err error) bool {
	text := strings.ToLower(string(out))
	if err != nil {
		text += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(text, "notfound") || strings.Contains(text, "not found")
}

func commandError(action string, out []byte, err error) error {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s", action, tailText(text, 2000))
}

func tailText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[len(text)-limit:]
}

const selfUpgradeKubeconfigPrelude = `set -eu
sleep 5
source=/root/.kube/config
if [ ! -f "$source" ]; then
  echo "kubeconfig volume is missing" >&2
  exit 1
fi
export KUBECONFIG=/tmp/doops-kubeconfig
if [ -n "${KUBERNETES_SERVICE_HOST:-}" ]; then
  python3 /app/configure_kubeconfig.py "$source" "$KUBECONFIG" "$KUBERNETES_SERVICE_HOST" "${KUBERNETES_SERVICE_PORT_HTTPS:-443}"
else
  cp "$source" "$KUBECONFIG"
  chmod 600 "$KUBECONFIG"
fi
ns="$DOOPS_UPGRADE_NAMESPACE"
release="$DOOPS_UPGRADE_RELEASE"
workload="$DOOPS_UPGRADE_WORKLOAD"
`

const selfUpgradeDeploymentVerifier = `verify_deployment() {
  python3 - "$ns" "$workload" "$1" <<'PY'
import json
import re
import subprocess
import sys

namespace, expected_workload, expected_image = sys.argv[1:4]
deployment = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "deployment", expected_workload, "-o", "json"],
    timeout=30,
))

metadata = deployment.get("metadata") or {}
spec = deployment.get("spec") or {}
status = deployment.get("status") or {}
name = str(metadata.get("name") or "")
if name != expected_workload:
    raise SystemExit(
        f"Deployment identity changed: {name or '<unknown>'} != {expected_workload}"
    )

generation = int(metadata.get("generation") or 0)
observed_generation = int(status.get("observedGeneration") or 0)
if generation <= 0 or observed_generation != generation:
    raise SystemExit("Deployment controller has not observed the current generation")

desired = int(spec.get("replicas") if spec.get("replicas") is not None else 1)
if desired <= 0:
    raise SystemExit(f"Deployment desiredReplicas={desired} must be positive")
for field in ("replicas", "updatedReplicas", "readyReplicas", "availableReplicas"):
    actual = int(status.get(field) or 0)
    if actual != desired:
        raise SystemExit(
            f"Deployment {field}={actual}, desiredReplicas={desired}"
        )
if int(status.get("unavailableReplicas") or 0) != 0:
    raise SystemExit("Deployment still has unavailable replicas")

containers = ((spec.get("template") or {}).get("spec") or {}).get("containers") or []
actual_image = next(
    (container.get("image") for container in containers
     if container.get("name") == "doops-agent"),
    None,
)
if actual_image != expected_image:
    raise SystemExit(
        f"Deployment doops-agent image mismatch: {actual_image or '<none>'} != {expected_image}"
    )

selector = ((spec.get("selector") or {}).get("matchLabels") or {})
if not selector:
    raise SystemExit("Deployment selector.matchLabels is empty")
selector_text = ",".join(f"{key}={value}" for key, value in sorted(selector.items()))
pods = json.loads(subprocess.check_output(
    ["kubectl", "-n", namespace, "get", "pods", "-l", selector_text, "-o", "json"],
    timeout=30,
)).get("items") or []
pods = [pod for pod in pods if not (pod.get("metadata") or {}).get("deletionTimestamp")]
if len(pods) != desired:
    raise SystemExit(f"Active Agent Pods={len(pods)}, desiredReplicas={desired}")
expected_digest = expected_image.rsplit("@", 1)[1] if "@sha256:" in expected_image else ""
for pod in pods:
    metadata = pod.get("metadata") or {}
    uid = str(metadata.get("uid") or "")
    if not uid:
        raise SystemExit("Agent Pod is missing metadata.uid")
    status = pod.get("status") or {}
    if status.get("phase") != "Running" or not any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in status.get("conditions") or []
    ):
        raise SystemExit("Agent Pod is not Running and Ready")
    containers = (pod.get("spec") or {}).get("containers") or []
    container = next((item for item in containers if item.get("name") == "doops-agent"), None)
    if not container or container.get("image") != expected_image:
        raise SystemExit("Agent Pod spec image does not match the expected image")
    statuses = status.get("containerStatuses") or []
    agent = next((item for item in statuses if item.get("name") == "doops-agent"), None)
    if not agent or not agent.get("ready"):
        raise SystemExit("Agent container is not Ready")
    image_id = str(agent.get("imageID") or "")
    actual_digest = image_id.split("://")[-1].rsplit("@", 1)[-1]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", actual_digest):
        raise SystemExit("Agent Pod imageID has no valid sha256 digest")
    if expected_digest and actual_digest != expected_digest:
        raise SystemExit("Agent Pod imageID does not match the expected digest")
    print("pod_verified uid=" + uid + " imageID=" + image_id)
print("deployment_verified=" + name + " generation=" + str(generation))
PY
}
`

const selfUpgradeApplyScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + `repository="$DOOPS_UPGRADE_REPOSITORY"
digest="$DOOPS_UPGRADE_DIGEST"
target="$DOOPS_UPGRADE_IMAGE"
chart=` + selfUpgradeChartPath + `
previous_image="$(python3 - "$ns" "$workload" <<'PY'
import json, subprocess, sys
ns, workload = sys.argv[1:3]
doc = json.loads(subprocess.check_output(["kubectl", "-n", ns, "get", "deployment", workload, "-o", "json"]))
strategy = (doc.get("spec") or {}).get("strategy") or {}
if strategy.get("type") != "RollingUpdate":
    raise SystemExit("deployment strategy must be RollingUpdate")
rolling = strategy.get("rollingUpdate") or {}
if rolling.get("maxUnavailable") not in (0, "0"):
    raise SystemExit("maxUnavailable must be 0")
surge = rolling.get("maxSurge")
if isinstance(surge, bool) or surge not in (1, "1"):
    raise SystemExit("maxSurge must be 1")
containers = ((doc.get("spec") or {}).get("template") or {}).get("spec", {}).get("containers") or []
actual_image = next(
    (container.get("image") for container in containers
     if container.get("name") == "doops-agent"),
    None,
)
if not actual_image:
    raise SystemExit("deployment must contain a doops-agent container")
print(actual_image)
PY
)"
previous="$(helm history "$release" -n "$ns" -o json | python3 -c 'import json,sys; rows=json.load(sys.stdin); print(max(int(row["revision"]) for row in rows if row["status"]=="deployed"))')"
selector_json="$(kubectl -n "$ns" get deployment "$workload" -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(json.dumps(d["spec"]["selector"]["matchLabels"], separators=(",", ":"), sort_keys=True))')"
values="${TMPDIR:-/tmp}/doops-upgrade-values.json"
helm get values "$release" -n "$ns" -o json >"$values"
python3 - "$values" "$repository" "$digest" "$selector_json" <<'PY'
import json, sys
path, repository, digest, selector_json = sys.argv[1:5]
with open(path, encoding="utf-8") as fh:
    values = json.load(fh)
image = values.setdefault("image", {})
image.pop("tag", None)
image["repository"] = repository
image["digest"] = digest
values.setdefault("workload", {})["selectorLabels"] = json.loads(selector_json)
with open(path, "w", encoding="utf-8") as fh:
    json.dump(values, fh)
PY
mutated=false
rollback_release() {
  echo "rolling back ${release} to ${previous}" >&2
  if ! helm rollback "$release" "$previous" -n "$ns" --timeout 300s --wait; then
    echo "Error: automatic rollback failed: Helm rollback command failed" >&2
    return 1
  fi
  if ! kubectl -n "$ns" rollout status "deployment/${workload}" --timeout 300s; then
    echo "Error: automatic rollback failed: rolled-back Deployment did not become ready" >&2
    return 1
  fi
  if ! verify_deployment "$previous_image"; then
    echo "Error: automatic rollback failed: rolled-back Deployment final state is not verified" >&2
    return 1
  fi
  echo "automatic rollback complete revision=${previous}" >&2
}
cleanup() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ "$mutated" = true ]; then
    if ! rollback_release; then
      echo "Error: automatic rollback failed; manual intervention is required" >&2
      exit 1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT
helm upgrade "$release" "$chart" -n "$ns" -f "$values" --timeout 180s --wait=false --dry-run=server
mutated=true
helm upgrade "$release" "$chart" -n "$ns" -f "$values" --timeout 180s --wait=false
deadline=$(( $(date +%s) + 600 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if verify_deployment "$target"; then
    sleep 15
    if verify_deployment "$target"; then
      echo "self-upgrade complete image=${target}"
      exit 0
    fi
  fi
  sleep 5
done
echo "new deployment did not stay ready" >&2
exit 1
`

const selfUpgradeRollbackScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + `previous="$(helm history "$release" -n "$ns" -o json | python3 -c 'import json,sys; rows=json.load(sys.stdin); current=max(int(row["revision"]) for row in rows if row["status"]=="deployed"); earlier=[int(row["revision"]) for row in rows if int(row["revision"]) < current and row["status"]=="superseded"]; print(max(earlier)) if earlier else sys.exit("no previous successful superseded revision")')"
expected="$(python3 - "$release" "$ns" "$previous" "$workload" <<'PY'
import subprocess, sys, yaml
release, namespace, revision, workload = sys.argv[1:5]
manifest = subprocess.check_output(
    ["helm", "get", "manifest", release, "-n", namespace, "--revision", revision],
    timeout=30,
)
images = [
    container["image"]
    for doc in yaml.safe_load_all(manifest) if doc and doc.get("kind") == "Deployment"
    and (doc.get("metadata") or {}).get("name") == workload
    for container in doc["spec"]["template"]["spec"]["containers"]
    if container.get("name") == "doops-agent"
]
if len(images) != 1 or not images[0]:
    raise SystemExit("rollback revision must contain exactly one doops-agent image")
print(images[0])
PY
)"
helm rollback "$release" "$previous" -n "$ns" --timeout 300s --wait
kubectl -n "$ns" rollout status "deployment/${workload}" --timeout 300s
verify_deployment "$expected"
echo "self-upgrade rolled back to ${previous}"
`
