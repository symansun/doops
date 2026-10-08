package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
	"gopkg.in/yaml.v3"
)

var helmValueKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
var helmImageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var helmImageTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var helmRegistryHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$`)
var helmRepositoryComponentPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)

const (
	helmAssetSourceRemote    = "remote"
	helmAssetSourceWorkspace = "workspace"
)

type helmCommand struct {
	args          []string
	cwd           string
	release       string
	namespace     string
	expectedImage string
	mutationID    string
	revision      int
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
	cleanup, err := prepareManagedHelmChart(ctx, params, &command)
	if err != nil {
		return "", err
	}
	defer cleanup()
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
	params.AssetSource = strings.TrimSpace(params.AssetSource)
	params.Chart = strings.TrimSpace(params.Chart)
	params.Namespace = strings.TrimSpace(params.Namespace)
	params.ExpectedImage = strings.TrimSpace(params.ExpectedImage)
	params.Timeout = strings.TrimSpace(params.Timeout)
	if params.AssetSource == "" {
		params.AssetSource = helmAssetSourceRemote
	}
	if params.AssetSource != helmAssetSourceRemote && params.AssetSource != helmAssetSourceWorkspace {
		return helmCommand{}, fmt.Errorf("invalid helm asset_source %q", params.AssetSource)
	}

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
		var expectedImage string
		if params.ExpectedImage != "" {
			expectedImage, err = validateExpectedHelmImage(params.ExpectedImage)
			if err != nil {
				return helmCommand{}, err
			}
		}
		var chart string
		var values []string
		if params.AssetSource == helmAssetSourceWorkspace {
			workspace, chart, values, err = resolveWorkspaceHelmAssets(params)
		} else {
			chart, err = resolveRemoteHelmChartReference(params.Chart)
			if err == nil {
				for _, valuePath := range params.ValueFiles {
					resolved, valueErr := resolveRemoteHelmValueReference(valuePath)
					if valueErr != nil {
						return helmCommand{}, valueErr
					}
					values = append(values, resolved)
				}
			}
		}
		if err != nil {
			return helmCommand{}, err
		}
		mutationID := fmt.Sprintf("doops:%s:%d", params.SessionID, time.Now().UnixNano())
		args := []string{"upgrade", params.Release, chart, "--install", "--atomic", "--reset-values", "--description", mutationID}
		for _, valuePath := range values {
			args = append(args, "--values", valuePath)
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
			args: append(args, common...), cwd: workspace,
			release: params.Release, namespace: params.Namespace, expectedImage: expectedImage, mutationID: mutationID,
		}, nil
	case "rollback":
		if params.AssetSource == helmAssetSourceWorkspace {
			return helmCommand{}, fmt.Errorf("workspace asset_source is only supported for helm upgrade-install")
		}
		if params.Revision <= 0 {
			return helmCommand{}, fmt.Errorf("positive revision is required for helm rollback")
		}
		args := []string{"rollback", params.Release, fmt.Sprintf("%d", params.Revision)}
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

func prepareManagedHelmChart(ctx context.Context, params api.HelmParams, command *helmCommand) (func(), error) {
	cleanup := func() {}
	if command.operation() != "upgrade" {
		return cleanup, nil
	}
	if strings.TrimSpace(params.AssetSource) == helmAssetSourceWorkspace {
		return cleanup, nil
	}
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.Release = strings.TrimSpace(params.Release)
	params.Namespace = strings.TrimSpace(params.Namespace)
	params.Chart = strings.TrimSpace(params.Chart)
	auth, expectedDigest, err := authorizedHelmCredential(ctx, params)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(params.Chart, "https://") {
		return nil, fmt.Errorf("helm_chart_requires_explicit_https_download_reference")
	}
	if auth == nil {
		parsed, err := url.Parse(params.Chart)
		if err != nil {
			return nil, fmt.Errorf("helm chart reference invalid")
		}
		auth = &helmRepositoryAuth{URL: parsed.Scheme + "://" + parsed.Host}
	}
	chart, digest, err := fetchManagedHelmChart(ctx, auth.URL, auth.Username, auth.Password, params.Chart)
	auth.Password = ""
	auth.Username = ""
	if err != nil {
		return nil, err
	}
	if expectedDigest != "" && digest != expectedDigest {
		return nil, fmt.Errorf("helm_chart_digest_changed")
	}
	directory, err := os.MkdirTemp("", "doops-helm-chart-*")
	if err != nil {
		return nil, fmt.Errorf("helm_chart_staging_failed")
	}
	cleanup = func() { _ = os.RemoveAll(directory) }
	path := filepath.Join(directory, "chart.tgz")
	if os.WriteFile(path, chart, 0600) != nil {
		cleanup()
		return nil, fmt.Errorf("helm_chart_staging_failed")
	}
	command.args[2] = path
	for index := 3; index < len(command.args); index++ {
		if command.args[index] != "--values" {
			continue
		}
		values, err := fetchHelmValues(ctx, command.args[index+1])
		if err != nil {
			cleanup()
			return nil, err
		}
		valuePath := filepath.Join(directory, fmt.Sprintf("values-%d.yaml", index))
		if err := os.WriteFile(valuePath, values, 0600); err != nil {
			cleanup()
			return nil, fmt.Errorf("helm_values_staging_failed")
		}
		command.args[index+1] = valuePath
		index++
	}
	return cleanup, nil
}

func fetchHelmValues(ctx context.Context, reference string) ([]byte, error) {
	endpoint, err := url.Parse(reference)
	if err != nil || !isRemoteHelmValueReference(reference) || credentialScalarContainsInlineMaterial(reference) {
		return nil, fmt.Errorf("helm_values_invalid")
	}
	if endpoint.Scheme == "http" && endpoint.Port() == "" {
		endpoint.Host += ":80"
	}
	client := credentialHTTPClient(endpoint)
	client.Transport.(*http.Transport).Proxy = nil
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("helm_values_invalid")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("helm_values_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("helm_values_http_%d", response.StatusCode)
	}
	values, err := io.ReadAll(io.LimitReader(response.Body, managedHelmYAMLLimit+1))
	if err != nil || len(values) > managedHelmYAMLLimit {
		return nil, fmt.Errorf("helm_values_invalid")
	}
	return values, nil
}

func validateExpectedHelmImage(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("expected_image must be a complete repository:tag or repository@sha256:<digest>")
	}
	if at := strings.LastIndexByte(value, '@'); at > 0 {
		repository := value[:at]
		digest := value[at+1:]
		if strings.Contains(digest, "@") || !validHelmImageRepository(repository) || !helmImageDigestPattern.MatchString(digest) {
			return "", fmt.Errorf("invalid expected_image: expected repository:tag or repository@sha256:<digest>")
		}
		return value, nil
	}
	lastSlash := strings.LastIndexByte(value, '/')
	lastColon := strings.LastIndexByte(value, ':')
	if lastColon <= lastSlash || !helmImageTagPattern.MatchString(value[lastColon+1:]) ||
		!validHelmImageRepository(value[:lastColon]) {
		return "", fmt.Errorf("invalid expected_image: expected repository:tag or repository@sha256:<digest>")
	}
	return value, nil
}

func resolveWorkspaceHelmAssets(params api.HelmParams) (string, string, []string, error) {
	workspace, sessionRoot, err := resolveHelmSessionWorkspace(params.SessionID)
	if err != nil {
		return "", "", nil, err
	}

	chart, err := cleanWorkspaceRelativePath(params.Chart)
	if err != nil {
		return "", "", nil, fmt.Errorf("invalid workspace chart path: %w", err)
	}
	_, resolvedChart, err := resolveWorkspaceAsset(sessionRoot, chart, false)
	if err != nil {
		return "", "", nil, err
	}
	chartInfo, err := os.Stat(resolvedChart)
	if err != nil || !chartInfo.IsDir() {
		return "", "", nil, fmt.Errorf("workspace chart %q must be a directory", chart)
	}
	chartMetadata := filepath.ToSlash(filepath.Join(filepath.FromSlash(chart), "Chart.yaml"))
	if _, _, err := resolveWorkspaceAsset(sessionRoot, chartMetadata, true); err != nil {
		return "", "", nil, fmt.Errorf("workspace chart %q is invalid: %w", chart, err)
	}

	var values []string
	for _, valuePath := range params.ValueFiles {
		value, _, err := resolveWorkspaceAsset(sessionRoot, valuePath, true)
		if err != nil {
			return "", "", nil, err
		}
		values = append(values, value)
	}

	return workspace, chart, values, nil
}

func resolveHelmSessionWorkspace(sessionID string) (string, string, error) {
	workspace, err := workspacePath(sessionID)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.EvalSymlinks(workspaceRoot())
	if err != nil {
		return "", "", fmt.Errorf("resolve agent workspace root: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return "", "", fmt.Errorf("agent runtime root is not available")
	}
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve session workspace: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == "." || filepath.Dir(relative) != "." {
		return "", "", fmt.Errorf("session workspace is not bound below the agent workspace root")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("session workspace is not a directory")
	}
	return workspace, resolved, nil
}

func cleanWorkspaceRelativePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || strings.ContainsAny(value, `\`) {
		return "", fmt.Errorf("path must be relative to the session workspace")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("path traversal is not allowed")
		}
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == "." || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay inside the session workspace")
	}
	return filepath.ToSlash(cleaned), nil
}

func resolveWorkspaceAsset(sessionRoot, relative string, requireRegular bool) (string, string, error) {
	cleaned, err := cleanWorkspaceRelativePath(relative)
	if err != nil {
		return "", "", err
	}
	candidate := filepath.Join(sessionRoot, filepath.FromSlash(cleaned))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("workspace asset %q does not exist", cleaned)
		}
		return "", "", fmt.Errorf("resolve workspace asset %q: %w", cleaned, err)
	}
	if !pathWithin(sessionRoot, resolved) {
		return "", "", fmt.Errorf("workspace asset %q escapes the session workspace", cleaned)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("stat workspace asset %q: %w", cleaned, err)
	}
	if requireRegular && !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("workspace asset %q must be a regular file", cleaned)
	}
	return cleaned, resolved, nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func resolveRemoteHelmChartReference(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !isRemoteHelmChartReference(value) {
		return "", fmt.Errorf("helm chart must be an explicit https:// reference")
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
	return isRemoteHelmReference(value, "https")
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
	if command.operation() == "upgrade" {
		// Server-side dry-run preserves lookup behavior before Helm creates a revision.
		preflightArgs := make([]string, len(command.args), len(command.args)+2)
		copy(preflightArgs, command.args)
		preflightArgs = append(preflightArgs, "--dry-run=server", "--output=json")
		preflight := exec.CommandContext(ctx, "helm", preflightArgs...)
		preflight.Dir = command.cwd
		var preflightOutput bytes.Buffer
		preflight.Stdout = &preflightOutput
		var preflightStderr bytes.Buffer
		preflight.Stderr = &preflightStderr
		if err := preflight.Run(); err != nil {
			return "", fmt.Errorf("helm_preflight_failed")
		}
		var rendered struct {
			Manifest string `json:"manifest"`
		}
		if json.Unmarshal(preflightOutput.Bytes(), &rendered) != nil {
			return "", fmt.Errorf("helm_preflight_invalid_manifest")
		}
		if _, err := validateHelmRenderedImages([]byte(rendered.Manifest), command.expectedImage); err != nil {
			return "", fmt.Errorf("helm_preflight_image_mismatch")
		}
	}

	mutationArgs := append([]string(nil), command.args...)
	if command.operation() == "upgrade" {
		mutationArgs = append(mutationArgs, "--output=json")
	}
	cmd := exec.CommandContext(ctx, "helm", mutationArgs...)
	cmd.Dir = command.cwd
	// Templates/NOTES may contain lookup-derived values or encoded credentials.
	// Only structured state verification, never chart-controlled output, is public.
	var mutationOutput bytes.Buffer
	cmd.Stdout = &mutationOutput
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("helm_mutation_failed")
	}
	if command.operation() != "upgrade" {
		return "", nil
	}
	revision, err := parseHelmReleaseOutput(mutationOutput.Bytes(), command.mutationID)
	if err != nil {
		return "", fmt.Errorf("helm_mutation_invalid_result")
	}
	command.revision = revision
	verified, err := verifyHelmDeploymentState(ctx, command)
	if err != nil {
		return "", fmt.Errorf("helm_verification_failed")
	}
	return verified, nil
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
		return 0, err
	}
	if release.Version <= 0 {
		return 0, fmt.Errorf("helm release output has no positive revision")
	}
	if release.Info.Description != expectedDescription {
		return 0, fmt.Errorf("helm release output description does not match mutation")
	}
	return release.Version, nil
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
	if command.mutationID != "" && command.revision <= 0 {
		return "", fmt.Errorf("helm verification failed: mutation revision is unavailable")
	}
	manifestArgs := []string{
		"get",
		"manifest",
		command.release,
		"--namespace",
		command.namespace,
	}
	if command.revision > 0 {
		manifestArgs = append(manifestArgs, "--revision", fmt.Sprintf("%d", command.revision))
	}
	manifestCmd := exec.CommandContext(
		ctx,
		"helm",
		manifestArgs...,
	)
	manifestCmd.Dir = command.cwd
	manifest, err := manifestCmd.Output()
	if err != nil {
		return "", fmt.Errorf("helm verification failed: read final release manifest: %w", err)
	}
	deployments, err := validateHelmRenderedImages(manifest, command.expectedImage)
	if err != nil {
		return "", fmt.Errorf("helm verification failed: %w", err)
	}

	for _, expected := range deployments {
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
	if command.mutationID != "" {
		statusCmd := exec.CommandContext(ctx, "helm", "status", command.release, "--namespace", command.namespace, "--output", "json")
		statusCmd.Dir = command.cwd
		statusBytes, err := statusCmd.Output()
		if err != nil {
			return "", fmt.Errorf("helm verification failed: read current release status: %w", err)
		}
		currentRevision, err := parseHelmReleaseOutput(statusBytes, command.mutationID)
		if err != nil || currentRevision != command.revision {
			return "", fmt.Errorf("helm verification failed: release was changed by another mutation")
		}
	}
	return fmt.Sprintf("final Deployment spec verified: %d Deployment(s) observed at current generation and Ready", len(deployments)), nil
}

func validateHelmRenderedImages(manifest []byte, expectedImage string) ([]renderedHelmDeployment, error) {
	deployments, err := parseRenderedHelmDeployments(manifest)
	if err != nil {
		return nil, err
	}
	if len(deployments) == 0 {
		return nil, fmt.Errorf("final release manifest contains no Deployment")
	}
	if expectedImage == "" {
		return deployments, nil
	}
	for _, deployment := range deployments {
		for _, image := range deployment.Containers {
			if image == expectedImage {
				return deployments, nil
			}
		}
	}
	return nil, fmt.Errorf("final release manifest does not contain expected image %q", expectedImage)
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
