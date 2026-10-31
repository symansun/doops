package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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

var errNoManagedRelease = errors.New("no managed doops-agent Helm release")

var (
	imageRepositoryPattern  = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	imageTagPattern         = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	imageDigestPattern      = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
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
	_, _, _, err := parseUpgradeImage(args.Image)
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
	if _, _, _, err := parseUpgradeImage(image); err != nil {
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
		return "", fmt.Errorf("doops upgrade replaces a Helm-managed Deployment; bring this workload under Helm first (see BUILD_DEPLOY_SOP.md, unmanaged Agent adoption)")
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
	repository, tag, digest, err := parseUpgradeImage(args.Image)
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
	script := selfUpgradeApplyScript
	if args.Preflight {
		script = selfUpgradePreflightScript
	}
	release, workload, err := p.managedRelease(ctx, namespace, args.Release, args.Workload)
	if errors.Is(err, errNoManagedRelease) {
		// No Helm release yet: the same controller Job brings the Deployment under Helm.
		release, workload, err = adoptionTarget(args.Release, args.Workload)
		if err == nil && digest == "" {
			err = fmt.Errorf("adopting an unmanaged Agent requires a repository@sha256:<digest> image")
		}
		script = selfUpgradeAdoptScript
		if args.Preflight {
			script = selfUpgradePullOnlyScript
		}
	}
	if err != nil {
		return "", err
	}
	pod, err := p.podSpec(ctx, namespace)
	if err != nil {
		return "", err
	}
	jobName := "doops-self-upgrade-" + upgradeImageID(args.Image)
	if err := p.replaceFinishedJob(ctx, namespace, jobName); err != nil {
		return "", err
	}
	manifest, err := selfUpgradeJobManifest(jobName, namespace, pod, selfUpgradePlan{
		image:                        strings.TrimSpace(args.Image),
		repository:                   repository,
		tag:                          tag,
		digest:                       digest,
		release:                      release,
		workload:                     workload,
		script:                       script,
		cluster:                      p.cluster,
		instance:                     p.instance,
		preflight:                    args.Preflight,
		allowMissingRollbackBaseline: args.AllowMissingRollbackBaseline,
	})
	if err != nil {
		return "", err
	}
	if err := p.applyJob(ctx, namespace, manifest); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"self-upgrade-accepted job=%s namespace=%s release=%s workload=%s image=%s",
		jobName, namespace, release, workload, strings.TrimSpace(args.Image),
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
	rollbackImage, revision, err := p.rollbackImage(ctx, namespace, release, workload)
	if err != nil {
		return "", err
	}
	repository, digest, err := parsePinnedImage(rollbackImage)
	if err != nil {
		return "", err
	}
	if err := p.replaceFinishedJob(ctx, namespace, jobName); err != nil {
		return "", err
	}
	manifest, err := selfUpgradeJobManifest(jobName, namespace, pod, selfUpgradePlan{
		image:      rollbackImage,
		repository: repository,
		digest:     digest,
		release:    release,
		workload:   workload,
		revision:   revision,
		script:     selfUpgradeRollbackScript,
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

func (p upgradeProcess) rollbackImage(ctx context.Context, namespace, release, workload string) (string, int, error) {
	out, err := p.command(ctx, "", "helm", "history", release, "-n", namespace, "-o", "json")
	if err != nil {
		return "", 0, commandError("read rollback history", out, err)
	}
	var history []struct {
		Revision int    `json:"revision"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(out, &history); err != nil {
		return "", 0, fmt.Errorf("parse rollback history: %w", err)
	}
	out, err = p.command(ctx, "", "kubectl", "-n", namespace, "get", "configmap", "doops-upgrade-verified-"+release, "--ignore-not-found", "-o", "json")
	if err != nil {
		return "", 0, commandError("read verified rollback evidence", out, err)
	}
	var stored struct {
		Data map[string]string `json:"data"`
	}
	var evidence struct {
		SchemaVersion int    `json:"schemaVersion"`
		Release       string `json:"release"`
		Workload      string `json:"workload"`
		Revisions     map[string]struct {
			Image string `json:"image"`
		} `json:"revisions"`
	}
	if json.Unmarshal(out, &stored) != nil || json.Unmarshal([]byte(stored.Data["evidence.json"]), &evidence) != nil ||
		evidence.SchemaVersion != 1 || evidence.Release != release || evidence.Workload != workload {
		return "", 0, fmt.Errorf("verified rollback evidence is missing or invalid; use the authorized external controller")
	}
	current, previous := 0, 0
	for _, row := range history {
		if row.Status == "deployed" && row.Revision > current {
			current = row.Revision
		}
	}
	for _, row := range history {
		if row.Status == "superseded" && row.Revision < current && row.Revision > previous && evidence.Revisions[fmt.Sprint(row.Revision)].Image != "" {
			previous = row.Revision
		}
	}
	if previous == 0 {
		return "", 0, fmt.Errorf("no previously verified superseded revision")
	}
	out, err = p.command(ctx, "", "helm", "get", "manifest", release, "-n", namespace, "--revision", fmt.Sprint(previous))
	if err != nil {
		return "", 0, commandError("read rollback manifest", out, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(out)))
	var images []string
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name  string `yaml:"name"`
							Image string `yaml:"image"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return "", 0, fmt.Errorf("parse rollback manifest: %w", err)
		}
		if doc.Kind != "Deployment" || doc.Metadata.Name != workload {
			continue
		}
		if doc.Metadata.Namespace != "" && doc.Metadata.Namespace != namespace {
			return "", 0, fmt.Errorf("rollback workload namespace mismatch")
		}
		for _, container := range doc.Spec.Template.Spec.Containers {
			if container.Name == selfUpgradeContainer {
				images = append(images, container.Image)
			}
		}
	}
	if len(images) != 1 {
		return "", 0, fmt.Errorf("rollback manifest must contain exactly one Agent image")
	}
	if _, _, err := parsePinnedImage(images[0]); err != nil {
		return "", 0, fmt.Errorf("rollback requires an immutable historical image: %w", err)
	}
	if evidence.Revisions[fmt.Sprint(previous)].Image != images[0] {
		return "", 0, fmt.Errorf("rollback manifest does not match verified evidence")
	}
	return images[0], previous, nil
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
		// A failed upgrade names its reason with a stable category, so the
		// operator reads why the release was left alone instead of guessing.
		if category := selfUpgradeFailureCategory(logs); category != "" {
			return fmt.Sprintf("self-upgrade-failed job=%s namespace=%s category=%s\n%s", jobName, namespace, category, logs), nil
		}
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
	tag        string
	digest     string
	release    string
	workload   string
	revision   int
	script     string
	cluster    string
	instance   string
	preflight  bool
	// allowMissingRollbackBaseline lets the operator accept an upgrade whose
	// rollback baseline image can no longer be pulled.
	allowMissingRollbackBaseline bool
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
	if plan.allowMissingRollbackBaseline {
		env = append(env, map[string]string{"name": "DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE", "value": "1"})
	}
	if plan.revision != 0 {
		env = append(env, map[string]string{"name": "DOOPS_UPGRADE_REVISION", "value": fmt.Sprint(plan.revision)})
	}
	if plan.cluster != "" {
		env = append(env, map[string]string{"name": "DOOPS_GATEWAY_CLUSTER", "value": plan.cluster},
			map[string]string{"name": "DOOPS_GATEWAY_INSTANCE", "value": plan.instance})
	}
	if plan.repository != "" {
		env = append(env, map[string]string{"name": "DOOPS_UPGRADE_REPOSITORY", "value": plan.repository})
		if plan.tag != "" {
			env = append(env, map[string]string{"name": "DOOPS_UPGRADE_TAG", "value": plan.tag})
		}
		if plan.digest != "" {
			env = append(env, map[string]string{"name": "DOOPS_UPGRADE_DIGEST", "value": plan.digest})
		}
		env = append(env, map[string]string{"name": "DOOPS_UPGRADE_IMAGE", "value": plan.image})
	}
	pullPolicy := "Always"
	if plan.digest != "" {
		pullPolicy = "IfNotPresent"
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
				"imagePullPolicy": pullPolicy,
				"command":         []string{"/bin/sh", "-c", plan.script},
				"env":             env,
				"securityContext": map[string]bool{"privileged": false},
			},
		},
	}
	container := podSpec["containers"].([]map[string]interface{})[0]
	if pod.volume != nil {
		container["volumeMounts"] = []jobVolumeMount{pod.mount}
		podSpec["volumes"] = []json.RawMessage{pod.volume}
	} else {
		env = append(env, map[string]string{"name": "DOOPS_UPGRADE_KUBECONFIG_SOURCE", "value": "serviceAccount"})
		container["env"] = env
	}
	if pod.serviceAccountName != "" {
		podSpec["serviceAccountName"] = pod.serviceAccountName
	}
	if pod.runtimeClassName != "" {
		podSpec["runtimeClassName"] = pod.runtimeClassName
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
	if plan.preflight {
		// An unpullable image never fails the pod, it just waits; bound that wait.
		manifest["spec"].(map[string]interface{})["activeDeadlineSeconds"] = 600
	}
	return json.Marshal(manifest)
}

func parseUpgradeImage(image string) (string, string, string, error) {
	image = strings.TrimSpace(image)
	at := strings.LastIndex(image, "@sha256:")
	if at > 0 {
		repository := image[:at]
		digest := image[at+1:]
		if !imageDigestPattern.MatchString(digest) {
			return "", "", "", fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
		}
		if !imageRepositoryPattern.MatchString(repository) {
			return "", "", "", fmt.Errorf("agent upgrade image repository is invalid")
		}
		return repository, "", digest, nil
	}
	colon := strings.LastIndex(image, ":")
	slash := strings.LastIndex(image, "/")
	if colon <= slash {
		return "", "", "", fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
	}
	repository := image[:colon]
	tag := image[colon+1:]
	if !imageRepositoryPattern.MatchString(repository) {
		return "", "", "", fmt.Errorf("agent upgrade image repository is invalid")
	}
	if !imageTagPattern.MatchString(tag) {
		return "", "", "", fmt.Errorf("agent upgrade image tag is invalid")
	}
	return repository, tag, "", nil
}

func parsePinnedImage(image string) (string, string, error) {
	repository, _, digest, err := parseUpgradeImage(image)
	if err != nil {
		return "", "", err
	}
	if digest == "" {
		return "", "", fmt.Errorf("rollback requires an immutable historical image")
	}
	return repository, digest, nil
}

func upgradeImageID(image string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(image)))
	return hex.EncodeToString(digest[:])[:8]
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
export KUBECONFIG=/tmp/doops-kubeconfig
if [ "${DOOPS_UPGRADE_KUBECONFIG_SOURCE:-mounted}" = "serviceAccount" ]; then
  python3 /app/configure_kubeconfig.py --service-account "$KUBECONFIG" "$KUBERNETES_SERVICE_HOST" "${KUBERNETES_SERVICE_PORT_HTTPS:-443}"
elif [ ! -f "$source" ]; then
  echo "kubeconfig volume is missing" >&2
  exit 1
elif [ -n "${KUBERNETES_SERVICE_HOST:-}" ]; then
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
  python3 - "$ns" "$workload" "$1" "${2:-controller}" "${3:-}" <<'PY'
import json
import os
import re
import subprocess
import sys

namespace, expected_workload, expected_image, identity_source, record = sys.argv[1:6]
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
for field in ("updatedReplicas", "readyReplicas", "availableReplicas"):
    actual = int(status.get(field) or 0)
    if actual < desired:
        raise SystemExit(
            f"Deployment {field}={actual}, desiredReplicas={desired}"
        )
if int(status.get("replicas") or 0) < desired:
    raise SystemExit(
        f"Deployment replicas={int(status.get('replicas') or 0)}, desiredReplicas={desired}"
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
matching_pods = []
for pod in pods:
    containers = (pod.get("spec") or {}).get("containers") or []
    container = next((item for item in containers if item.get("name") == "doops-agent"), None)
    if not container or container.get("image") != expected_image:
        ready = any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in (pod.get("status") or {}).get("conditions") or []
        )
        if ready:
            raise SystemExit("Active Ready Agent Pod still uses a different image")
        continue
    matching_pods.append(pod)
if len(matching_pods) < desired:
    raise SystemExit(f"Active Agent Pods={len(matching_pods)}, desiredReplicas={desired}")
pods = matching_pods
expected_digest = expected_image.rsplit("@", 1)[1] if "@sha256:" in expected_image else ""
controller_node = ""
runtime_class = ""
if identity_source == "baseline":
    baseline = json.loads(os.environ["DOOPS_UPGRADE_BASELINE"])
    if expected_image != baseline.get("image"):
        raise SystemExit("Rollback image does not match the captured baseline")
    expected_digest = baseline.get("digest") or ""
    controller_node = baseline.get("node") or ""
    runtime_class = baseline.get("runtime_class") or ""
    if not controller_node or not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_digest):
        raise SystemExit("Rollback baseline runtime identity is missing")
