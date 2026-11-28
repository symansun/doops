package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	selfUpgradeJobPattern = regexp.MustCompile(`^doops-self-upgrade-[a-f0-9]{24}-(preflight|apply|rollback)$`)
)

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

func selfUpgradeJobName(operationID, phase string) string {
	digest := sha256.Sum256([]byte(operationID))
	return "doops-self-upgrade-" + hex.EncodeToString(digest[:])[:24] + "-" + phase
}
