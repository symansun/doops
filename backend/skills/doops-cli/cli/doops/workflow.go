package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var workflowInlineCredentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9]{20,255}\b`),
	regexp.MustCompile(`(?i)\bgithub_pat_[A-Za-z0-9_]{20,255}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,255}\b`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,255}\b`),
	regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{16,255}\b`),
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36,255}\b`),
}

type WorkspaceSourceIdentity struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Branch     string `json:"branch,omitempty"`
	TreeDigest string `json:"treeDigest,omitempty"`
}

const workspaceManifestAPIVersion = "doops.sh/v2"

type workflowCommand struct {
	File   string
	Target string
	Model  string
	Inputs map[string]string
	DryRun bool
}

type workflowAgentCaller interface {
	CallStructured(toolName string, arguments map[string]interface{}, destination interface{}) error
}

var resolveWorkflowSource = workflowDeclaredSource

var openWorkflowAgent = func(
	server Server,
	sessionStore *SessionStore,
	sessionID string,
	verbose bool,
) (workflowAgentCaller, func()) {
	client := NewMCPClient(server, sessionStore, sessionID, verbose)
	client.Token = ResolveToken(server.Name, server.Token)
	return client, client.Close
}

func runCICDCommand(
	ctx context.Context,
	args []string,
	servers []Server,
	configErr error,
	sessionStore *SessionStore,
	sessionID string,
	verbose bool,
) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) != "run" {
		return fmt.Errorf("only `cicd run` is supported; workflow validation and orchestration belong to doops-cicd")
	}
	request := workflowCommand{Inputs: map[string]string{}}
	sets := workflowSetFlags(request.Inputs)
	flags := flag.NewFlagSet("cicd run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&request.File, "f", "", "workflow YAML file")
	flags.StringVar(&request.File, "file", "", "workflow YAML file")
	flags.StringVar(&request.Target, "target", "", "configured DoOps target")
	flags.StringVar(&request.Model, "model", "", "model to use for trusted reconciliation")
	flags.Var(sets, "set", "workflow input override in key=value form")
	flags.BoolVar(&request.DryRun, "dry-run", false, "observe without mutation")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(request.File) == "" {
		return fmt.Errorf("-f workflow YAML file is required")
	}
	if strings.TrimSpace(request.Target) == "" {
		return fmt.Errorf("-target is required; the CLI must not infer a deployment target")
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("-session is required for `cicd run`")
	}
	if configErr != nil {
		return configErr
	}
	server := findServer(servers, request.Target)
	if server == nil {
		return fmt.Errorf("target %q not found; configure it with `doops add`", request.Target)
	}
	_, workflowPath, err := workflowPathInGitRepository(request.File)
	if err != nil {
		return err
	}
	if err := validateWorkflowInputs(request.Inputs); err != nil {
		return err
	}
	if strings.TrimSpace(server.Gateway) == "" {
		return fmt.Errorf("target %q must use a configured DoOps gateway", server.Name)
	}
	manifestAPIVersion, err := workflowExecutionAPIVersion(request.File)
	if err != nil {
		return err
	}
	client, closeClient := openWorkflowAgent(*server, sessionStore, sessionID, verbose)
	defer closeClient()

	sourceRepository, sourceBranch, err := resolveWorkflowSource(request.File, request.Inputs)
	if err != nil {
		return fmt.Errorf("resolve workflow source: %w", err)
	}

	executionMode := "apply"
	operation := "apply"
	if request.DryRun {
		executionMode = "dry-run"
		operation = "ask"
	}
	credentialRun, err := prepareWorkflowCredentials(ctx, *server, ResolveToken(server.Name, server.Token), credentialPrepareRequest{
		Cluster:          server.Cluster,
		Instance:         server.Instance,
		SessionID:        sessionID,
		WorkflowPath:     workflowPath,
		SourceRepository: sourceRepository,
		SourceBranch:     sourceBranch,
		Inputs:           request.Inputs,
		Mode:             executionMode,
	})
	if err != nil {
		return fmt.Errorf("credential prepare: %w", err)
	}
	runID, err := generateWorkflowRunID(sessionID)
	if err != nil {
		return fmt.Errorf("generate workflow run id: %w", err)
	}
	instructionEnvelope := map[string]interface{}{
		"task":          "execute-doops-cicd-workflow",
		"skill":         "$doops-cicd",
		"executionMode": executionMode,
		"runId":         runID,
		"workflowPath":  workflowPath,
		"source": map[string]string{
			"repository": sourceRepository,
			"branch":     sourceBranch,
		},
		"inputs":        request.Inputs,
		"credentialRun": credentialRun,
		"resultContract": map[string]interface{}{
			"apiVersion": manifestAPIVersion,
			"kind":       "DeploymentRun",
		},
	}
	instruction, err := json.Marshal(instructionEnvelope)
	if err != nil {
		return fmt.Errorf("encode doops-cicd instruction: %w", err)
	}
	var result json.RawMessage
	callArguments := map[string]interface{}{
		"instruction":     string(instruction),
		"response_format": "json",
		"operation":       operation,
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		callArguments["model"] = model
	}
	if err := client.CallStructured("doops_agent_prompt", callArguments, &result); err != nil {
		return fmt.Errorf("ask doops-cicd: %w", err)
	}
	output := string(result)
	if err := validateWorkflowResult(output, manifestAPIVersion, executionMode); err != nil {
		return fmt.Errorf("doops-cicd returned invalid result: %w", err)
	}
	if strings.TrimSpace(output) != "" {
		fmt.Print(output)
		if !strings.HasSuffix(output, "\n") {
			fmt.Println()
		}
	}
	return nil
}

