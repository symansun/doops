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
//	helm_no_workload              no workload or ingress the verifier can check was rendered
//	helm_image_mismatch           expected_image is in no rendered container
//	helm_mutation_failed          the real helm command failed (first stderr line)
//	helm_mutation_invalid_result  helm output has no revision for this mutation
//	helm_verification_failed      live state does not match the rendered release
//	helm_interrupted              the call ran out of time; the reason names the
//	                              revision and state the release was left in
//
// Only rollback and uninstall skip preflight and verify.
//
// A doops_helm call must end, with its outcome recorded, before doagent's MCP
// tool timeout stops the server. An interrupted helm that is simply killed
// leaves the release pending-upgrade with nobody to record its terminal state,
// and every later Helm operation on it fails. So the whole call runs under
// helmToolDeadline, helm is stopped with SIGTERM (Helm then records the
// revision failed itself), and settleInterruptedHelmRelease checks the result.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// helmSettleReserve is kept free at the end of the MCP tool timeout for the
	// SIGTERM grace period and the settle step.
	helmSettleReserve = 90 * time.Second
	// maxHelmWaitTimeout is the largest helm --timeout a single call accepts, so
	// the helm wait itself always ends inside helmToolDeadline. Verification
	// shares what is left; running out there is reported as helm_interrupted.
	maxHelmWaitTimeout = 10 * time.Minute
	// helmSettleTimeout bounds reading history and unlocking a pending release.
	helmSettleTimeout = 45 * time.Second
)

var (
	// helmToolDeadline bounds one whole doops_helm call. It is tighter than
	// mcpToolCallDeadline so the SIGTERM grace and the settle step still fit
	// before doagent's own timeout. Vars so tests can shorten them.
	helmToolDeadline = time.Duration(doopsMCPToolTimeoutSeconds)*time.Second - helmSettleReserve
	// helmTerminationGrace is how long an interrupted helm has after SIGTERM to
	// record its terminal status before it is killed.
	helmTerminationGrace = 30 * time.Second
)

var (
	errHelmManifestInvalid = errors.New("helm_manifest_invalid")
	errHelmNoWorkload      = errors.New("helm_no_workload: rendered release contains no verifiable workload or ingress")
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
		if command.mutationID == "" {
			return "", err
		}
		// The upgrade failed. Helm may have left this mutation's revision
		// pending, which locks the release for every later operation, so the
		// revision is settled before the error is returned.
		note, settleErr := settleHelmRevision(ctx, command)
		if settleErr != nil {
			// The release state is unknown, but Helm's own failure is still the
			// result. The interruption is only claimed when our budget ended.
			if ctx.Err() != nil {
				return "", helmStepFailure("helm_interrupted", fmt.Sprintf(
					"the call ran out of time and the release history could not be read; check helm status before any retry; helm reported: %s", err.Error()))
			}
			return "", helmStepFailure("helm_mutation_failed", fmt.Sprintf(
				"the release history could not be read, so the release state is unknown; check helm status before any retry; helm reported: %s", err.Error()))
		}
		if note == "" {
			return "", err
		}
		if ctx.Err() != nil {
			return "", helmStepFailure("helm_interrupted", fmt.Sprintf(
				"the call ran out of time; %s; helm reported: %s", note, err.Error()))
		}
		// Helm failed on its own terms and left nothing pending: the original
		// failure is the result, with the revision state added.
		return "", helmStepFailure("helm_mutation_failed", fmt.Sprintf("%s; helm reported: %s", note, err.Error()))
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
		if ctx.Err() != nil {
			return "", helmStepFailure("helm_interrupted", fmt.Sprintf(
				"the call ran out of time after revision %d was deployed; the workloads were not fully verified, read them back before relying on it", revision))
		}
		return "", helmStepFailure("helm_verification_failed", redactSecretText(err.Error()))
	}
	return verified, nil
}

