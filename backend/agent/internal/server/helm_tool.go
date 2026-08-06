package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
}

type helmImageValues struct {
	repository string
	tag        string
	digest     string
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
	if err := runHelmCommand(ctx, command, push); err != nil {
		return "", err
	}
	return "Helm operation complete.", nil
}

func buildHelmCommand(params api.HelmParams) (helmCommand, error) {
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.Operation = strings.TrimSpace(params.Operation)
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
	workspace, err := workspacePath(params.SessionID)
	if err != nil {
		return helmCommand{}, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return helmCommand{}, fmt.Errorf("resolve bound workspace: %w", err)
	}
	workspaceInfo, err := os.Stat(workspace)
	if err != nil || !workspaceInfo.IsDir() {
		return helmCommand{}, fmt.Errorf("bound workspace is not available")
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
		chart, err := resolveHelmWorkspacePath(workspace, params.Chart, "deploy/helm/", true)
		if err != nil {
			return helmCommand{}, err
		}
		args := []string{"upgrade", params.Release, chart, "--install", "--atomic"}
		preserveInsertAt := len(args)
		valueFiles := make([]string, 0, len(params.ValueFiles))
		for _, valuePath := range params.ValueFiles {
			resolved, err := resolveHelmWorkspacePath(workspace, strings.TrimSpace(valuePath), "deploy/", false)
			if err != nil {
				return helmCommand{}, err
			}
			valueFiles = append(valueFiles, resolved)
			args = append(args, "--values", resolved)
		}
		if err := validateHelmImageInput(workspace, chart, valueFiles, params.SetStrings); err != nil {
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

func validateHelmImageInput(
	workspace string,
	chart string,
	valueFiles []string,
	setStrings map[string]string,
) error {
	imageRepository, hasImageRepository := setStrings["image.repository"]
	imageDigest, hasImageDigest := setStrings["image.digest"]
	if hasImageRepository || hasImageDigest {
		if !hasImageRepository || !hasImageDigest {
			return fmt.Errorf(
				"upgrade-install requires both image.repository and image.digest set_strings",
			)
		}
		if !validHelmImageRepository(imageRepository) {
			return fmt.Errorf("invalid image.repository set_string")
		}
		if !helmImageDigestPattern.MatchString(imageDigest) {
			return fmt.Errorf("invalid image.digest set_string")
		}
		image, err := loadHelmImageValues(workspace, chart, valueFiles)
		if err != nil {
			return err
		}
		if image.tag != "" {
			return fmt.Errorf("digest image mode must not define image.tag in values")
		}
		return nil
	}
	if len(setStrings) != 0 {
		return fmt.Errorf("values-owned image mode does not allow set_strings")
	}

	image, err := loadHelmImageValues(workspace, chart, valueFiles)
	if err != nil {
		return err
	}
	if !validHelmImageRepository(image.repository) {
		return fmt.Errorf("values-owned image mode requires a valid image.repository")
	}
	if !validHelmReleaseTag(image.tag) {
		return fmt.Errorf("values-owned image.tag must match release-YYYYMMDD")
	}
	if image.digest != "" {
		return fmt.Errorf("values-owned image mode must not define image.digest")
	}
	return nil
}

func loadHelmImageValues(workspace string, chart string, valueFiles []string) (helmImageValues, error) {
	var image helmImageValues
	chartValues := filepath.ToSlash(filepath.Join(chart, "values.yaml"))
	paths := make([]string, 0, len(valueFiles)+1)
	if _, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(chartValues))); err == nil {
		resolved, err := resolveHelmWorkspacePath(workspace, chartValues, chart+"/", false)
		if err != nil {
			return helmImageValues{}, err
		}
		paths = append(paths, resolved)
	} else if !os.IsNotExist(err) {
		return helmImageValues{}, fmt.Errorf("inspect helm chart values: %w", err)
	}
	paths = append(paths, valueFiles...)

	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(path)))
		if err != nil {
			return helmImageValues{}, fmt.Errorf("read helm values file %s: %w", path, err)
		}
		var values map[string]interface{}
		if err := yaml.Unmarshal(content, &values); err != nil {
			return helmImageValues{}, fmt.Errorf("decode helm values file %s: %w", path, err)
		}
		rawImage, exists := values["image"]
		if !exists {
			continue
		}
		if rawImage == nil {
			image = helmImageValues{}
			continue
		}
		imageMap, ok := rawImage.(map[string]interface{})
		if !ok {
			return helmImageValues{}, fmt.Errorf("helm values file %s image must be a map", path)
		}
		for _, field := range []struct {
			key    string
			target *string
		}{
			{key: "repository", target: &image.repository},
			{key: "tag", target: &image.tag},
			{key: "digest", target: &image.digest},
		} {
			value, exists := imageMap[field.key]
			if !exists {
				continue
			}
			if value == nil {
				*field.target = ""
				continue
			}
			text, ok := value.(string)
			if !ok {
				return helmImageValues{}, fmt.Errorf(
					"helm values file %s image.%s must be a string",
					path,
					field.key,
				)
			}
			*field.target = text
		}
	}
	return image, nil
}

func validHelmReleaseTag(value string) bool {
	if !helmReleaseTagPattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("20060102", strings.TrimPrefix(value, "release-"))
	return err == nil
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

func resolveHelmWorkspacePath(root, value, requiredPrefix string, directory bool) (string, error) {
	if value == "" || filepath.IsAbs(value) {
		return "", fmt.Errorf("helm path must be a workspace-relative %s path", requiredPrefix)
	}
	clean := filepath.Clean(value)
	slash := filepath.ToSlash(clean)
	if slash == "." || strings.HasPrefix(slash, "../") || !strings.HasPrefix(slash, requiredPrefix) {
		return "", fmt.Errorf("helm path must be under %s", requiredPrefix)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("resolve helm path: %w", err)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("helm path escapes the workspace")
	}
	resolvedSlash := filepath.ToSlash(relative)
	if !strings.HasPrefix(resolvedSlash, requiredPrefix) {
		return "", fmt.Errorf("resolved helm path must remain under %s", requiredPrefix)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect helm path: %w", err)
	}
	if info.IsDir() != directory {
		if directory {
			return "", fmt.Errorf("helm chart path must be a directory")
		}
		return "", fmt.Errorf("helm values path must be a file")
	}
	return resolvedSlash, nil
}

func escapeHelmSetString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, ",", `\,`, "=", `\=`)
	return replacer.Replace(value)
}

func runHelmCommand(ctx context.Context, command helmCommand, push notificationSender) error {
	var stdin []byte
	if command.preserveReleaseValues {
		existingValues, found, err := prepareExistingHelmValues(ctx, command)
		if err != nil {
			return err
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
			return fmt.Errorf(
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
		return fmt.Errorf("start helm: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("start helm: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start helm: %w", err)
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
		return fmt.Errorf(
			"helm %s failed: %w: %s",
			strings.Join(command.args, " "),
			err,
			strings.TrimSpace(stderrCapture.String()),
		)
	}
	return nil
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