elif identity_source == "controller":
    if expected_image != os.environ.get("DOOPS_UPGRADE_IMAGE"):
        raise SystemExit("Controller image does not match the requested image")
    controller = json.loads(subprocess.check_output(
        ["kubectl", "-n", namespace, "get", "pod", os.environ["HOSTNAME"], "-o", "json"],
        timeout=30,
    ))
    controller_spec = controller.get("spec") or {}
    controller_node = controller_spec.get("nodeName") or ""
    runtime_class = controller_spec.get("runtimeClassName") or ""
    pinned = next((item for item in controller_spec.get("containers") or []
                   if item.get("name") == "controller"), None)
    running = next((item for item in (controller.get("status") or {}).get("containerStatuses") or []
                    if item.get("name") == "controller"), None)
    if not controller_node or not pinned or pinned.get("image") != expected_image:
        raise SystemExit("Controller Pod does not match the requested image and node")
    if not running or not running.get("ready"):
        raise SystemExit("Controller container is not Ready")
    expected_digest = str(running.get("imageID") or "").split("://")[-1].rsplit("@", 1)[-1]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_digest):
        raise SystemExit("Controller Pod imageID has no valid sha256 digest")
else:
    raise SystemExit("Unknown runtime identity source")
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
    if controller_node and (pod.get("spec") or {}).get("nodeName") != controller_node:
        raise SystemExit("Agent and Controller Pods must use the same node runtime")
    if ((pod.get("spec") or {}).get("runtimeClassName") or "") != runtime_class:
        raise SystemExit("Agent and Controller Pods must use the same runtime class")
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
if record == "record":
    release = os.environ["DOOPS_UPGRADE_RELEASE"]
    rows = json.loads(subprocess.check_output(
        ["helm", "history", release, "-n", namespace, "-o", "json"], timeout=30,
    ))
    deployed = [int(row["revision"]) for row in rows if row["status"] == "deployed"]
    if len(deployed) != 1:
        raise SystemExit("Cannot bind verification to one deployed Helm revision")
    receipt_name = "doops-upgrade-verified-" + release
    raw = subprocess.check_output(
        ["kubectl", "-n", namespace, "get", "configmap", receipt_name, "--ignore-not-found", "-o", "json"], timeout=30,
    ).strip()
    receipt = json.loads(json.loads(raw)["data"]["evidence.json"]) if raw else {
        "schemaVersion": 1, "release": release, "workload": expected_workload, "revisions": {},
    }
    if receipt.get("schemaVersion") != 1 or receipt.get("release") != release or receipt.get("workload") != expected_workload:
        raise SystemExit("Verified revision evidence scope mismatch")
    revisions = receipt["revisions"]
    revisions[str(deployed[0])] = {"image": expected_image, "runtimeDigest": expected_digest,
                                  "podUID": uid, "node": controller_node, "runtimeClass": runtime_class}
    retained = {str(row["revision"]) for row in rows}
    receipt["revisions"] = {key: value for key, value in revisions.items() if key in retained}
    manifest = {"apiVersion": "v1", "kind": "ConfigMap",
                "metadata": {"name": receipt_name, "namespace": namespace},
                "data": {"evidence.json": json.dumps(receipt, sort_keys=True)}}
    subprocess.run(["kubectl", "-n", namespace, "apply", "-f", "-"],
                   input=json.dumps(manifest).encode(), check=True, timeout=30)
