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

	buildrelease "github.com/user/doops/agent/pkg/release"
	"gopkg.in/yaml.v3"
)

// Self-upgrade is a Kubernetes Job, not a Helm command in the agent process.
// The Job uses the kubeconfig volume the agent already mounts, so the operator
// does not pass a kubeconfig. Chart handoff stays maxSurge=1 and maxUnavailable=0.
// If the new Pod does not stay ready, the Job itself rolls back; that controller
// is not the process the rollout deletes.

const (
	selfUpgradeLabelKey    = "app.kubernetes.io/name"
	selfUpgradeLabelValue  = "doops-self-upgrade"
	selfUpgradeChartPrefix = "doops-agent"
	selfUpgradeContainer   = "doops-agent"
	selfUpgradeChartPath   = "/app/deploy/helm/doops-agent"
)

var errNoManagedRelease = errors.New("no managed doops-agent Helm release")

var (
	imageRepositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	imageTagPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	imageDigestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	selfUpgradeJobPattern  = regexp.MustCompile(`^doops-self-upgrade-[a-f0-9]{24}-(preflight|apply|rollback)$`)
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

func upgradeAgent(ctx context.Context, args agentUpgradeParams, proc upgradeProcess) (text string, err error) {
	defer func() {
		if err != nil && selfUpgradeFailureCategory(err.Error()) == "" {
			err = fmt.Errorf("category=upgrade_%s_failed %w", agentUpgradePhase(args), err)
		}
	}()
	if err := validateAgentUpgradeDispatch(args); err != nil {
		return "", err
	}
	if err := validateUpgradeEvidenceParams(args); err != nil {
		return "", err
	}
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
	phase := agentUpgradePhase(args)
	jobName := selfUpgradeJobName(args.UpgradeOperationID, phase)
	if err := p.requireNoActiveUpgrade(ctx, namespace); err != nil {
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
		evidence:                     args,
		phase:                        phase,
	})
	if err != nil {
		return "", err
	}
	if err := p.applyJob(ctx, namespace, manifest); err != nil {
		return "", fmt.Errorf("%w job=%s namespace=%s", err, jobName, namespace)
	}
	return fmt.Sprintf(
		"self-upgrade-accepted job=%s namespace=%s release=%s workload=%s image=%s digest=%s expected_release_commit=%s upgrade_operation_id=%s phase=%s",
		jobName, namespace, release, workload, strings.TrimSpace(args.Image), digest, args.ExpectedReleaseCommit, args.UpgradeOperationID, phase,
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
	jobName := selfUpgradeJobName(args.UpgradeOperationID, "rollback")
	rollbackImage, revision, commit, err := p.rollbackImage(ctx, namespace, release, workload)
	if err != nil {
		return "", err
	}
	repository, digest, err := parsePinnedImage(rollbackImage)
	if err != nil {
		return "", err
	}
	if err := p.requireNoActiveUpgrade(ctx, namespace); err != nil {
		return "", err
	}
	args.ExpectedReleaseCommit = commit
	manifest, err := selfUpgradeJobManifest(jobName, namespace, pod, selfUpgradePlan{
		image:           rollbackImage,
		controllerImage: pod.image,
		repository:      repository,
		digest:          digest,
		release:         release,
		workload:        workload,
		revision:        revision,
		script:          selfUpgradeRollbackScript,
		evidence:        args,
		phase:           "rollback",
	})
	if err != nil {
		return "", err
	}
	if err := p.applyJob(ctx, namespace, manifest); err != nil {
		return "", fmt.Errorf("%w job=%s namespace=%s", err, jobName, namespace)
	}
	return fmt.Sprintf(
		"self-upgrade-accepted job=%s namespace=%s release=%s workload=%s image=%s digest=%s expected_release_commit=%s upgrade_operation_id=%s phase=rollback",
		jobName, namespace, release, workload, rollbackImage, digest, commit, args.UpgradeOperationID,
	), nil
}

func (p upgradeProcess) rollbackImage(ctx context.Context, namespace, release, workload string) (string, int, string, error) {
	out, err := p.command(ctx, "", "helm", "history", release, "-n", namespace, "-o", "json")
	if err != nil {
		return "", 0, "", commandError("read rollback history", out, err)
	}
	var history []struct {
		Revision int    `json:"revision"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(out, &history); err != nil {
		return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid parse rollback history: %w", err)
	}
	out, err = p.command(ctx, "", "kubectl", "-n", namespace, "get", "configmap", "doops-upgrade-verified-"+release, "--ignore-not-found", "-o", "json")
	if err != nil {
		return "", 0, "", commandError("read verified rollback evidence", out, err)
	}
	var stored struct {
		Data map[string]string `json:"data"`
	}
	var evidence struct {
		SchemaVersion int    `json:"schemaVersion"`
		Release       string `json:"release"`
		Workload      string `json:"workload"`
		Revisions     map[string]struct {
			Image         string `json:"image"`
			ReleaseCommit string `json:"release_commit"`
			Outcome       string `json:"outcome"`
			OperationID   string `json:"upgrade_operation_id"`
			Job           string `json:"job"`
			Phase         string `json:"phase"`
			Stage         string `json:"verification_stage"`
		} `json:"revisions"`
	}
	if json.Unmarshal(out, &stored) != nil || json.Unmarshal([]byte(stored.Data["evidence.json"]), &evidence) != nil ||
		evidence.SchemaVersion != 1 || evidence.Release != release || evidence.Workload != workload {
		return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid verified rollback evidence is missing or invalid; use the authorized external controller")
	}
	current, previous := 0, 0
	for _, row := range history {
		if row.Status == "deployed" && row.Revision > current {
			current = row.Revision
		}
	}
	for _, row := range history {
		entry := evidence.Revisions[fmt.Sprint(row.Revision)]
		if row.Status != "superseded" || row.Revision >= current || row.Revision <= previous || entry.Image == "" {
			continue
		}
		// Upgrade candidates may have live verification before the controller
		// commits its terminal outcome. A failed apply cannot certify that revision.
		if entry.Phase == "apply" && entry.Stage != "baseline" && entry.Stage != "rollback" {
			if entry.OperationID == "" || entry.Job != selfUpgradeJobName(entry.OperationID, "apply") {
				continue
			}
			receipt, err := p.readUpgradeReceipt(ctx, namespace, entry.Job, entry.OperationID)
			if err != nil || receipt["outcome"] != "succeeded" || receipt["release"] != release || receipt["workload"] != workload ||
				receipt["revision"] != row.Revision || receipt["verified_image"] != entry.Image || receipt["release_commit"] != entry.ReleaseCommit {
				continue
			}
		}
		previous = row.Revision
	}
	if previous == 0 {
		return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid no previously verified superseded revision")
	}
	verified := evidence.Revisions[fmt.Sprint(previous)]
	if !buildrelease.ValidCommit(verified.ReleaseCommit) || verified.Outcome != "succeeded" {
		return "", 0, "", fmt.Errorf("category=rollback_commit_unknown historical revision %d has no verified release_commit", previous)
	}
	out, err = p.command(ctx, "", "helm", "get", "manifest", release, "-n", namespace, "--revision", fmt.Sprint(previous))
	if err != nil {
		return "", 0, "", commandError("read rollback manifest", out, err)
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
			return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid parse rollback manifest: %w", err)
		}
		if doc.Kind != "Deployment" || doc.Metadata.Name != workload {
			continue
		}
		if doc.Metadata.Namespace != "" && doc.Metadata.Namespace != namespace {
			return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid rollback workload namespace mismatch")
		}
		for _, container := range doc.Spec.Template.Spec.Containers {
			if container.Name == selfUpgradeContainer {
				images = append(images, container.Image)
			}
		}
	}
	if len(images) != 1 {
		return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid rollback manifest must contain exactly one Agent image")
	}
	if _, _, err := parsePinnedImage(images[0]); err != nil {
		return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid rollback requires an immutable historical image: %w", err)
	}
	if evidence.Revisions[fmt.Sprint(previous)].Image != images[0] {
		return "", 0, "", fmt.Errorf("category=rollback_evidence_invalid rollback manifest does not match verified evidence")
	}
	return images[0], previous, verified.ReleaseCommit, nil
}

func (p upgradeProcess) observeSelfUpgrade(ctx context.Context, args agentUpgradeParams) (string, error) {
	jobName := strings.TrimSpace(args.ObserveJob)
	if !selfUpgradeJobPattern.MatchString(jobName) {
		return "", fmt.Errorf("category=invalid_upgrade_request observe_job must name a doops self-upgrade job")
	}
	namespace, err := p.agentNamespace(args.Namespace)
	if err != nil {
		return "", err
	}
	job, found, err := p.readUpgradeJob(ctx, namespace, jobName)
	if err != nil {
		return "", err
	}
	if found && (job.Metadata.Annotations["doops.sh/upgrade-operation-id"] != args.UpgradeOperationID ||
		selfUpgradeJobName(args.UpgradeOperationID, job.Metadata.Annotations["doops.sh/phase"]) != jobName) {
		return "", fmt.Errorf("category=upgrade_operation_mismatch Job does not belong to upgrade_operation_id")
	}
	header := fmt.Sprintf("job=%s namespace=%s phase=observe upgrade_operation_id=%s", jobName, namespace, args.UpgradeOperationID)
	for _, field := range []string{"release", "image", "digest", "expected_release_commit", "phase"} {
		value := job.Metadata.Annotations["doops.sh/"+strings.ReplaceAll(field, "_", "-")]
		if field == "phase" {
			field = "job_phase"
		}
		if value != "" {
			header += " " + field + "=" + value
		}
	}
	if found && job.phase() == "running" {
		return fmt.Sprintf("self-upgrade-running %s", header), nil
	}
	receipt, receiptErr := p.readUpgradeReceipt(ctx, namespace, jobName, args.UpgradeOperationID)
	if receiptErr == nil {
		if found && receipt["outcome"] != job.phase() {
			return "", fmt.Errorf("category=upgrade_receipt_conflict Job and durable receipt disagree")
		}
		for _, key := range []string{"release", "workload", "image", "expected_release_commit"} {
			expected := job.Metadata.Annotations["doops.sh/"+strings.ReplaceAll(key, "_", "-")]
			if found && expected != "" && receipt[key] != expected {
				return "", fmt.Errorf("category=upgrade_receipt_conflict Job and receipt identity disagree")
			}
		}
		evidence, _ := json.Marshal(receipt)
		return fmt.Sprintf("self-upgrade-%s %s\n%s", receipt["outcome"], header, evidence), nil
	}
	if !found || job.phase() == "succeeded" {
		return "", receiptErr
	}
	// A deadline or killed controller may never have written a receipt. Preserve
	// that explicit evidence gap; arbitrary command/Pod output is not audit data.
	category := firstNonEmptyString(selfUpgradeFailureCategory(p.jobLogs(ctx, namespace, jobName)), "upgrade_job_failed")
	for _, condition := range job.Status.Conditions {
		if condition.Status == "True" && condition.Reason == "DeadlineExceeded" {
			category = "upgrade_job_deadline_exceeded"
		}
	}
	evidence, _ := json.Marshal(map[string]string{
		"category": category, "receipt_error": selfUpgradeFailureCategory(receiptErr.Error()),
	})
	return fmt.Sprintf("self-upgrade-failed %s category=%s\n%s", header, category, evidence), nil
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

func (p upgradeProcess) requireNoActiveUpgrade(ctx context.Context, namespace string) error {
	active, err := p.activeSelfUpgrade(ctx, namespace)
	if err != nil {
		return err
	}
	if active != "" {
		return fmt.Errorf("category=upgrade_busy self-upgrade job %s is already running", active)
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
			Status selfUpgradeJobStatus `json:"status"`
		} `json:"items"`
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return "", nil
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return "", fmt.Errorf("parse self-upgrade jobs: %w", err)
	}
	for _, item := range listed.Items {
		if (&selfUpgradeJob{Status: item.Status}).phase() == "running" {
			return item.Metadata.Name, nil
		}
	}
	return "", nil
}

type selfUpgradeJobStatus struct {
	Active     int `json:"active"`
	Succeeded  int `json:"succeeded"`
	Failed     int `json:"failed"`
	Conditions []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"conditions"`
}

type selfUpgradeJob struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Status selfUpgradeJobStatus `json:"status"`
}

func (job *selfUpgradeJob) phase() string {
	for _, condition := range job.Status.Conditions {
		if condition.Status == "True" {
			if condition.Type == "Failed" {
				return "failed"
			}
			if condition.Type == "Complete" {
				return "succeeded"
			}
		}
	}
	switch {
	case job.Status.Succeeded > 0:
		return "succeeded"
	case job.Status.Failed > 0:
		return "failed"
	default:
		return "running"
	}
}

func (p upgradeProcess) readUpgradeJob(ctx context.Context, namespace, jobName string) (selfUpgradeJob, bool, error) {
	var job selfUpgradeJob
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "get", "job", jobName, "-o", "json")
	if err != nil {
		if isNotFound(out, err) {
			return job, false, nil
		}
		return job, false, commandError("read self-upgrade job", out, err)
	}
	if err := json.Unmarshal(out, &job); err != nil {
		return job, false, fmt.Errorf("category=upgrade_job_invalid parse self-upgrade job: %w", err)
	}
	return job, true, nil
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
	out, err := p.command(ctx, string(manifest), "kubectl", "-n", namespace, "create", "-f", "-")
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "alreadyexists") || strings.Contains(strings.ToLower(string(out)), "already exists") {
			return fmt.Errorf("category=upgrade_job_exists this operation phase already has a Job; observe the existing Job")
		}
		return fmt.Errorf("category=upgrade_job_create_unknown %w", commandError("create self-upgrade job", out, err))
	}
	return nil
}

