package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/user/doops/agent/api"
	"gopkg.in/yaml.v3"
)

var helmValueKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
var helmImageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var helmReleaseTagPattern = regexp.MustCompile(`^release-[0-9]{8}$`)
var helmRegistryHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$`)
var helmRepositoryComponentPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)

type helmCommand struct {
	args                  []string
	cwd                   string
	release               string
	namespace             string
	preserveReleaseValues bool
	preserveInsertAt      int
	expectedImage         string
}

func handleHelmTool(ctx context.Context, raw json.RawMessage, push notificationSender) (string, error) {
	var params api.HelmParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return "", fmt.Errorf("invalid helm request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid helm request")
	}

	command, err := buildHelmCommand(params)
	if err != nil {
		return "", err
	}
	verification, err := runHelmCommand(ctx, command, push)
	if err != nil {
		return "", err
	}
	if verification == "" {
		return "Helm operation complete.", nil
	}
	return "Helm operation complete: " + verification, nil
}

func buildHelmCommand(params api.HelmParams) (helmCommand, error) {
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.Operation = strings.ToLower(strings.TrimSpace(params.Operation))
	params.Release = strings.TrimSpace(params.Release)
	params.Chart = strings.TrimSpace(params.Chart)
	params.Namespace = strings.TrimSpace(params.Namespace)
	params.Timeout = strings.TrimSpace(params.Timeout)

	if err := validateSession(params.SessionID); err != nil {
		return helmCommand{}, err
	}
	if !params.Confirm {
		return helmCommand{}, fmt.Errorf("confirm=true is required for helm mutation %q", params.Operation)
	}
	workspace := workspaceRoot()
	workspaceInfo, err := os.Stat(workspace)
	if err != nil || !workspaceInfo.IsDir() {
		return helmCommand{}, fmt.Errorf("agent runtime root is not available")
	}
	if !k8sNamePattern.MatchString(params.Release) {
		return helmCommand{}, fmt.Errorf("invalid helm release")
	}
	if !k8sNamePattern.MatchString(params.Namespace) {
		return helmCommand{}, fmt.Errorf("invalid kubernetes namespace")
	}
	if params.Timeout != "" && !k8sTimeoutPattern.MatchString(params.Timeout) {
		return helmCommand{}, fmt.Errorf("invalid helm timeout")
	}

	common := []string{"--namespace", params.Namespace, "--wait"}
	if params.Timeout != "" {
		common = append(common, "--timeout", params.Timeout)
	}

	switch params.Operation {
	case "upgrade-install":
		chart, err := resolveRemoteHelmChartReference(params.Chart)
		if err != nil {
			return helmCommand{}, err
		}
		args := []string{"upgrade", params.Release, chart, "--install", "--atomic"}
		preserveInsertAt := len(args)
		valueFiles := make([]string, 0, len(params.ValueFiles))
		for _, valuePath := range params.ValueFiles {
			resolved, err := resolveRemoteHelmValueReference(valuePath)
			if err != nil {
				return helmCommand{}, err
			}
			valueFiles = append(valueFiles, resolved)
			args = append(args, "--values", resolved)
		}
		expectedImage, err := validateHelmImageInput(params.SetStrings)
		if err != nil {
			return helmCommand{}, err
		}
		keys := make([]string, 0, len(params.SetStrings))
		for key := range params.SetStrings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := params.SetStrings[key]
			if !helmValueKeyPattern.MatchString(key) {
				return helmCommand{}, fmt.Errorf("invalid helm set_strings key")
			}
			if credentialSecretLikeKey(key) || credentialScalarContainsInlineMaterial(value) ||
				strings.ContainsAny(value, "\r\n") {
				return helmCommand{}, fmt.Errorf("helm set_strings contains forbidden credential material")
			}
			args = append(args, "--set-string", key+"="+escapeHelmSetString(value))
		}
		return helmCommand{
			args:                  append(args, common...),
			cwd:                   workspace,
			release:               params.Release,
			namespace:             params.Namespace,
			preserveReleaseValues: true,
			preserveInsertAt:      preserveInsertAt,
			expectedImage:         expectedImage,
		}, nil
	case "rollback":
		if params.Revision <= 0 {
			return helmCommand{}, fmt.Errorf("positive revision is required for helm rollback")
		}
		args := []string{"rollback", params.Release, fmt.Sprintf("%d", params.Revision), "--atomic"}
		return helmCommand{args: append(args, common...), cwd: workspace}, nil
	case "uninstall":
		if params.Revision != 0 {
			return helmCommand{}, fmt.Errorf("revision is not valid for helm uninstall")
		}
		args := []string{"uninstall", params.Release}
		return helmCommand{args: append(args, common...), cwd: workspace}, nil
	default:
		return helmCommand{}, fmt.Errorf("unsupported helm operation %q", params.Operation)
	}
}

func validateHelmImageInput(setStrings map[string]string) (string, error) {
	imageRepository, hasImageRepository := setStrings["image.repository"]
	imageDigest, hasImageDigest := setStrings["image.digest"]
	imageTag, hasImageTag := setStrings["image.tag"]
	if !hasImageRepository {
		return "", fmt.Errorf(
			"upgrade-install requires image.repository plus either image.digest or image.tag set_strings",
		)
	}
	if !validHelmImageRepository(imageRepository) {
		return "", fmt.Errorf("invalid image.repository set_string")
	}
	if hasImageDigest == hasImageTag {
		return "", fmt.Errorf(
			"upgrade-install requires exactly one of image.digest or image.tag set_strings",
		)
	}
	if len(setStrings) != 2 {
		return "", fmt.Errorf(
			"direct helm apply only allows image.repository plus one controlled image selector",
		)
	}
	if hasImageDigest {
		if !helmImageDigestPattern.MatchString(imageDigest) {
			return "", fmt.Errorf("invalid image.digest set_string")
		}
		return imageRepository + "@" + imageDigest, nil
	}
	if !validHelmReleaseTag(imageTag) {
		return "", fmt.Errorf("invalid image.tag set_string: expected release-YYYYMMDD")
	}
	return imageRepository + ":" + imageTag, nil
}

func validHelmReleaseTag(value string) bool {
	if !helmReleaseTagPattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("20060102", strings.TrimPrefix(value, "release-"))
	return err == nil
}

func resolveRemoteHelmChartReference(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !isRemoteHelmChartReference(value) {
		return "", fmt.Errorf("helm chart must be an explicit https:// or oci:// reference")
	}
	return value, nil
}

func resolveRemoteHelmValueReference(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !isRemoteHelmValueReference(value) {
		return "", fmt.Errorf("helm value files must be explicit http(s):// references")
	}
	return value, nil
}

func isRemoteHelmChartReference(value string) bool {
	return isRemoteHelmReference(value, "oci", "http", "https")
}

func isRemoteHelmValueReference(value string) bool {
	return isRemoteHelmReference(value, "http", "https")
}

func isRemoteHelmReference(value string, schemes ...string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	for _, scheme := range schemes {
		if strings.EqualFold(parsed.Scheme, scheme) {
			return true
		}
	}
	return false
}

func validHelmImageRepository(value string) bool {
	if value == "" || strings.Contains(value, "://") || strings.Contains(value, "@") ||
		strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") ||
		strings.Contains(value, "//") {
		return false
	}
	parts := strings.Split(value, "/")
	pathStart := 0
	if len(parts) > 1 &&
		(strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		if !helmRegistryHostPattern.MatchString(parts[0]) {
			return false
		}
		pathStart = 1
	}
	for _, part := range parts[pathStart:] {
		if !helmRepositoryComponentPattern.MatchString(part) {
			return false
		}
	}
	return pathStart < len(parts)
}

func escapeHelmSetString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, ",", `\,`, "=", `\=`)
	return replacer.Replace(value)
}

func runHelmCommand(ctx context.Context, command helmCommand, push notificationSender) (string, error) {
	var stdin []byte
	if command.preserveReleaseValues {
		existingValues, found, err := prepareExistingHelmValues(ctx, command)
		if err != nil {
			return "", err
		}
		if found {
			preserveArgs := []string{"--reset-values", "--values", "-"}
			args := make([]string, 0, len(command.args)+len(preserveArgs))
			args = append(args, command.args[:command.preserveInsertAt]...)
			args = append(args, preserveArgs...)
			args = append(args, command.args[command.preserveInsertAt:]...)
			command.args = args
			stdin = existingValues
		}
	}

	if command.preserveReleaseValues {
		// Server-side dry-run preserves lookup behavior before Helm creates a revision.
		preflightArgs := make([]string, len(command.args)+1)
		copy(preflightArgs, command.args)
		preflightArgs[len(command.args)] = "--dry-run=server"
		preflight := exec.CommandContext(ctx, "helm", preflightArgs...)
		preflight.Dir = command.cwd
		if len(stdin) != 0 {
			preflight.Stdin = bytes.NewReader(stdin)
		}
		preflight.Stdout = io.Discard
		var preflightStderr bytes.Buffer
		preflight.Stderr = &preflightStderr
		if err := preflight.Run(); err != nil {
			return "", fmt.Errorf(
				"helm preflight failed: %w: %s",
				err,
				strings.TrimSpace(preflightStderr.String()),
			)
		}
	}

	cmd := exec.CommandContext(ctx, "helm", command.args...)
	cmd.Dir = command.cwd
	if len(stdin) != 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("start helm: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("start helm: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start helm: %w", err)
	}

	var wg sync.WaitGroup
	var stderrCapture bytes.Buffer
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamReader(stdout, push)
	}()
	go func() {
		defer wg.Done()
		streamReader(struct {
			io.Reader
			io.Closer
		}{
			Reader: io.TeeReader(stderr, &stderrCapture),
			Closer: stderr,
		}, push)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf(
			"helm %s failed: %w: %s",
			strings.Join(command.args, " "),
			err,
			strings.TrimSpace(stderrCapture.String()),
		)
	}
	if command.operation() != "upgrade" {
		return "", nil
	}
	return verifyHelmDeploymentState(ctx, command)
}

func (c helmCommand) operation() string {
	if len(c.args) == 0 {
		return ""
	}
	return c.args[0]
}

type renderedHelmDeployment struct {
	Name       string
	Containers []string
}

type liveDeployment struct {
	Metadata struct {
		Generation int64 `json:"generation"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int32 `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ObservedGeneration  int64  `json:"observedGeneration"`
		UpdatedReplicas     int32  `json:"updatedReplicas"`
		ReadyReplicas       int32  `json:"readyReplicas"`
		AvailableReplicas   int32  `json:"availableReplicas"`
		UnavailableReplicas *int32 `json:"unavailableReplicas"`
	} `json:"status"`
}

func verifyHelmDeploymentState(ctx context.Context, command helmCommand) (string, error) {
	manifestCmd := exec.CommandContext(
		ctx,
		"helm",
		"get",
		"manifest",
		command.release,
		"--namespace",
		command.namespace,
	)
	manifestCmd.Dir = command.cwd
	manifest, err := manifestCmd.Output()
	if err != nil {
		return "", fmt.Errorf("helm verification failed: read final release manifest: %w", err)
	}
	deployments, err := parseRenderedHelmDeployments(manifest)
	if err != nil {
		return "", fmt.Errorf("helm verification failed: %w", err)
	}
	if len(deployments) == 0 {
		return "", fmt.Errorf("helm verification failed: final release manifest contains no Deployment")
	}

	expectedRepository := imageRepository(command.expectedImage)
	for _, expected := range deployments {
		if !deploymentHasExpectedImage(expected, command.expectedImage, expectedRepository) {
			return "", fmt.Errorf(
				"helm verification failed: rendered Deployment %s/%s does not contain expected image %q",
				command.namespace,
				expected.Name,
				command.expectedImage,
			)
		}

		getCmd := exec.CommandContext(
			ctx,
			"kubectl",
			"get",
			"deployment",
			expected.Name,
			"--namespace",
			command.namespace,
			"--output",
			"json",
		)
		getCmd.Dir = command.cwd
		liveBytes, err := getCmd.Output()
		if err != nil {
			return "", fmt.Errorf(
				"helm verification failed: read final Deployment %s/%s: %w",
				command.namespace,
				expected.Name,
				err,
			)
		}
		var live liveDeployment
		if err := json.Unmarshal(liveBytes, &live); err != nil {
			return "", fmt.Errorf(
				"helm verification failed: decode final Deployment %s/%s: %w",
				command.namespace,
				expected.Name,
				err,
			)
		}
		if live.Metadata.Generation == 0 ||
			live.Status.ObservedGeneration != live.Metadata.Generation {
			return "", fmt.Errorf(
				"helm verification failed: Deployment %s/%s observedGeneration=%d generation=%d",
				command.namespace,
				expected.Name,
				live.Status.ObservedGeneration,
				live.Metadata.Generation,
			)
		}
		desiredReplicas := int32(1)
		if live.Spec.Replicas != nil {
			desiredReplicas = *live.Spec.Replicas
		}
		if live.Status.UpdatedReplicas != desiredReplicas ||
			live.Status.ReadyReplicas != desiredReplicas ||
			live.Status.AvailableReplicas != desiredReplicas ||
			(live.Status.UnavailableReplicas != nil && *live.Status.UnavailableReplicas != 0) {
			return "", fmt.Errorf(
				"helm verification failed: Deployment %s/%s is not fully Ready (desired=%d updated=%d ready=%d available=%d unavailable=%v)",
				command.namespace,
				expected.Name,
				desiredReplicas,
				live.Status.UpdatedReplicas,
				live.Status.ReadyReplicas,
				live.Status.AvailableReplicas,
				live.Status.UnavailableReplicas,
			)
		}
		actualImages := make([]string, 0, len(live.Spec.Template.Spec.Containers))
		for _, container := range live.Spec.Template.Spec.Containers {
			actualImages = append(actualImages, strings.TrimSpace(container.Image))
		}
		if !sameStringList(expected.Containers, actualImages) {
			return "", fmt.Errorf(
				"helm verification failed: Deployment %s/%s spec images=%q rendered=%q",
				command.namespace,
				expected.Name,
				actualImages,
				expected.Containers,
			)
		}
	}
	return fmt.Sprintf("final Deployment spec verified: %d Deployment(s) observed at current generation and Ready", len(deployments)), nil
}

func parseRenderedHelmDeployments(manifest []byte) ([]renderedHelmDeployment, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(manifest)))
	var deployments []renderedHelmDeployment
	seen := make(map[string]struct{})
	for {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `yaml:"image"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode final release manifest: %w", err)
		}
		if strings.EqualFold(strings.TrimSpace(document.Kind), "List") {
			return nil, fmt.Errorf("final release manifest uses unsupported Deployment List wrapper")
		}
		if !strings.EqualFold(strings.TrimSpace(document.Kind), "Deployment") {
			continue
		}
		name := strings.TrimSpace(document.Metadata.Name)
		if name == "" {
			return nil, fmt.Errorf("final release manifest contains Deployment without metadata.name")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("final release manifest contains duplicate Deployment %q", name)
		}
		seen[name] = struct{}{}
		containers := make([]string, 0, len(document.Spec.Template.Spec.Containers))
		for _, container := range document.Spec.Template.Spec.Containers {
			image := strings.TrimSpace(container.Image)
			if image == "" {
				return nil, fmt.Errorf("final release manifest Deployment %q contains an empty image", name)
			}
			containers = append(containers, image)
		}
		if len(containers) == 0 {
			return nil, fmt.Errorf("final release manifest Deployment %q contains no containers", name)
		}
		deployments = append(deployments, renderedHelmDeployment{Name: name, Containers: containers})
	}
	return deployments, nil
}