PY
}
`

// The stable categories an upgrade preflight can report. They name why an
// upgrade was refused, so an operator reads the reason instead of guessing
// whether the Agent or the release is broken.
const (
	selfUpgradeCategoryRollbackBaselineUnavailable = "rollback_baseline_image_unavailable"
	selfUpgradeCategoryRollbackBaselineUnresolved  = "rollback_baseline_image_unresolved"
	selfUpgradeCategoryRollbackBaselineProbeFailed = "rollback_baseline_probe_failed"
)

var selfUpgradeFailureCategories = []string{
	selfUpgradeCategoryRollbackBaselineUnavailable,
	selfUpgradeCategoryRollbackBaselineUnresolved,
	selfUpgradeCategoryRollbackBaselineProbeFailed,
}

// selfUpgradeFailureCategory reports the stable category a failed upgrade Job
// carried, or "" when the failure has no category. The category is part of the
// Job log, so it survives the Job, the observation and the CLI report.
func selfUpgradeFailureCategory(logs string) string {
	for _, category := range selfUpgradeFailureCategories {
		if strings.Contains(logs, category) {
			return category
		}
	}
	return ""
}

// selfUpgradeRevisionImage prints the doops-agent image of one Helm revision.
// The pre-change checks and the rollback script both read the rollback target
// through this one helper, so the two can never select different images.
const selfUpgradeRevisionImage = `revision_image() {
  python3 - "$release" "$ns" "$1" "$workload" <<'PY'
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
    and (doc.get("metadata") or {}).get("namespace", namespace) == namespace
    for container in ((doc.get("spec") or {}).get("template") or {}).get("spec", {}).get("containers") or []
    if container.get("name") == "doops-agent"
]
if len(images) != 1 or not images[0]:
    raise SystemExit("rollback_baseline_image_unresolved: the revision must contain exactly one doops-agent image")
