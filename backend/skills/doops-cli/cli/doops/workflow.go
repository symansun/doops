package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type WorkspaceSourceIdentity struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Branch     string `json:"branch,omitempty"`
	TreeDigest string `json:"treeDigest,omitempty"`
}

const workspaceManifestAPIVersion = "doops.sh/v3"

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
	if workflowPath != "deploy/environments.yaml" {
		return fmt.Errorf("v3 deployment root must be deploy/environments.yaml")
	}
	if err := validateWorkflowInputs(request.Inputs); err != nil {
		return err
	}
	sourceRepository, sourceBranch, err := workflowDeclaredSource(request.File, request.Inputs)
	if err != nil {
		return fmt.Errorf("resolve workflow source: %w", err)
	}
	sourceRevision, err := resolveWorkflowSourceRevision(sourceRepository, sourceBranch)
	if err != nil {
		return fmt.Errorf("resolve workflow source revision: %w", err)
	}
	if strings.TrimSpace(server.Gateway) == "" {
		return fmt.Errorf("target %q must use a configured DoOps gateway", server.Name)
	}
	client, closeClient := openWorkflowAgent(*server, sessionStore, sessionID, verbose)
	defer closeClient()

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
		SourceRevision:   sourceRevision,
		Mode:             executionMode,
	})
	if err != nil {
		return fmt.Errorf("credential prepare: %w", err)
	}
	instructionEnvelope := map[string]interface{}{
		"task":            "execute-doops-cicd",
		"skill":           "$doops-cicd",
		"executionMode":   executionMode,
		"manifestPath":    workflowPath,
		"workspaceCommit": sourceRevision,
		"source": map[string]string{
			"repository": sourceRepository,
			"branch":     sourceBranch,
		},
		"inputs":        request.Inputs,
		"credentialRun": credentialRun,
	}
	instruction, err := json.Marshal(instructionEnvelope)
	if err != nil {
		return fmt.Errorf("encode doops-cicd instruction: %w", err)
	}
	var result json.RawMessage
	callArguments := map[string]interface{}{
		"instruction":      string(instruction),
		"response_format":  "json",
		"operation":        operation,
		"workspace_commit": sourceRevision,
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		callArguments["model"] = model
	}
	if err := client.CallStructured("doops_agent_prompt", callArguments, &result); err != nil {
		return fmt.Errorf("ask doops-cicd: %w", err)
	}
	output := string(result)
	if strings.TrimSpace(output) != "" {
		fmt.Print(output)
		if !strings.HasSuffix(output, "\n") {
			fmt.Println()
		}
	}
	return nil
}

func workflowDeclaredSource(path string, inputs map[string]string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read workflow source: %w", err)
	}
	var registry struct {
		APIVersion   string `yaml:"apiVersion"`
		Kind         string `yaml:"kind"`
		Environments map[string]struct {
			Source struct {
				Repository string `yaml:"repository"`
				Branch     string `yaml:"branch"`
			} `yaml:"source"`
			Services map[string]string `yaml:"services"`
		} `yaml:"environments"`
	}
	if err := yaml.Unmarshal(data, &registry); err != nil {
		return "", "", fmt.Errorf("decode workflow source: %w", err)
	}
	if registry.APIVersion != workspaceManifestAPIVersion || registry.Kind != "EnvironmentRegistry" {
		return "", "", fmt.Errorf("cicd run requires doops.sh/v3 EnvironmentRegistry")
	}
	environmentName := strings.TrimSpace(inputs["environment"])
	serviceName := strings.TrimSpace(inputs["service"])
	if environmentName == "" || serviceName == "" {
		return "", "", fmt.Errorf("environment and service inputs are required")
	}
	environment, ok := registry.Environments[environmentName]
	if !ok {
		return "", "", fmt.Errorf("environment %q is not declared", environmentName)
	}
	if servicePath, ok := environment.Services[serviceName]; !ok || strings.TrimSpace(servicePath) == "" {
		return "", "", fmt.Errorf("service %q is not declared for environment %q", serviceName, environmentName)
	}
	repository := strings.TrimSpace(environment.Source.Repository)
	branch := strings.TrimSpace(environment.Source.Branch)
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("workflow source repository must be an HTTPS URL without inline credentials, query, or fragment")
	}
	if branch == "" {
		return "", "", fmt.Errorf("workflow source branch is required")
	}
	return repository, branch, nil
}

func resolveWorkflowSourceRevision(repository, branch string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "ls-remote", "--refs", "--", repository, "refs/heads/"+branch)
	command.Env = append(
		os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ALLOW_PROTOCOL=https",
	)
	output, err := command.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("workflow source revision lookup timed out")
	}
	if err != nil {
		return "", fmt.Errorf("workflow source revision lookup failed: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 || !validWorkspaceCommit(fields[0]) {
		return "", fmt.Errorf("workflow source branch did not resolve to one immutable commit")
	}
	return fields[0], nil
}

func validateWorkflowInputs(inputs map[string]string) error {
	for key, value := range inputs {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(key) != key ||
			strings.TrimSpace(value) != value {
			return fmt.Errorf("workflow inputs must use trimmed non-empty keys and values")
		}
	}
	return nil
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