type selfUpgradePlan struct {
	image           string
	controllerImage string
	repository      string
	tag             string
	digest          string
	release         string
	workload        string
	revision        int
	script          string
	cluster         string
	instance        string
	preflight       bool
	evidence        agentUpgradeParams
	phase           string
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
	image := firstNonEmptyString(plan.controllerImage, plan.image)
	if image == "" {
		image = pod.image
	}
	env := []map[string]string{
		{"name": "DOOPS_UPGRADE_NAMESPACE", "value": namespace},
		{"name": "DOOPS_UPGRADE_RELEASE", "value": plan.release},
		{"name": "DOOPS_UPGRADE_WORKLOAD", "value": plan.workload},
	}
	annotations := map[string]string{
		"doops.sh/release": plan.release, "doops.sh/workload": plan.workload,
		"doops.sh/image": plan.image, "doops.sh/digest": plan.digest,
		"doops.sh/upgrade-operation-id":    plan.evidence.UpgradeOperationID,
		"doops.sh/expected-release-commit": plan.evidence.ExpectedReleaseCommit,
		"doops.sh/source-commit":           plan.evidence.SourceCommit, "doops.sh/ci-url": plan.evidence.CIURL,
		"doops.sh/requester-id": plan.evidence.RequesterID, "doops.sh/audit-id": fmt.Sprint(plan.evidence.AuditID),
		"doops.sh/session-id": plan.evidence.SessionID, "doops.sh/phase": plan.phase,
		"doops.sh/created-at":                      time.Now().UTC().Format(time.RFC3339Nano),
		"doops.sh/job":                             jobName,
		"doops.sh/allow-missing-rollback-baseline": fmt.Sprint(plan.allowMissingRollbackBaseline),
	}
	for _, key := range []string{"upgrade-operation-id", "expected-release-commit", "source-commit", "ci-url",
		"requester-id", "audit-id", "session-id", "phase", "created-at", "job"} {
		name := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		if key == "upgrade-operation-id" {
			name = "OPERATION_ID"
		}
		env = append(env, map[string]string{
			"name":  "DOOPS_UPGRADE_" + name,
			"value": annotations["doops.sh/"+key],
		})
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
	if strings.Contains(image, "@sha256:") {
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
			"annotations": annotations,
		},
		"spec": map[string]interface{}{
			"backoffLimit":            0,
			"ttlSecondsAfterFinished": 7 * 24 * 60 * 60,
			"activeDeadlineSeconds":   1200,
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": annotations,
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

func selfUpgradeJobName(operationID, phase string) string {
	digest := sha256.Sum256([]byte(operationID))
	return "doops-self-upgrade-" + hex.EncodeToString(digest[:])[:24] + "-" + phase
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

func commandError(action string, _ []byte, _ error) error {
	// Kubernetes/Helm diagnostics can echo submitted objects and credentials.
	return fmt.Errorf("%s failed", action)
}

func tailText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return text
	}
	return utf8Tail(text, limit)
}

// The stable categories an upgrade preflight can report. They name why an
// upgrade was refused, so an operator reads the reason instead of guessing
// whether the Agent or the release is broken.
const (
	selfUpgradeCategoryRollbackBaselineUnavailable   = "rollback_baseline_image_unavailable"
	selfUpgradeCategoryRollbackBaselineUnresolved    = "rollback_baseline_image_unresolved"
	selfUpgradeCategoryRollbackBaselineProbeFailed   = "rollback_baseline_probe_failed"
	selfUpgradeCategoryStaleVolumeMountsUnresolved   = "stale_volume_mounts_unresolved"
	selfUpgradeCategoryRBACControllerIdentityChanged = "rbac_controller_identity_changed"
)

var selfUpgradeFailureCategories = []string{
	selfUpgradeCategoryRollbackBaselineUnavailable,
	selfUpgradeCategoryRollbackBaselineUnresolved,
	selfUpgradeCategoryRollbackBaselineProbeFailed,
	selfUpgradeCategoryStaleVolumeMountsUnresolved,
	selfUpgradeCategoryRBACControllerIdentityChanged,
}

// selfUpgradeFailureCategory reports the stable category a failed upgrade Job
// carried, or "" when the failure has no category. The category is part of the
// Job log, so it survives the Job, the observation and the CLI report.
func selfUpgradeFailureCategory(logs string) string {
	matches := upgradeCategoryPattern.FindAllStringSubmatch(logs, -1)
	if len(matches) > 0 {
		return matches[len(matches)-1][1]
	}
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

// selfUpgradeRollbackBaselineProbe proves that the image an automatic rollback
// would restore is still in its registry.
//
// It reads the baseline image manifest with skopeo, authenticated with the
// Agent Pod's own imagePullSecrets, which are the credentials the kubelet uses
// for the rollback Pod. An Agent Pod without pull secrets is checked
// anonymously, as the kubelet would pull it. No new credential dimension and no
// new mount: the Secret is read into a 0600 file that only skopeo reads, and the
// payload never reaches an argument or the Job log.
//
// It asks the registry instead of the kubelet on purpose. A kubelet pull waits
// behind every other pull on the node, so on a busy node it can take minutes
// and proves nothing more about whether the manifest is gone.
//
// A manifest that is gone from the registry is therefore caught before the
// release changes, which is what makes the third upgrade guarantee (restore the
// backup when verification fails) real rather than aspirational. A refused
// credential or an unreachable registry is not reported as a missing image.
//
// The operator can still proceed on purpose with
// DOOPS_UPGRADE_ALLOW_MISSING_ROLLBACK_BASELINE=1; the default is fail closed.

// selfUpgradeApplyChecks holds every check that runs before the first change, so
// the preflight Job and the apply Job cannot disagree about what is admissible.

// A ServiceAccount controller cannot let Helm add, remove or rebind its own
// identity: a failed upgrade's rollback could delete the newly managed account
// or binding, and a rollback across that adoption point could do the same.
// Those transitions need a controller with an independent Kubernetes identity.
// A mounted kubeconfig may also authenticate as a ServiceAccount, so the API
// server's effective identity, not the configuration source, selects the check.

// selfUpgradeStaleVolumeMounts finds the volume mounts the Helm upgrade would
// leave pointing at a volume it deletes.
//
// Helm removes only what its own previous revision rendered. A volume the
// previous revision rendered and the new chart drops is deleted, but a mount of
// that volume added outside Helm (for example by the kubectl apply that first
// created the Agent) is not Helm's to remove. The API server then rejects the
// Deployment, because a mount names a volume that no longer exists.
//
// Only mounts of exactly those volumes are stale: a volume the previous
// revision never rendered is not Helm's either, so Helm keeps it and its mount
// stays valid. The check reads the Deployment snapshot the other checks
// already took and changes nothing; the apply Job removes the mounts it lists,
// after backing them up, and restores them if the upgrade is rolled back.

// selfUpgradeRestoreVolumeMounts puts back the stale mounts the apply Job
// removed, once Helm has rolled the release back and the volume exists again.
// It reads the live Deployment and adds only the mounts that are missing, so it
// is safe to run whether or not the removal itself went through.

// The preflight Job starts from the target image, so a successful start already
// means the image was pulled to the node. It changes nothing: it only repeats the
// apply checks and asks the API server to dry-run the same Helm upgrade.

// Adoption has no Helm release to dry-run yet; starting the Job still proves the pull.

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