print(images[0])
PY
}
`

// selfUpgradeRollbackBaselineProbe proves that the image an automatic rollback
// would restore can still be pulled.
//
// The probe is a throwaway Job in the Agent's namespace whose Pod runs the
// baseline image on the Agent's own node, with the Agent's own pull secrets,
// service account, runtime class and scheduling constraints. The kubelet that
// would serve the rollback Pod is therefore the kubelet that has to pull the
// baseline image, and the credentials are the ones the workload already has:
// no new credential dimension and no new mount.
//
// A manifest that is gone from the registry is therefore caught before the
// release changes, which is what makes the third upgrade guarantee (restore the
// backup when verification fails) real rather than aspirational.
//
// The operator can still proceed on purpose with
// DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE=1; the default is fail closed.
const selfUpgradeRollbackBaselineProbe = `rollback_baseline_probe="$(python3 - "$ns" "$rollback_baseline_image" "$previous" "$baseline" <<'PY' 2>&1
import json, subprocess, sys, time

namespace, image, revision, baseline_json = sys.argv[1:5]
baseline = json.loads(baseline_json)
probe_job = "doops-rollback-baseline-" + revision
PROBE_LABEL = "app.kubernetes.io/name=doops-rollback-baseline-probe"
CATEGORY_UNAVAILABLE = "rollback_baseline_image_unavailable"
CATEGORY_PROBE_FAILED = "rollback_baseline_probe_failed"
PULL_FAILURE_MARKERS = (
    "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "ErrImageNeverPull",
    "RegistryUnavailable", "failed to resolve reference", "manifest unknown",
)