func workflowDeclaredSource(path string, inputs map[string]string) (repository, branch string, err error) {
	root, _, err := workflowPathInGitRepository(path)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read workflow source: %w", err)
	}
	var declaration struct {
		APIVersion   string `yaml:"apiVersion"`
		Kind         string `yaml:"kind"`
		Environments map[string]struct {
			Source struct {
				Repository string `yaml:"repository"`
				Branch     string `yaml:"branch"`
			} `yaml:"source"`
			Services map[string]string `yaml:"services"`
		} `yaml:"environments"`
		Spec struct {
			ConfigurationSource string `yaml:"configurationSource"`
			Source              struct {
				Repository string `yaml:"repository"`
				Branch     string `yaml:"branch"`
			} `yaml:"source"`
			Release struct {
				Source struct {
					Repository string `yaml:"repository"`
					Branch     string `yaml:"branch"`
				} `yaml:"source"`
			} `yaml:"release"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &declaration); err != nil {
		return "", "", fmt.Errorf("decode workflow source: %w", err)
	}
	if declaration.APIVersion == "doops.sh/v3" && declaration.Kind == "EnvironmentRegistry" {
		environmentName := strings.TrimSpace(inputs["environment"])
		serviceName := strings.TrimSpace(inputs["service"])
		if environmentName == "" || serviceName == "" {
			return "", "", fmt.Errorf("v3 EnvironmentRegistry requires environment and service inputs")
		}
		environment, ok := declaration.Environments[environmentName]
		if !ok {
			return "", "", fmt.Errorf("environment %q is not declared", environmentName)
		}
		if servicePath, ok := environment.Services[serviceName]; !ok || strings.TrimSpace(servicePath) == "" {
			return "", "", fmt.Errorf("service %q is not declared for environment %q", serviceName, environmentName)
		}
		repository = strings.TrimSpace(environment.Source.Repository)
		branch = strings.TrimSpace(environment.Source.Branch)
	} else {
		repository = strings.TrimSpace(declaration.Spec.Source.Repository)
		branch = strings.TrimSpace(declaration.Spec.Source.Branch)
		if repository == "" {
			repository = strings.TrimSpace(declaration.Spec.Release.Source.Repository)
		}
		if branch == "" {
			branch = strings.TrimSpace(declaration.Spec.Release.Source.Branch)
		}
	}
	if repository == "" || branch == "" {
		configurationSource := strings.TrimSpace(declaration.Spec.ConfigurationSource)
		if configurationSource != "" {
			configurationPath := filepath.Clean(filepath.Join(root, filepath.FromSlash(configurationSource)))
			relative, relErr := filepath.Rel(root, configurationPath)
			if relErr != nil || relative == "." || relative == ".." ||
				strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return "", "", fmt.Errorf("configurationSource %q escapes Git repository", configurationSource)
			}
			configurationData, readErr := os.ReadFile(configurationPath)
			if readErr != nil {
				return "", "", fmt.Errorf("read workflow configurationSource %q: %w", configurationSource, readErr)
			}
			var configuration struct {
				ArtifactContract struct {
					SourceRepository string `yaml:"sourceRepository"`
					SourceBranch     string `yaml:"sourceBranch"`
				} `yaml:"artifactContract"`
			}
			if decodeErr := yaml.Unmarshal(configurationData, &configuration); decodeErr != nil {
				return "", "", fmt.Errorf("decode workflow configurationSource %q: %w", configurationSource, decodeErr)
			}
			if repository == "" {
				repository = strings.TrimSpace(configuration.ArtifactContract.SourceRepository)
			}
			if branch == "" {
				branch = strings.TrimSpace(configuration.ArtifactContract.SourceBranch)
			}
		}
	}
	if repository == "" || branch == "" {
		return "", "", fmt.Errorf("workflow declaration requires source.repository and source.branch")
	}
	if err := validateWorkflowSourceRepository(repository); err != nil {
		return "", "", err
	}
	return repository, branch, nil
}

func validateWorkflowSourceRepository(repository string) error {
	parsed, err := url.Parse(strings.TrimSpace(repository))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("workflow source repository must be an HTTPS URL without inline credentials, query, or fragment")
	}
	return nil
}

func workflowAPIVersion(path string) (string, error) {
	version, _, err := workflowIdentity(path)
	if err != nil {
		return "", err
	}
	return version, nil
}

func workflowExecutionAPIVersion(path string) (string, error) {
	version, kind, err := workflowIdentity(path)
	if err != nil {
		return "", err
	}
	if (version == workspaceManifestAPIVersion && kind == "DeploymentTemplate") ||
		(version == "doops.sh/v3" && kind == "EnvironmentRegistry") {
		return version, nil
	}
	return "", fmt.Errorf(
		"trusted cicd execution requires %s DeploymentTemplate or doops.sh/v3 EnvironmentRegistry, got %s %s",
		workspaceManifestAPIVersion,
		version,
		kind,
	)
}

func workflowIdentity(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read workflow apiVersion: %w", err)
	}
	var identity struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &identity); err != nil {
		return "", "", fmt.Errorf("decode workflow identity: %w", err)
	}
	identity.APIVersion = strings.TrimSpace(identity.APIVersion)
	identity.Kind = strings.TrimSpace(identity.Kind)
	if identity.APIVersion != "doops.sh/v2" && identity.APIVersion != "doops.sh/v3" {
		if identity.APIVersion == "" {
			return "", "", fmt.Errorf("workflow apiVersion is required")
		}
		return "", "", fmt.Errorf("unsupported workflow apiVersion %q", identity.APIVersion)
	}
	if identity.Kind == "" {
		return "", "", fmt.Errorf("workflow kind is required")
	}
	return identity.APIVersion, identity.Kind, nil
}

func generateWorkflowRunID(sessionID string) (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return strings.TrimSpace(sessionID) + "-" + hex.EncodeToString(random), nil
}

type workflowSetFlags map[string]string

func (f workflowSetFlags) String() string {
	if len(f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f))
	for key, value := range f {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func (f workflowSetFlags) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("--set must be key=value")
	}
	f[strings.TrimSpace(key)] = val
	return nil
}

func validateWorkflowInputs(inputs map[string]string) error {
	for key, value := range inputs {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(key) != key {
			return fmt.Errorf("workflow input name must be non-empty and trimmed")
		}
		if workflowInputContainsCredentialMaterial(key) {
			return fmt.Errorf("sensitive workflow input %q is forbidden; use credentialRef", key)
		}
		if workflowInputValueContainsCredentialMaterial(value) {
			return fmt.Errorf("workflow input %q contains inline credential material; use credentialRef", key)
		}
	}
	return nil
}

func workflowInputValueContainsCredentialMaterial(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, " private key-----") {
		return true
	}
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "basic ") ||
		strings.Contains(lower, "sk-") {
		return true
	}
	for _, pattern := range workflowInlineCredentialPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	if strings.Contains(value, "://") || strings.Contains(value, "?") {
		parsed, err := url.Parse(value)
		if err == nil {
			if parsed.User != nil {
				return true
			}
			for key, values := range parsed.Query() {
				if workflowInputContainsCredentialMaterial(key) {
					for _, queryValue := range values {
						if strings.TrimSpace(queryValue) != "" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func workflowInputContainsCredentialMaterial(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '.', ' ':
			return -1
		default:
			return r
		}
	}, strings.ToLower(strings.TrimSpace(key)))
	if normalized == "auth" || normalized == "authorization" || normalized == "cookie" ||
		normalized == "data" || normalized == "stringdata" || normalized == "secret" ||
		normalized == "sig" {
		return true
	}
	for _, suffix := range []string{
		"token", "password", "privatekey", "clientsecret", "apikey", "accesskey", "secretkey",
		"credential", "pat", "passphrase", "dockerconfigjson", "sshidentity", "signature",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func workflowPathInGitRepository(path string) (root, relative string, err error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve workflow path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", "", fmt.Errorf("read workflow file: %w", err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("workflow path must be a file")
	}
	for directory := filepath.Dir(absolute); ; directory = filepath.Dir(directory) {
		if _, statErr := os.Stat(filepath.Join(directory, ".git")); statErr == nil {
			relative, relErr := filepath.Rel(directory, absolute)
			if relErr != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
				return "", "", fmt.Errorf("workflow path escapes Git repository")
			}
			return directory, filepath.ToSlash(relative), nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", "", fmt.Errorf("workflow file %q is not inside a Git repository", path)
		}
	}
}

func validateWorkflowResult(
	output,
	apiVersion,
	executionMode string,
) error {
	var result struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Spec       struct {
			Mode string `json:"mode"`
		} `json:"spec"`
		Status struct {
			Phase         string          `json:"phase"`
			MutationCount *int            `json:"mutationCount"`
			Capabilities  json.RawMessage `json:"capabilities"`
		} `json:"status"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode DeploymentRun: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("DeploymentRun must be exactly one JSON object")
	}
	if result.APIVersion != apiVersion || result.Kind != "DeploymentRun" {
		return fmt.Errorf("result must be %s DeploymentRun", apiVersion)
	}
	if result.Spec.Mode != executionMode {
		return fmt.Errorf("result mode %q does not match requested mode %q", result.Spec.Mode, executionMode)
	}
	switch result.Status.Phase {
	case "planned", "converged", "blocked", "failed", "outcome-unknown":
	default:
		return fmt.Errorf("unsupported result phase %q", result.Status.Phase)
	}
	if result.Status.MutationCount == nil || *result.Status.MutationCount < 0 {
		return fmt.Errorf("result mutationCount is required and must be non-negative")
	}
	if executionMode == "dry-run" && *result.Status.MutationCount != 0 {
		return fmt.Errorf("dry-run result must have mutationCount=0")
	}
	if len(result.Status.Capabilities) == 0 || string(result.Status.Capabilities) == "null" {
		return fmt.Errorf("result capabilities snapshot is required")
	}
	var capabilities map[string]interface{}
	if err := json.Unmarshal(result.Status.Capabilities, &capabilities); err != nil {
		return fmt.Errorf("decode result capabilities: %w", err)
	}
	if len(capabilities) == 0 {
		return fmt.Errorf("result capabilities snapshot must not be empty")
	}
	return nil
}

func validateWorkflowJSONValue(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("is required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("must contain exactly one JSON value")
		}
		return err
	}
	return nil
}
