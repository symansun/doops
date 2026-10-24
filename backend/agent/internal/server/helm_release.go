package server

// Helm release pipeline: preflight -> mutate -> verify.
//
//	preflight  helm upgrade --dry-run=server renders the release (manifest plus
//	           hooks); the rendered workloads and optional expected_image are
//	           checked before anything changes.
//	mutate     the real helm command; upgrade reports the new revision.
//	verify     the release manifest of that revision is read back and every
//	           rendered workload is compared with the live cluster object.
//
// Every failure is one stable code, then ": " and the concrete reason:
//
//	helm_preflight_failed         helm dry-run itself failed (first stderr line)
//	helm_manifest_invalid         rendered manifest cannot be decoded or is malformed
//	helm_no_workload              no workload the verifier can check was rendered
//	helm_image_mismatch           expected_image is in no rendered container
//	helm_mutation_failed          the real helm command failed (first stderr line)
//	helm_mutation_invalid_result  helm output has no revision for this mutation
//	helm_verification_failed      live state does not match the rendered release
//
// Only rollback and uninstall skip preflight and verify.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var (
	errHelmManifestInvalid = errors.New("helm_manifest_invalid")
	errHelmNoWorkload      = errors.New("helm_no_workload: rendered release contains no supported workload")
	errHelmImageMismatch   = errors.New("helm_image_mismatch: rendered release does not contain expected image")
)

// helmStepError carries a stable code plus the reason a step failed.
type helmStepError struct {
	code   string
	reason string
}

func (e *helmStepError) Error() string {
	if e.reason == "" {
		return e.code
	}
	return e.code + ": " + e.reason
}

func helmStepFailure(code, reason string) error {
	return &helmStepError{code: code, reason: reason}
}

// renderedWorkloadFailure keeps the code chosen by the manifest checks.
func renderedWorkloadFailure(err error) error {
	return helmStepFailure(helmRenderedErrorCode(err), helmRenderedErrorReason(err))
}

func helmRenderedErrorCode(err error) string {
	switch {
	case errors.Is(err, errHelmImageMismatch):
		return "helm_image_mismatch"
	case errors.Is(err, errHelmNoWorkload):
		return "helm_no_workload"
	default:
		return "helm_manifest_invalid"
	}
}

func helmRenderedErrorReason(err error) string {
	reason := err.Error()
	for _, code := range []string{"helm_image_mismatch: ", "helm_no_workload: ", "helm_manifest_invalid: "} {
		reason = strings.TrimPrefix(reason, code)
	}
	return redactSecretText(reason)
}

// helmStderrReason keeps the first line of Helm's error. Chart-controlled
// stdout (NOTES, rendered manifests) is never returned; stderr is redacted and
// capped because a template error can quote a value.
func helmStderrReason(stderr []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "Error: ")
		line = redactSecretText(line)
		if utf8.RuneCountInString(line) > 300 {
			line = string([]rune(line)[:300]) + "..."
		}
		return line
	}
	return "helm exited without an error message"
}

func runHelmCommand(ctx context.Context, command helmCommand, push notificationSender) (string, error) {
	if command.operation() == "upgrade" {
		if err := preflightHelmUpgrade(ctx, command); err != nil {
			return "", err
		}
	}
	output, err := mutateHelmRelease(ctx, command)
	if err != nil {
		return "", err
	}
	if command.operation() != "upgrade" {
		return "", nil
	}
	revision, err := parseHelmReleaseOutput(output, command.mutationID)
	if err != nil {
		return "", helmStepFailure("helm_mutation_invalid_result", err.Error())
	}
	command.revision = revision
	verified, err := verifyHelmDeploymentState(ctx, command)
	if err != nil {
		return "", helmStepFailure("helm_verification_failed", redactSecretText(err.Error()))
	}
	return verified, nil
}