def fail(category, detail):
    raise SystemExit(category + ": " + detail)


def kubectl(*args, stdin=None):
    return subprocess.run(
        ["kubectl", "-n", namespace] + list(args),
        input=stdin, capture_output=True, timeout=60,
    )


def cleanup():
    kubectl("delete", "job", probe_job, "--ignore-not-found", "--wait=false")


probe = {
    "apiVersion": "batch/v1",
    "kind": "Job",
    "metadata": {
        "name": probe_job,
        "namespace": namespace,
        "labels": {"app.kubernetes.io/name": "doops-rollback-baseline-probe"},
    },
    "spec": {
        "backoffLimit": 0,
        "ttlSecondsAfterFinished": 120,
        "activeDeadlineSeconds": 90,
        "template": {
            "metadata": {"labels": {"app.kubernetes.io/name": "doops-rollback-baseline-probe"}},
            "spec": {
                "restartPolicy": "Never",
                "terminationGracePeriodSeconds": 5,
                "containers": [{
                    "name": "probe",
                    "image": image,
                    "imagePullPolicy": "Always",
                    "command": ["/bin/sh", "-c", "exit 0"],
                }],
            },
        },
    },
}
pod_spec = probe["spec"]["template"]["spec"]
if baseline.get("node"):
    pod_spec["nodeName"] = baseline["node"]
