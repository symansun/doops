package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	buildrelease "github.com/user/doops/agent/pkg/release"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