// preflightHelmUpgrade renders the upgrade server-side, so lookups behave as in
// the real run, and checks the rendered workloads before Helm creates a revision.
func preflightHelmUpgrade(ctx context.Context, command helmCommand) error {
	args := append(append([]string(nil), command.args...), "--dry-run=server", "--output=json")
	preflight := helmProcess(ctx, command.cwd, args...)
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
	if _, err := validateHelmRenderedContent(manifest, command.expectedImage); err != nil {
		return renderedWorkloadFailure(err)
	}
	return nil
}

func mutateHelmRelease(ctx context.Context, command helmCommand) ([]byte, error) {
	args := append([]string(nil), command.args...)
	if command.operation() == "upgrade" {
		args = append(args, "--output=json")
	}
	cmd := helmProcess(ctx, command.cwd, args...)
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

// helmProcess stops helm with SIGTERM when ctx ends, so Helm can record the
// revision failed and release its lock; it is killed only after the grace period.
func helmProcess(ctx context.Context, cwd string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Dir = cwd
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = helmTerminationGrace
	return cmd
}

type helmHistoryEntry struct {
	Revision    int    `json:"revision"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// settleHelmRevision reads what Helm recorded for this mutation's revision and
// makes sure the release is not left locked. A revision still pending-* blocks
// every later Helm operation on the release, so it is rolled back to the last
// deployed revision; any other state is left for the operator.
//
// The returned note describes what was done, and is empty when the release
// needs nothing. An error means the release state could not be read, so the
// caller must not claim the release is fine.
func settleHelmRevision(ctx context.Context, command helmCommand) (string, error) {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), helmSettleTimeout)
	defer cancel()
	historyCmd := exec.CommandContext(settleCtx, "helm", "history", command.release,
		"--namespace", command.namespace, "--max", "20", "--output", "json")
	historyCmd.Dir = command.cwd
	raw, err := historyCmd.Output()
	var history []helmHistoryEntry
	if err == nil {
		err = json.Unmarshal(raw, &history)
	}
	if err != nil {
		return "", helmStepFailure("helm_interrupted",
			"the release history could not be read; check helm status before any retry")
	}
	var mine *helmHistoryEntry
	deployed := 0
	for i := range history {
		entry := &history[i]
		if entry.Description == command.mutationID {
			mine = entry
		}
		if entry.Status == "deployed" && entry.Revision > deployed {
			deployed = entry.Revision
		}
	}
	switch {
	case mine == nil:
		return "", nil
	case mine.Status == "failed":
		// Helm recorded the failure itself; nothing is locked.
		return fmt.Sprintf("Helm recorded revision %d failed; deployed revision %d is unchanged", mine.Revision, deployed), nil
	case mine.Status == "deployed":
		return "", nil
	case !strings.HasPrefix(mine.Status, "pending-"):
		return "", nil
	case deployed == 0:
		return fmt.Sprintf("revision %d is %s and there is no deployed revision to roll back to; uninstall it or rerun the install", mine.Revision, mine.Status), nil
	}
	// No --wait: unlocking the release must finish inside the settle budget.
	rollbackCmd := exec.CommandContext(settleCtx, "helm", "rollback", command.release, fmt.Sprintf("%d", deployed),
		"--namespace", command.namespace)
	rollbackCmd.Dir = command.cwd
	var stderr bytes.Buffer
	rollbackCmd.Stderr = &stderr
	if err := rollbackCmd.Run(); err != nil {
		return "", helmStepFailure("helm_interrupted", fmt.Sprintf(
			"revision %d stayed %s and the rollback to revision %d failed: %s",
			mine.Revision, mine.Status, deployed, helmStderrReason(stderr.Bytes())))
	}
	return fmt.Sprintf("revision %d stayed %s, so the release was rolled back to revision %d to unlock it; verify the workloads before retrying",
		mine.Revision, mine.Status, deployed), nil
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

// helmIngressKinds maps a rendered kind to its canonical name when the kind is
// a release resource the verifier can read back without running a pod. An
// Ingress carries no container image, so it is checked by shape instead: the
// live object must exist and its routing must equal the rendered routing.
var helmIngressKinds = map[string]string{
	"ingress": "Ingress",
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

// helmIngressSpec is the part of an Ingress the verifier compares: the class,
// the TLS material and every route with its backend. It is shared by the
// rendered manifest (YAML) and the live object (JSON), so both sides produce
// the same signature.
type helmIngressSpec struct {
	IngressClassName string            `yaml:"ingressClassName" json:"ingressClassName"`
	TLS              []helmIngressTLS  `yaml:"tls" json:"tls"`
	Rules            []helmIngressRule `yaml:"rules" json:"rules"`
}

// helmIngressTLS is one TLS block of an Ingress.
type helmIngressTLS struct {
	Hosts      []string `yaml:"hosts" json:"hosts"`
	SecretName string   `yaml:"secretName" json:"secretName"`
}

// helmIngressRule is one host rule of an Ingress.
type helmIngressRule struct {
	Host string           `yaml:"host" json:"host"`
	HTTP *helmIngressHTTP `yaml:"http" json:"http"`
}

// helmIngressHTTP holds the paths of one host rule.
type helmIngressHTTP struct {
	Paths []helmIngressPath `yaml:"paths" json:"paths"`
}

// helmIngressPath is one route of an Ingress.
type helmIngressPath struct {
	Path     string             `yaml:"path" json:"path"`
	PathType string             `yaml:"pathType" json:"pathType"`
	Backend  helmIngressBackend `yaml:"backend" json:"backend"`
}

// helmIngressBackend is the Service an Ingress path routes to. Both the
// networking.k8s.io/v1 spelling and the pre-v1 spelling are accepted, because
// a live object and a rendered manifest may use either.
type helmIngressBackend struct {
	Service *helmIngressService `yaml:"service" json:"service"`
	// The spelling used before networking.k8s.io/v1.
	ServiceName string `yaml:"serviceName" json:"serviceName"`
	ServicePort int32  `yaml:"servicePort" json:"servicePort"`
}

// helmIngressService is the Service and port an Ingress path routes to.
type helmIngressService struct {
	Name string                 `yaml:"name" json:"name"`
	Port helmIngressServicePort `yaml:"port" json:"port"`
}

// helmIngressServicePort is the port an Ingress path routes to.
type helmIngressServicePort struct {
	Number int32 `yaml:"number" json:"number"`
}

// renderedHelmIngress is a rendered Ingress reduced to the routing the
// verifier compares with the live object. An Ingress carries no image, so it
// is verified by routing shape and by the existence of the backends it names.
type renderedHelmIngress struct {
	Kind      string
	Name      string
	Namespace string
	// Signature is the canonical routing of this Ingress. The live object must
	// produce the same signature.
	Signature string
	// Backends lists the distinct Service names this Ingress routes to, with
	// the port each one is reached on. They must exist and serve that port, or
	// the Ingress serves errors.
	Backends map[string]int32
}

// helmIngressBackendService returns the backend Service name and port of one
// path, accepting both the v1 and the pre-v1 spelling.
func helmIngressBackendService(backend helmIngressBackend) (string, int32) {
	if backend.Service != nil {
		return backend.Service.Name, backend.Service.Port.Number
	}
	return backend.ServiceName, backend.ServicePort
}

// helmIngressSignature is the canonical routing of an Ingress: its class, every
// route with its backend, and its TLS material. Two Ingresses with the same
// signature route the same traffic, so the rendered and the live side are
// compared through it.
func helmIngressSignature(spec helmIngressSpec) string {
	lines := make([]string, 0, 8)
	if spec.IngressClassName != "" {
		lines = append(lines, "class="+spec.IngressClassName)
	}
	for _, rule := range spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			name, port := helmIngressBackendService(path.Backend)
			lines = append(lines, fmt.Sprintf("route host=%s path=%s pathType=%s backend=%s:%d",
				rule.Host, path.Path, path.PathType, name, port))
		}
	}
	for _, tls := range spec.TLS {
		hosts := append([]string(nil), tls.Hosts...)
		sort.Strings(hosts)
		lines = append(lines, fmt.Sprintf("tls secret=%s hosts=%s", tls.SecretName, strings.Join(hosts, ",")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// helmIngressBackends lists the distinct Service names an Ingress routes to,
// with the port each one is reached on.
func helmIngressBackends(spec helmIngressSpec) map[string]int32 {
	backends := map[string]int32{}
	for _, rule := range spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			name, port := helmIngressBackendService(path.Backend)
			if name == "" {
				continue
			}
			if _, exists := backends[name]; !exists {
				backends[name] = port
			}
		}
	}
	return backends
}

// helmIngressPathCount counts the routes an Ingress declares.
func helmIngressPathCount(spec helmIngressSpec) int {
	count := 0
	for _, rule := range spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		count += len(rule.HTTP.Paths)
	}
	return count
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

// helmRenderedContent is what a release rendered: the workloads that must roll
// out, and the Ingresses whose routing must match the live objects.
type helmRenderedContent struct {
	Workloads []renderedHelmWorkload
	Ingresses []renderedHelmIngress
}

// validateHelmRenderedContent returns the rendered release content and, when
// expectedImage is set, requires it in at least one container. A release that
// renders neither a workload nor an Ingress has nothing the verifier can
// check, which is the one thing this refuses.
func validateHelmRenderedContent(manifest []byte, expectedImage string) (helmRenderedContent, error) {
	content, err := parseRenderedHelmContent(manifest)
	if err != nil {
		return helmRenderedContent{}, err
	}
	if len(content.Workloads) == 0 && len(content.Ingresses) == 0 {
		return helmRenderedContent{}, errHelmNoWorkload
	}
	if expectedImage == "" {
		return content, nil
	}
	for _, workload := range content.Workloads {
		for _, image := range workload.Containers {
			if image == expectedImage {
				return content, nil
			}
		}
	}
	rendered := make([]string, 0, len(content.Workloads))
	for _, workload := range content.Workloads {
		rendered = append(rendered, workload.Kind+"/"+workload.Name+"="+strings.Join(workload.Containers, ","))
	}
	return helmRenderedContent{}, fmt.Errorf("%w %q; rendered %s", errHelmImageMismatch, expectedImage, strings.Join(rendered, " "))
}

// parseRenderedHelmContent reads both kinds of release content out of one
// rendered manifest.
func parseRenderedHelmContent(manifest []byte) (helmRenderedContent, error) {
	workloads, err := parseRenderedHelmWorkloads(manifest)
	if err != nil {
		return helmRenderedContent{}, err
	}
	ingresses, err := parseRenderedHelmIngresses(manifest)
	if err != nil {
		return helmRenderedContent{}, err
	}
	return helmRenderedContent{Workloads: workloads, Ingresses: ingresses}, nil
}

// validateHelmRenderedImages returns the rendered workloads and, when
// expectedImage is set, requires it in at least one container.
func validateHelmRenderedImages(manifest []byte, expectedImage string) ([]renderedHelmWorkload, error) {
	content, err := validateHelmRenderedContent(manifest, expectedImage)
	if err != nil {
		return nil, err
	}
	return content.Workloads, nil
}

// parseRenderedHelmIngresses reads the rendered Ingresses out of a manifest. An
// Ingress is release content the verifier can check, so it is parsed by shape
// instead of by container image.
func parseRenderedHelmIngresses(manifest []byte) ([]renderedHelmIngress, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	var ingresses []renderedHelmIngress
	seen := map[string]struct{}{}
	for index := 1; ; index++ {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
			Spec helmIngressSpec `yaml:"spec"`
		}
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("%w: YAML document %d: %v", errHelmManifestInvalid, index, err)
		}
		kind, ok := helmIngressKinds[strings.ToLower(strings.TrimSpace(document.Kind))]
		if !ok {
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
		if helmIngressPathCount(document.Spec) == 0 {
			return nil, fmt.Errorf("%w: %s has no routes", errHelmManifestInvalid, key)
		}
		ingresses = append(ingresses, renderedHelmIngress{
			Kind: kind, Name: name, Namespace: namespace,
			Signature: helmIngressSignature(document.Spec),
			Backends:  helmIngressBackends(document.Spec),
		})
	}
	return ingresses, nil
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
// rolled out (Ready, or complete for hook Jobs). Rendered Ingresses are checked
// too: the live object must route exactly like the rendered one, and the
// Services it names must exist.
func verifyHelmDeploymentState(ctx context.Context, command helmCommand) (string, error) {
	if command.mutationID != "" && command.revision <= 0 {
		return "", fmt.Errorf("mutation revision is unavailable")
	}
	content, err := readReleaseContent(ctx, command)
	if err != nil {
		return "", err
	}
	for _, expected := range content.Workloads {
		if err := verifyLiveWorkload(ctx, command, expected); err != nil {
			return "", err
		}
	}
	for _, expected := range content.Ingresses {
		if err := verifyLiveIngress(ctx, command, expected); err != nil {
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
	return fmt.Sprintf("final Helm workload spec verified: %d workload(s) and %d ingress(es) observed and Ready or complete",
		len(content.Workloads), len(content.Ingresses)), nil
}

// readReleaseContent loads the release manifest; a hook-only release has its
// workloads in the hooks.
func readReleaseContent(ctx context.Context, command helmCommand) (helmRenderedContent, error) {
	args := []string{"get", "manifest", command.release, "--namespace", command.namespace}
	if command.revision > 0 {
		args = append(args, "--revision", fmt.Sprintf("%d", command.revision))
	}
	manifestCmd := exec.CommandContext(ctx, "helm", args...)
	manifestCmd.Dir = command.cwd
	manifest, err := manifestCmd.Output()
	if err != nil {
		return helmRenderedContent{}, fmt.Errorf("read final release manifest: %w", err)
	}
	content, err := validateHelmRenderedContent(manifest, command.expectedImage)
	if errors.Is(err, errHelmNoWorkload) {
		hookArgs := []string{"get", "hooks", command.release, "--namespace", command.namespace}
		if command.revision > 0 {
			hookArgs = append(hookArgs, "--revision", fmt.Sprintf("%d", command.revision))
		}
		hookCmd := exec.CommandContext(ctx, "helm", hookArgs...)
		hookCmd.Dir = command.cwd
		if hooks, hookErr := hookCmd.Output(); hookErr == nil {
			content, err = validateHelmRenderedContent(append(append(manifest, "\n---\n"...), hooks...), command.expectedImage)
		}
	}
	if err != nil {
		return helmRenderedContent{}, errors.New(helmRenderedErrorReason(err))
	}
	return content, nil
}

// defaultHelmRolloutTimeout matches Helm's own default --timeout.
const defaultHelmRolloutTimeout = 5 * time.Minute

// helmRolloutPollInterval is how often verification re-reads a workload that is
// still rolling out. A var so tests can shorten it.
var helmRolloutPollInterval = 3 * time.Second

// liveHelmIngress is the live Ingress, reduced to the routing the verifier
// compares with the rendered release.
type liveHelmIngress struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec helmIngressSpec `json:"spec"`
}

// verifyLiveIngress checks that the live Ingress routes exactly like the
// rendered one, and that every Service it routes to exists. An Ingress whose
// backend Service is gone serves errors, so a missing Service fails the
// release instead of passing it.
func verifyLiveIngress(ctx context.Context, command helmCommand, expected renderedHelmIngress) error {
	namespace := expected.Namespace
	if namespace == "" {
		namespace = command.namespace
	}
	ref := fmt.Sprintf("%s %s/%s", expected.Kind, namespace, expected.Name)
	getCmd := exec.CommandContext(ctx, "kubectl", "get", "ingress", expected.Name,
		"--namespace", namespace, "--output", "json")
	getCmd.Dir = command.cwd
	liveBytes, err := getCmd.Output()
	if err != nil {
		return fmt.Errorf("read final %s: %w", ref, err)
	}
	var live liveHelmIngress
	if err := json.Unmarshal(liveBytes, &live); err != nil {
		return fmt.Errorf("decode final %s: %w", ref, err)
	}
	actual := helmIngressSignature(live.Spec)
	if actual != expected.Signature {
		return fmt.Errorf("%s routes differently than the release", ref)
	}
	for name, port := range expected.Backends {
		if err := verifyIngressBackend(ctx, command, namespace, name, port); err != nil {
			return fmt.Errorf("%s: %w", ref, err)
		}
	}
	return nil
}

// verifyIngressBackend checks that one backend Service of an Ingress exists
// and exposes the port the Ingress routes to. An Ingress pointing at a port
// the Service does not serve answers with errors, so that is a failed release.
func verifyIngressBackend(ctx context.Context, command helmCommand, namespace, name string, port int32) error {
	getCmd := exec.CommandContext(ctx, "kubectl", "get", "service", name,
		"--namespace", namespace, "--output", "json")
	getCmd.Dir = command.cwd
	serviceBytes, err := getCmd.Output()
	if err != nil {
		return fmt.Errorf("backend Service %s/%s does not exist", namespace, name)
	}
	var service struct {
		Spec struct {
			Ports []struct {
				Port int32 `json:"port"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(serviceBytes, &service); err != nil {
		return fmt.Errorf("read backend Service %s/%s: %w", namespace, name, err)
	}
	for _, servicePort := range service.Spec.Ports {
		if servicePort.Port == port {
			return nil
		}
	}
	return fmt.Errorf("backend Service %s/%s does not expose port %d", namespace, name, port)
}

// verifyLiveWorkload waits for one rendered workload to roll out. Helm --wait
// returns once ready >= replicas - maxUnavailable, so with maxUnavailable >= 1 a
// Deployment can still be rolling when Helm reports success; verification keeps
// reading until the workload is fully rolled out or the release timeout passes.
func verifyLiveWorkload(ctx context.Context, command helmCommand, expected renderedHelmWorkload) error {
	timeout := command.rolloutTimeout
	if timeout <= 0 {
		timeout = defaultHelmRolloutTimeout
	}
	deadline := time.Now().Add(timeout)
	if callDeadline, ok := ctx.Deadline(); ok && callDeadline.Before(deadline) {
		deadline = callDeadline
	}
	for {
		rolling, err := checkLiveWorkload(ctx, command, expected)
		if err == nil {
			return nil
		}
		if !rolling || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(helmRolloutPollInterval):
		}
	}
}