if baseline.get("runtime_class"):
    pod_spec["runtimeClassName"] = baseline["runtime_class"]
if baseline.get("service_account"):
    pod_spec["serviceAccountName"] = baseline["service_account"]
if baseline.get("tolerations"):
    pod_spec["tolerations"] = baseline["tolerations"]
if baseline.get("node_selector"):
    pod_spec["nodeSelector"] = baseline["node_selector"]
if baseline.get("image_pull_secrets"):
    pod_spec["imagePullSecrets"] = baseline["image_pull_secrets"]

cleanup()
try:
    created = kubectl("create", "-f", "-", stdin=json.dumps(probe).encode())
    if created.returncode != 0:
        fail(CATEGORY_PROBE_FAILED, created.stderr.decode("utf-8", "replace").strip()[:400])

    def probe_pods():
        try:
            return subprocess.check_output(
                ["kubectl", "-n", namespace, "get", "pods", "-l", PROBE_LABEL, "-o", "json"],
                timeout=30, stderr=subprocess.STDOUT,
            ).decode("utf-8", "replace")
        except Exception:
            return ""

    deadline = time.time() + 90
    while time.time() < deadline:
        read = kubectl("get", "job", probe_job, "-o", "json")
        if read.returncode != 0:
            fail(CATEGORY_PROBE_FAILED, "the pull probe job could not be read")
        status = json.loads(read.stdout).get("status") or {}
        if int(status.get("succeeded") or 0) > 0:
            print("rollback_baseline_image_available image=" + image)
            raise SystemExit(0)

        # An unpullable image leaves the Pod in a pull failure state and the Job
        # active forever, so the Pod state is what names the reason. The Job
        # status alone would report this as a timeout instead.
        evidence = probe_pods()
        if any(marker in evidence for marker in PULL_FAILURE_MARKERS):
            fail(CATEGORY_UNAVAILABLE, "image=" + image + " reason=the image could not be pulled")
        if int(status.get("failed") or 0) > 0:
            if any(marker in evidence for marker in PULL_FAILURE_MARKERS):
                fail(CATEGORY_UNAVAILABLE, "image=" + image + " reason=the image could not be pulled")
            fail(CATEGORY_PROBE_FAILED, "image=" + image + " reason=the pull probe job failed")
        time.sleep(3)
    fail(CATEGORY_PROBE_FAILED, "image=" + image + " reason=the pull probe did not settle in time")
finally:
    cleanup()
