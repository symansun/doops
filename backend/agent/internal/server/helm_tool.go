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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
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
	// rolloutTimeout bounds how long verification waits for workloads to roll out.
	rolloutTimeout time.Duration
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
		args := []string{"upgrade", params.Release, chart, "--install", "--reset-values", "--description", mutationID}
		// --atomic rolls a failed upgrade back automatically. It is the caller's
		// choice: by default a failed revision stays in place so the operator can
		// inspect it and decide whether and where to roll back.
		if params.Atomic {
			args = append(args, "--atomic")
		}
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
		rolloutTimeout := defaultHelmRolloutTimeout
		if params.Timeout != "" {
			if parsed, err := time.ParseDuration(params.Timeout); err == nil {
				rolloutTimeout = parsed
			}
		}
		return helmCommand{
			args: append(args, common...), cwd: workspace,
			release: params.Release, namespace: params.Namespace, expectedImage: expectedImage, mutationID: mutationID,
			rolloutTimeout: rolloutTimeout,
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
	auth, _, err := authorizedHelmCredential(ctx, params)
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
	chart, _, err := fetchManagedHelmChart(ctx, auth.URL, auth.Username, auth.Password, params.Chart)
	auth.Password = ""
	auth.Username = ""
	if err != nil {
		return nil, err
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