// checkLiveWorkload reads the live object once. rolling reports a failure that
// can still resolve by waiting (not yet observed or not yet Ready); a missing
// object or an image that differs from the rendered release is final.
func checkLiveWorkload(ctx context.Context, command helmCommand, expected renderedHelmWorkload) (rolling bool, err error) {
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
			return false, nil
		}
		return false, fmt.Errorf("read final %s: %w", ref, err)
	}
	var live liveHelmWorkload
	if err := json.Unmarshal(liveBytes, &live); err != nil {
		return false, fmt.Errorf("decode final %s: %w", ref, err)
	}
	actual := live.Spec.podTemplate(expected.Kind).images()
	if !sameStringList(expected.Containers, actual) {
		return false, fmt.Errorf("%s spec images=%q rendered=%q", ref, actual, expected.Containers)
	}
	if err := liveWorkloadRolledOut(expected.Kind, live); err != nil {
		// A failed hook Job will not recover by waiting.
		return expected.Kind != "Job" || live.Status.Failed == 0, fmt.Errorf("%s %s", ref, err)
	}
	return false, nil
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
			unavailable := int32(0)
			if status.UnavailableReplicas != nil {
				unavailable = *status.UnavailableReplicas
			}
			return fmt.Errorf("is not fully Ready (desired=%d updated=%d ready=%d available=%d unavailable=%d)",
				desired, status.UpdatedReplicas, status.ReadyReplicas, status.AvailableReplicas, unavailable)
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