func deploymentHasExpectedImage(deployment renderedHelmDeployment, expected, repository string) bool {
	found := false
	for _, image := range deployment.Containers {
		if image == expected {
			found = true
			continue
		}
		if repository != "" && imageRepository(image) == repository {
			return false
		}
	}
	return found
}

func imageRepository(image string) string {
	image = strings.TrimSpace(image)
	if at := strings.IndexByte(image, '@'); at >= 0 {
		return image[:at]
	}
	lastSlash := strings.LastIndexByte(image, '/')
	lastColon := strings.LastIndexByte(image, ':')
	if lastColon > lastSlash {
		return image[:lastColon]
	}
	return image
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

func prepareExistingHelmValues(ctx context.Context, command helmCommand) ([]byte, bool, error) {
	status := exec.CommandContext(
		ctx,
		"helm",
		"status",
		command.release,
		"--namespace",
		command.namespace,
	)
	status.Dir = command.cwd
	if output, err := status.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "release: not found") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("inspect existing helm release: %w: %s", err, strings.TrimSpace(string(output)))
	}

	getValues := exec.CommandContext(
		ctx,
		"helm",
		"get",
		"values",
		command.release,
		"--namespace",
		command.namespace,
		"--output",
		"json",
	)
	getValues.Dir = command.cwd
	var getValuesStderr bytes.Buffer
	getValues.Stderr = &getValuesStderr
	output, err := getValues.Output()
	if err != nil {
		return nil, false, fmt.Errorf(
			"read existing helm values: %w: %s",
			err,
			strings.TrimSpace(getValuesStderr.String()),
		)
	}

	var values map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return nil, false, fmt.Errorf("decode existing helm values: %w", err)
	}
	if image, ok := values["image"].(map[string]interface{}); ok {
		delete(image, "repository")
		delete(image, "digest")
		delete(image, "tag")
		if len(image) == 0 {
			delete(values, "image")
		}
	}
	sanitized, err := json.Marshal(values)
	if err != nil {
		return nil, false, fmt.Errorf("encode existing helm values: %w", err)
	}
	return sanitized, true, nil
}