// preflightHelmUpgrade renders the upgrade server-side, so lookups behave as in
// the real run, and checks the rendered workloads before Helm creates a revision.
func preflightHelmUpgrade(ctx context.Context, command helmCommand) error {
	args := append(append([]string(nil), command.args...), "--dry-run=server", "--output=json")
	preflight := exec.CommandContext(ctx, "helm", args...)
	preflight.Dir = command.cwd
	var stdout, stderr bytes.Buffer
	preflight.Stdout = &stdout
	preflight.Stderr = &stderr
	if err := preflight.Run(); err != nil {
		return helmStepFailure("helm_preflight_failed", helmStderrReason(stderr.Bytes()))
	}
	var rendered struct {
		Manifest string `json:"manifest"`
		Hooks    []struct {
			Manifest string `json:"manifest"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rendered); err != nil {
		return helmStepFailure("helm_manifest_invalid", "helm dry-run output is not JSON")
	}
	manifest := appendHelmHookManifests([]byte(rendered.Manifest), rendered.Hooks)
	if _, err := validateHelmRenderedImages(manifest, command.expectedImage); err != nil {
		return renderedWorkloadFailure(err)
	}
	return nil
}

func mutateHelmRelease(ctx context.Context, command helmCommand) ([]byte, error) {
	args := append([]string(nil), command.args...)
	if command.operation() == "upgrade" {
		args = append(args, "--output=json")
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Dir = command.cwd
	// stdout carries chart output (NOTES, manifests) and is only parsed for the
	// revision; it is never returned to the caller.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, helmStepFailure("helm_mutation_failed", helmStderrReason(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func (c helmCommand) operation() string {
	if len(c.args) == 0 {
		return ""
	}
	return c.args[0]
}

func parseHelmReleaseOutput(output []byte, expectedDescription string) (int, error) {
	var release struct {
		Version int `json:"version"`
		Info    struct {
			Description string `json:"description"`
		} `json:"info"`
	}
	if err := json.Unmarshal(output, &release); err != nil {
		return 0, fmt.Errorf("helm release output is not JSON")
	}
	if release.Version <= 0 {
		return 0, fmt.Errorf("helm release output has no positive revision")
	}
	if release.Info.Description != expectedDescription {
		return 0, fmt.Errorf("helm release output description does not match mutation")
	}
	return release.Version, nil
}

// ---- rendered workloads ----

// helmWorkloadKinds maps a rendered kind to its canonical name. Ordinary Jobs
// are not release workloads; only pre-install/pre-upgrade hook Jobs are.
var helmWorkloadKinds = map[string]string{
	"deployment":  "Deployment",
	"statefulset": "StatefulSet",
	"daemonset":   "DaemonSet",
	"cronjob":     "CronJob",
	"job":         "Job",
}

type renderedHelmWorkload struct {
	Kind string
	Name string
	// Namespace is the rendered metadata.namespace; empty means the release namespace.
	Namespace string
	// DeletedOnSuccess marks a hook Job Helm deletes once it succeeds
	// (helm.sh/hook-delete-policy: hook-succeeded). Helm only proceeds past a
	// successful hook, so a missing object is the success evidence.
	DeletedOnSuccess bool
	// Containers lists init containers first, then containers, in spec order.
	Containers []string
}

type helmPodTemplate struct {
	Spec struct {
		InitContainers []struct {
			Image string `yaml:"image" json:"image"`
		} `yaml:"initContainers" json:"initContainers"`
		Containers []struct {
			Image string `yaml:"image" json:"image"`
		} `yaml:"containers" json:"containers"`
	} `yaml:"spec" json:"spec"`
}

func (t helmPodTemplate) images() []string {
	images := make([]string, 0, len(t.Spec.InitContainers)+len(t.Spec.Containers))
	for _, container := range t.Spec.InitContainers {
		images = append(images, strings.TrimSpace(container.Image))
	}
	for _, container := range t.Spec.Containers {
		images = append(images, strings.TrimSpace(container.Image))
	}
	return images
}

type helmWorkloadSpec struct {
	Template    helmPodTemplate `yaml:"template" json:"template"`
	JobTemplate struct {
		Spec struct {
			Template helmPodTemplate `yaml:"template" json:"template"`
		} `yaml:"spec" json:"spec"`
	} `yaml:"jobTemplate" json:"jobTemplate"`
}

func (s helmWorkloadSpec) podTemplate(kind string) helmPodTemplate {
	if kind == "CronJob" {
		return s.JobTemplate.Spec.Template
	}
	return s.Template
}

func appendHelmHookManifests(manifest []byte, hooks []struct {
	Manifest string `json:"manifest"`
}) []byte {
	result := append([]byte(nil), manifest...)
	for _, hook := range hooks {
		if strings.TrimSpace(hook.Manifest) == "" {
			continue
		}
		// Helm's JSON hook manifests carry no leading document separator; without
		// one the hook merges into the previous document and decoding fails.
		result = append(result, "\n---\n"...)
		result = append(result, hook.Manifest...)
	}
	return result
}

// validateHelmRenderedImages returns the rendered workloads and, when
// expectedImage is set, requires it in at least one container.
func validateHelmRenderedImages(manifest []byte, expectedImage string) ([]renderedHelmWorkload, error) {
	workloads, err := parseRenderedHelmWorkloads(manifest)
	if err != nil {
		return nil, err
	}
	if len(workloads) == 0 {
		return nil, errHelmNoWorkload
	}
	if expectedImage == "" {
		return workloads, nil
	}
	rendered := make([]string, 0, len(workloads))
	for _, workload := range workloads {
		for _, image := range workload.Containers {
			if image == expectedImage {
				return workloads, nil
			}
		}
		rendered = append(rendered, workload.Kind+"/"+workload.Name+"="+strings.Join(workload.Containers, ","))
	}
	return nil, fmt.Errorf("%w %q; rendered %s", errHelmImageMismatch, expectedImage, strings.Join(rendered, " "))
}

func parseRenderedHelmWorkloads(manifest []byte) ([]renderedHelmWorkload, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	var workloads []renderedHelmWorkload
	seen := make(map[string]struct{})
	for index := 1; ; index++ {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name        string            `yaml:"name"`
				Namespace   string            `yaml:"namespace"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
			Spec helmWorkloadSpec `yaml:"spec"`
		}
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("%w: YAML document %d: %v", errHelmManifestInvalid, index, err)
		}
		rawKind := strings.TrimSpace(document.Kind)
		if strings.EqualFold(rawKind, "List") {
			return nil, fmt.Errorf("%w: List wrappers are not supported", errHelmManifestInvalid)
		}
		kind, ok := helmWorkloadKinds[strings.ToLower(rawKind)]
		if !ok || (kind == "Job" && !isHelmUpgradeHook(document.Metadata.Annotations)) {
			continue
		}
		name := strings.TrimSpace(document.Metadata.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: %s without metadata.name", errHelmManifestInvalid, kind)
		}
		namespace := strings.TrimSpace(document.Metadata.Namespace)
		key := kind + "/" + name
		if namespace != "" {
			key = kind + "/" + namespace + "/" + name
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate %s", errHelmManifestInvalid, key)
		}
		seen[key] = struct{}{}
		images := document.Spec.podTemplate(kind).images()
		if len(images) == 0 {
			return nil, fmt.Errorf("%w: %s has no containers", errHelmManifestInvalid, key)
		}
		for _, image := range images {
			if image == "" {
				return nil, fmt.Errorf("%w: %s has a container without an image", errHelmManifestInvalid, key)
			}
		}
		workloads = append(workloads, renderedHelmWorkload{
			Kind: kind, Name: name, Namespace: namespace, Containers: images,
			DeletedOnSuccess: kind == "Job" && hasHelmDeletePolicy(document.Metadata.Annotations, "hook-succeeded"),
		})
	}
	return workloads, nil
}