PY
)" || {
  case "$rollback_baseline_probe" in
    *rollback_baseline_image_unavailable*)
      if [ "${DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE:-0}" != "1" ]; then
        echo "$rollback_baseline_probe" >&2
        exit 1
      fi
      echo "warning: $rollback_baseline_probe; the operator allowed this upgrade without a pullable rollback baseline" >&2
      ;;
    *)
      echo "rollback_baseline_probe_failed: $rollback_baseline_probe" >&2
      exit 1
      ;;
  esac
}
`

// selfUpgradeApplyChecks holds every check that runs before the first change, so
// the preflight Job and the apply Job cannot disagree about what is admissible.
const selfUpgradeApplyChecks = selfUpgradeRevisionImage + `repository="$DOOPS_UPGRADE_REPOSITORY"
tag="${DOOPS_UPGRADE_TAG:-}"
digest="${DOOPS_UPGRADE_DIGEST:-}"
target="$DOOPS_UPGRADE_IMAGE"
chart=` + selfUpgradeChartPath + `
previous_image="$(python3 - "$ns" "$workload" <<'PY'
import json, subprocess, sys
ns, workload = sys.argv[1:3]
doc = json.loads(subprocess.check_output(["kubectl", "-n", ns, "get", "deployment", workload, "-o", "json"]))
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
previous="$(helm history "$release" -n "$ns" -o json | python3 -c 'import json,sys; rows=json.load(sys.stdin); deployed=[int(row["revision"]) for row in rows if row["status"]=="deployed"]; print(max(deployed) if len(deployed)==1 else "")')"
test -n "$previous" || { echo "rollback_baseline_image_unresolved: the release must have exactly one deployed revision" >&2; exit 1; }
selector_json="$(kubectl -n "$ns" get deployment "$workload" -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(json.dumps(d["spec"]["selector"]["matchLabels"], separators=(",", ":"), sort_keys=True))')"
baseline="$(python3 - "$ns" "$previous_image" "$selector_json" <<'PY'
import json, re, subprocess, sys
ns, image, labels = sys.argv[1:4]
selector = ",".join(f"{key}={value}" for key, value in sorted(json.loads(labels).items()))
pods = json.loads(subprocess.check_output(
    ["kubectl", "-n", ns, "get", "pods", "-l", selector, "-o", "json"], timeout=30,
)).get("items") or []
pods = [pod for pod in pods if not (pod.get("metadata") or {}).get("deletionTimestamp")]
if len(pods) != 1:
    raise SystemExit("Rollback baseline requires one active Agent Pod")
pod = pods[0]
spec = pod.get("spec") or {}
container = next((item for item in spec.get("containers") or [] if item.get("name") == "doops-agent"), None)
status = next((item for item in (pod.get("status") or {}).get("containerStatuses") or [] if item.get("name") == "doops-agent"), None)
if not (pod.get("metadata") or {}).get("uid") or not spec.get("nodeName") or not container or container.get("image") != image or not status or not status.get("ready"):
    raise SystemExit("Rollback baseline Pod identity is not verified")
digest = str(status.get("imageID") or "").split("://")[-1].rsplit("@", 1)[-1]
if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
    raise SystemExit("Rollback baseline runtime digest is invalid")
print(json.dumps({"image": image, "digest": digest, "node": spec["nodeName"],
                 "runtime_class": spec.get("runtimeClassName") or "",
                 "service_account": spec.get("serviceAccountName") or "",
                 "tolerations": spec.get("tolerations") or [],
                 "node_selector": spec.get("nodeSelector") or {},
                 "image_pull_secrets": spec.get("imagePullSecrets") or []}))
PY
)"
export DOOPS_UPGRADE_BASELINE="$baseline"
verify_deployment "$previous_image" baseline ${verify_record-record}
rollback_baseline_image="$(revision_image "$previous")"
test "$rollback_baseline_image" = "$previous_image" || {
  echo "rollback_baseline_image_unresolved: the rollback revision image differs from the live Agent image" >&2
  exit 1
}
` + selfUpgradeRollbackBaselineProbe + `values="${TMPDIR:-/tmp}/doops-upgrade-values.json"
helm get values "$release" -n "$ns" -o json >"$values"
python3 - "$values" "$repository" "$tag" "$digest" "$selector_json" <<'PY'
import json, sys
path, repository, tag, digest, selector_json = sys.argv[1:6]
with open(path, encoding="utf-8") as fh:
    values = json.load(fh)
image = values.setdefault("image", {})
image.pop("tag", None)
image["repository"] = repository
image.pop("digest", None)
if digest:
    image["digest"] = digest
else:
    image["tag"] = tag
values.setdefault("workload", {})["selectorLabels"] = json.loads(selector_json)
with open(path, "w", encoding="utf-8") as fh:
    json.dump(values, fh)
PY
`

// The preflight Job starts from the target image, so a successful start already
// means the image was pulled to the node. It changes nothing: it only repeats the
// apply checks and asks the API server to dry-run the same Helm upgrade.
const selfUpgradePreflightScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + "verify_record=\n" + selfUpgradeApplyChecks + `helm upgrade "$release" "$chart" -n "$ns" -f "$values" --timeout 180s --wait=false --dry-run=server >/dev/null
echo "preflight-passed image=${target}"
`

// Adoption has no Helm release to dry-run yet; starting the Job still proves the pull.
const selfUpgradePullOnlyScript = `echo "preflight-passed image=${DOOPS_UPGRADE_IMAGE}"
`

const selfUpgradeApplyScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + selfUpgradeApplyChecks + `mutated=false
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
  if ! verify_deployment "$previous_image" baseline record; then
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
    if verify_deployment "$target" controller record; then
      echo "self-upgrade complete image=${target}"
      exit 0
    fi
  fi
  sleep 5
done
echo "new deployment did not stay ready" >&2
exit 1
`

const selfUpgradeRollbackScript = selfUpgradeKubeconfigPrelude + selfUpgradeDeploymentVerifier + selfUpgradeRevisionImage + `previous="$DOOPS_UPGRADE_REVISION"
python3 - "$ns" "$release" "$previous" <<'PY'
import json, subprocess, sys
ns, release, revision = sys.argv[1:4]
rows = json.loads(subprocess.check_output(["helm", "history", release, "-n", ns, "-o", "json"], timeout=30))
current = [int(row["revision"]) for row in rows if row["status"] == "deployed"]
if len(current) != 1 or not any(int(row["revision"]) == int(revision) < current[0] and row["status"] == "superseded" for row in rows):
    raise SystemExit("admitted rollback revision is no longer eligible")
PY
expected="$(revision_image "$previous")"
test "$expected" = "$DOOPS_UPGRADE_IMAGE" || { echo "rollback revision image changed after admission" >&2; exit 1; }
helm rollback "$release" "$previous" -n "$ns" --timeout 300s --wait
kubectl -n "$ns" rollout status "deployment/${workload}" --timeout 300s
verify_deployment "$expected" controller record
echo "self-upgrade rolled back to ${previous}"
`

// adoptionTarget names the release and workload for an Agent Deployment that has no
// Helm release yet. The release defaults to the Deployment name.
func adoptionTarget(requestedRelease, requestedWorkload string) (string, string, error) {
	workload := strings.TrimPrefix(strings.TrimSpace(requestedWorkload), "deployment/")
	if workload == "" {
		workload = selfUpgradeContainer
	}
	if strings.Contains(workload, "/") {
		return "", "", fmt.Errorf("adoption supports a Deployment workload only")
	}
	release := firstNonEmptyString(strings.TrimSpace(requestedRelease), workload)
	if err := validateK8sName("workload", workload, 63); err != nil {
		return "", "", err
	}
	if err := validateK8sName("release", release, 53); err != nil {
		return "", "", err
	}
	return release, workload, nil
}

const selfUpgradeAdoptScript = selfUpgradeKubeconfigPrelude + `python3 /app/adopt_agent.py run \
  --namespace "$ns" --workload "$workload" --release "$release" \
  --cluster "$DOOPS_GATEWAY_CLUSTER" --instance "$DOOPS_GATEWAY_INSTANCE" \
  --image "$DOOPS_UPGRADE_IMAGE"
echo "self-upgrade complete image=${DOOPS_UPGRADE_IMAGE}"
`