func hasHelmDeletePolicy(annotations map[string]string, policy string) bool {
	for _, value := range strings.Split(annotations["helm.sh/hook-delete-policy"], ",") {
		if strings.TrimSpace(value) == policy {
			return true
		}
	}
	return false
}

func isHelmUpgradeHook(annotations map[string]string) bool {
	for _, hook := range strings.Split(annotations["helm.sh/hook"], ",") {
		switch strings.TrimSpace(hook) {
		case "pre-install", "pre-upgrade":
			return true
		}
	}
	return false
}

// ---- live verification ----

type liveHelmWorkload struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		helmWorkloadSpec
		Replicas       *int32 `json:"replicas"`
		UpdateStrategy struct {
			Type string `json:"type"`
		} `json:"updateStrategy"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration int64 `json:"observedGeneration"`
		// Deployment and StatefulSet.
		UpdatedReplicas     int32  `json:"updatedReplicas"`
		ReadyReplicas       int32  `json:"readyReplicas"`
		AvailableReplicas   int32  `json:"availableReplicas"`
		UnavailableReplicas *int32 `json:"unavailableReplicas"`
		// DaemonSet.
		DesiredNumberScheduled int32 `json:"desiredNumberScheduled"`
		UpdatedNumberScheduled int32 `json:"updatedNumberScheduled"`
		NumberReady            int32 `json:"numberReady"`
		NumberAvailable        int32 `json:"numberAvailable"`
		NumberUnavailable      int32 `json:"numberUnavailable"`
		// Job.
		Active    int32 `json:"active"`
		Succeeded int32 `json:"succeeded"`
		Failed    int32 `json:"failed"`
	} `json:"status"`
}

// verifyHelmDeploymentState reads back the manifest of the mutation's revision
// and checks each rendered workload against the live object: same images, and
// rolled out (Ready, or complete for hook Jobs).
func verifyHelmDeploymentState(ctx context.Context, command helmCommand) (string, error) {
	if command.mutationID != "" && command.revision <= 0 {
		return "", fmt.Errorf("mutation revision is unavailable")
	}
	workloads, err := readReleaseWorkloads(ctx, command)
	if err != nil {
		return "", err
	}
	for _, expected := range workloads {
		if err := verifyLiveWorkload(ctx, command, expected); err != nil {
			return "", err
		}
	}
	if command.mutationID != "" {
		statusCmd := exec.CommandContext(ctx, "helm", "status", command.release, "--namespace", command.namespace, "--output", "json")
		statusCmd.Dir = command.cwd
		statusBytes, err := statusCmd.Output()
		if err != nil {
			return "", fmt.Errorf("read current release status: %w", err)
		}
		currentRevision, err := parseHelmReleaseOutput(statusBytes, command.mutationID)
		if err != nil || currentRevision != command.revision {
			return "", fmt.Errorf("release was changed by another mutation")
		}
	}
	return fmt.Sprintf("final Helm workload spec verified: %d workload(s) observed and Ready or complete", len(workloads)), nil
}

// readReleaseWorkloads loads the release manifest; a hook-only release has its
// workloads in the hooks.
func readReleaseWorkloads(ctx context.Context, command helmCommand) ([]renderedHelmWorkload, error) {
	args := []string{"get", "manifest", command.release, "--namespace", command.namespace}
	if command.revision > 0 {
		args = append(args, "--revision", fmt.Sprintf("%d", command.revision))
	}
	manifestCmd := exec.CommandContext(ctx, "helm", args...)
	manifestCmd.Dir = command.cwd
	manifest, err := manifestCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read final release manifest: %w", err)
	}
	workloads, err := validateHelmRenderedImages(manifest, command.expectedImage)
	if errors.Is(err, errHelmNoWorkload) {
		hookArgs := []string{"get", "hooks", command.release, "--namespace", command.namespace}
		if command.revision > 0 {
			hookArgs = append(hookArgs, "--revision", fmt.Sprintf("%d", command.revision))
		}
		hookCmd := exec.CommandContext(ctx, "helm", hookArgs...)
		hookCmd.Dir = command.cwd
		if hooks, hookErr := hookCmd.Output(); hookErr == nil {
			workloads, err = validateHelmRenderedImages(append(append(manifest, "\n---\n"...), hooks...), command.expectedImage)
		}
	}
	if err != nil {
		return nil, errors.New(helmRenderedErrorReason(err))
	}
	return workloads, nil
}

func verifyLiveWorkload(ctx context.Context, command helmCommand, expected renderedHelmWorkload) error {
	// A chart may place a workload outside the release namespace.
	namespace := expected.Namespace
	if namespace == "" {
		namespace = command.namespace
	}
	ref := fmt.Sprintf("%s %s/%s", expected.Kind, namespace, expected.Name)
	getCmd := exec.CommandContext(ctx, "kubectl", "get", strings.ToLower(expected.Kind), expected.Name,
		"--namespace", namespace, "--output", "json")
	getCmd.Dir = command.cwd
	liveBytes, err := getCmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if expected.DeletedOnSuccess && errors.As(err, &exitErr) && strings.Contains(string(exitErr.Stderr), "NotFound") {
			return nil
		}
		return fmt.Errorf("read final %s: %w", ref, err)
	}
	var live liveHelmWorkload
	if err := json.Unmarshal(liveBytes, &live); err != nil {
		return fmt.Errorf("decode final %s: %w", ref, err)
	}
	if err := liveWorkloadRolledOut(expected.Kind, live); err != nil {
		return fmt.Errorf("%s %s", ref, err)
	}
	actual := live.Spec.podTemplate(expected.Kind).images()
	if !sameStringList(expected.Containers, actual) {
		return fmt.Errorf("%s spec images=%q rendered=%q", ref, actual, expected.Containers)
	}
	return nil
}

func liveWorkloadRolledOut(kind string, live liveHelmWorkload) error {
	status := live.Status
	if kind == "Job" {
		if status.Succeeded != 1 || status.Failed != 0 || status.Active != 0 {
			return fmt.Errorf("is not complete (active=%d succeeded=%d failed=%d)", status.Active, status.Succeeded, status.Failed)
		}
		return nil
	}
	if kind == "CronJob" {
		return nil // a CronJob has no rollout; its template images are checked by the caller
	}
	if live.Metadata.Generation == 0 || status.ObservedGeneration != live.Metadata.Generation {
		return fmt.Errorf("observedGeneration=%d generation=%d", status.ObservedGeneration, live.Metadata.Generation)
	}
	onDelete := live.Spec.UpdateStrategy.Type == "OnDelete"
	switch kind {
	case "DaemonSet":
		desired := status.DesiredNumberScheduled
		if status.NumberReady != desired || status.NumberAvailable != desired || status.NumberUnavailable != 0 ||
			(!onDelete && status.UpdatedNumberScheduled != desired) {
			return fmt.Errorf("is not fully Ready (desired=%d updated=%d ready=%d available=%d unavailable=%d)",
				desired, status.UpdatedNumberScheduled, status.NumberReady, status.NumberAvailable, status.NumberUnavailable)
		}
		return nil
	case "StatefulSet":
		desired := desiredReplicas(live)
		if status.ReadyReplicas != desired || (!onDelete && status.UpdatedReplicas != desired) {
			return fmt.Errorf("is not fully Ready (desired=%d updated=%d ready=%d)", desired, status.UpdatedReplicas, status.ReadyReplicas)
		}
		return nil
	default: // Deployment
		desired := desiredReplicas(live)
		if status.UpdatedReplicas != desired || status.ReadyReplicas != desired || status.AvailableReplicas != desired ||
			(status.UnavailableReplicas != nil && *status.UnavailableReplicas != 0) {
			return fmt.Errorf("is not fully Ready (desired=%d updated=%d ready=%d available=%d unavailable=%v)",
				desired, status.UpdatedReplicas, status.ReadyReplicas, status.AvailableReplicas, status.UnavailableReplicas)
		}
		return nil
	}
}

func desiredReplicas(live liveHelmWorkload) int32 {
	if live.Spec.Replicas != nil {
		return *live.Spec.Replicas
	}
	return 1
}

func sameStringList(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
