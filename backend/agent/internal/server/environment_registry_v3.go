package server

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

type credentialEnvironmentRegistryV3 struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Environments map[string]credentialEnvironmentRegistryV3Environment `yaml:"environments"`
}

type credentialEnvironmentRegistryV3Environment struct {
	Target struct {
		Name     string `yaml:"name"`
		Cluster  string `yaml:"cluster"`
		Instance string `yaml:"instance"`
		Session  string `yaml:"session"`
	} `yaml:"target"`
	Source struct {
		Repository string `yaml:"repository"`
		Branch     string `yaml:"branch"`
		Build      string `yaml:"build"`
	} `yaml:"source"`
	Helm struct {
		Release   string `yaml:"release"`
		Namespace string `yaml:"namespace"`
		Chart     string `yaml:"chart"`
		Values    string `yaml:"values"`
		Atomic    bool   `yaml:"atomic"`
		Wait      bool   `yaml:"wait"`
	} `yaml:"helm"`
	Artifacts struct {
		TargetRegistry         string                         `yaml:"targetRegistry"`
		SourceCredentialRef    credentialEnvironmentSecretRef `yaml:"sourceCredentialRef"`
		TargetCredentialRef    credentialEnvironmentSecretRef `yaml:"targetCredentialRef"`
		RuntimeImagePullSecret credentialEnvironmentSecretRef `yaml:"runtimeImagePullSecret"`
	} `yaml:"artifacts"`
	Services     map[string]string `yaml:"services"`
	HealthChecks []string          `yaml:"healthChecks"`
}

type credentialEnvironmentRegistryV3Selection struct {
	Registry        credentialEnvironmentRegistryV3
	EnvironmentName string
	ServiceName     string
	Environment     credentialEnvironmentRegistryV3Environment
	ServicePath     string
	Service         credentialEnvironmentService
}

func parseCredentialEnvironmentRegistryV3(
	root, workflowPath string,
	data []byte,
	inputs map[string]string,
) (credentialParsedWorkflow, error) {
	selection, err := selectCredentialEnvironmentRegistryV3(root, workflowPath, data, inputs)
	if err != nil {
		return credentialParsedWorkflow{}, err
	}
	var template credentialDeploymentTemplate
	template.APIVersion = selection.Registry.APIVersion
	template.Kind = selection.Registry.Kind
	template.Metadata.Name = strings.Join(
		[]string{selection.Registry.Metadata.Name, selection.EnvironmentName, selection.ServiceName},
		"-",
	)
	required := true
	template.Spec.Parameters = map[string]credentialTemplateParameter{
		"environment": {Required: &required},
		"service":     {Required: &required},
		"reason":      {},
	}
	template.Spec.Application = selection.Registry.Metadata.Name
	template.Spec.Environment = selection.EnvironmentName
	template.Spec.Release.Source.Repository = selection.Environment.Source.Repository
	template.Spec.Release.Source.Branch = selection.Environment.Source.Branch
	template.Spec.CredentialRefs, err = credentialEnvironmentRegistryV3References(selection)
	if err != nil {
		return credentialParsedWorkflow{}, err
	}
	return credentialParsedWorkflow{
		Template: template,
		Environment: &credentialEnvironmentContext{
			Target: credentialConfigurationTarget{
				Name:     selection.Environment.Target.Name,
				Cluster:  selection.Environment.Target.Cluster,
				Instance: selection.Environment.Target.Instance,
			},
			SourceRepository: selection.Environment.Source.Repository,
			SourceBranch:     selection.Environment.Source.Branch,
		},
	}, nil
}

func credentialEnvironmentRegistryV3References(
	selection credentialEnvironmentRegistryV3Selection,
) ([]credentialTemplateCredentialRef, error) {
	environment := selection.Environment
	if credentialEnvironmentSecretRefDeclared(environment.Artifacts.SourceCredentialRef) {
		return nil, invalidCredentialDeclaration(
			fmt.Errorf("environment %q sourceCredentialRef is not supported by the v3 materialization contract", selection.EnvironmentName),
		)
	}
	if credentialEnvironmentSecretRefDeclared(environment.Artifacts.TargetCredentialRef) {
		return nil, invalidCredentialDeclaration(
			fmt.Errorf("environment %q targetCredentialRef is not supported by the v3 materialization contract", selection.EnvironmentName),
		)
	}
	runtimeRef := environment.Artifacts.RuntimeImagePullSecret
	if !credentialEnvironmentSecretRefDeclared(runtimeRef) {
		return nil, nil
	}
	workloadParts := strings.SplitN(selection.Service.Spec.Helm.Workload, "/", 2)
	repository, err := credentialRegistryRepository(selection.Service.Spec.Artifact.Target)
	if err != nil {
		return nil, invalidCredentialDeclaration(err)
	}
	return []credentialTemplateCredentialRef{{
		Name:               runtimeRef.Name,
		Use:                CredentialUseImagePull,
		Namespace:          environment.Helm.Namespace,
		RegistryRepository: repository,
		RegistryReference:  selection.Service.Spec.Artifact.Digest,
		Workload: credentialTemplateWorkload{
			Kind: workloadParts[0],
			Name: workloadParts[1],
		},
	}}, nil
}

func credentialRegistryRepository(reference string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(reference), "/", 2)
	if len(parts) != 2 ||
		(!strings.Contains(parts[0], ".") &&
			!strings.Contains(parts[0], ":") &&
			parts[0] != "localhost") ||
		strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("registry artifact %q must include registry host and repository", reference)
	}
	return parts[1], nil
}

func selectCredentialEnvironmentRegistryV3(
	root, workflowPath string,
	data []byte,
	inputs map[string]string,
) (credentialEnvironmentRegistryV3Selection, error) {
	relative, err := credentialDeclarationRelativePath(workflowPath)
	if err != nil {
		return credentialEnvironmentRegistryV3Selection{}, err
	}
	if filepath.ToSlash(relative) != "deploy/environments.yaml" {
		return credentialEnvironmentRegistryV3Selection{}, invalidCredentialDeclaration(
			errors.New("v3 EnvironmentRegistry path must be \"deploy/environments.yaml\""),
		)
	}
	var registry credentialEnvironmentRegistryV3
	if err := strictCredentialYAMLDecode(data, &registry); err != nil {
		return credentialEnvironmentRegistryV3Selection{}, invalidCredentialDeclaration(err)
	}
	if registry.APIVersion != "doops.sh/v3" || registry.Kind != "EnvironmentRegistry" ||
		strings.TrimSpace(registry.Metadata.Name) == "" || len(registry.Environments) == 0 {
		return credentialEnvironmentRegistryV3Selection{}, invalidCredentialDeclaration(
			errors.New("invalid v3 EnvironmentRegistry identity"),
		)
	}
	environmentName := strings.TrimSpace(inputs["environment"])
	serviceName := strings.TrimSpace(inputs["service"])
	if environmentName == "" || serviceName == "" {
		return credentialEnvironmentRegistryV3Selection{}, invalidCredentialDeclaration(
			errors.New("v3 EnvironmentRegistry requires environment and service inputs"),
		)
	}
	environment, ok := registry.Environments[environmentName]
	if !ok {
		return credentialEnvironmentRegistryV3Selection{}, invalidCredentialDeclaration(
			fmt.Errorf("environment %q is not declared", environmentName),
		)
	}
	servicePath, ok := environment.Services[serviceName]
	if !ok || strings.TrimSpace(servicePath) == "" {
		return credentialEnvironmentRegistryV3Selection{}, invalidCredentialDeclaration(
			fmt.Errorf("service %q is not declared for environment %q", serviceName, environmentName),
		)
	}
	if err := validateCredentialEnvironmentRegistryV3Environment(environmentName, environment); err != nil {
		return credentialEnvironmentRegistryV3Selection{}, err
	}
	service, err := validateCredentialEnvironmentRegistryV3Service(
		root,
		environmentName,
		serviceName,
		servicePath,
		environment,
	)
	if err != nil {
		return credentialEnvironmentRegistryV3Selection{}, err
	}
	return credentialEnvironmentRegistryV3Selection{
		Registry: registry, EnvironmentName: environmentName, ServiceName: serviceName,
		Environment: environment, ServicePath: servicePath, Service: service,
	}, nil
}

func validateCredentialEnvironmentRegistryV3Environment(
	name string,
	environment credentialEnvironmentRegistryV3Environment,
) error {
	if strings.TrimSpace(environment.Target.Name) == "" ||
		strings.TrimSpace(environment.Target.Cluster) == "" ||
		strings.TrimSpace(environment.Target.Instance) == "" ||
		strings.TrimSpace(environment.Target.Session) == "" ||
		strings.TrimSpace(environment.Source.Repository) == "" ||
		strings.TrimSpace(environment.Source.Branch) == "" ||
		strings.TrimSpace(environment.Source.Build) == "" ||
		strings.TrimSpace(environment.Helm.Release) == "" ||
		strings.TrimSpace(environment.Helm.Namespace) == "" ||
		strings.TrimSpace(environment.Helm.Chart) == "" ||
		strings.TrimSpace(environment.Helm.Values) == "" ||
		!environment.Helm.Atomic || !environment.Helm.Wait ||
		strings.TrimSpace(environment.Artifacts.TargetRegistry) == "" ||
		len(environment.Services) == 0 || len(environment.HealthChecks) == 0 {
		return invalidCredentialDeclaration(
			fmt.Errorf("environment %q is incomplete", name),
		)
	}
	for label, reference := range map[string]credentialEnvironmentSecretRef{
		"sourceCredentialRef":    environment.Artifacts.SourceCredentialRef,
		"targetCredentialRef":    environment.Artifacts.TargetCredentialRef,
		"runtimeImagePullSecret": environment.Artifacts.RuntimeImagePullSecret,
	} {
		if err := validateCredentialEnvironmentSecretRef(name, label, reference); err != nil {
			return err
		}
	}
	for index, value := range environment.HealthChecks {
		parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return invalidCredentialDeclaration(
				fmt.Errorf("environment %q healthChecks[%d] is invalid", name, index),
			)
		}
	}
	return nil
}

func validateCredentialEnvironmentRegistryV3Service(
	root, environmentName, serviceName, servicePath string,
	environment credentialEnvironmentRegistryV3Environment,
) (credentialEnvironmentService, error) {
	serviceRelative, err := credentialDeclarationRelativePath(servicePath)
	if err != nil {
		return credentialEnvironmentService{}, err
	}
	expectedDirectory := filepath.ToSlash(filepath.Join("deploy", "environments", environmentName, "services"))
	if filepath.ToSlash(filepath.Dir(serviceRelative)) != expectedDirectory {
		return credentialEnvironmentService{}, invalidCredentialDeclaration(
			fmt.Errorf("environment service %q must be under %q", serviceName, expectedDirectory),
		)
	}
	data, err := readCredentialDeclarationFile(root, servicePath)
	if err != nil {
		return credentialEnvironmentService{}, err
	}
	if _, err := credentialYAMLDocument(data); err != nil {
		return credentialEnvironmentService{}, err
	}
	var service credentialEnvironmentService
	if err := strictCredentialYAMLDecode(data, &service); err != nil {
		return credentialEnvironmentService{}, invalidCredentialDeclaration(err)
	}
	if service.APIVersion != "doops.sh/v3" ||
		service.Kind != "EnvironmentService" ||
		strings.TrimSpace(service.Metadata.Name) == "" ||
		filepath.Base(filepath.ToSlash(serviceRelative)) != service.Metadata.Name+".yaml" ||
		service.Metadata.Environment != environmentName ||
		strings.TrimSpace(service.Spec.EnvironmentDefinition) != "" ||
		strings.TrimSpace(service.Spec.Artifact.Source) == "" ||
		strings.TrimSpace(service.Spec.Artifact.Target) == "" ||
		!strings.HasPrefix(
			service.Spec.Artifact.Target,
			strings.TrimRight(environment.Artifacts.TargetRegistry, "/")+"/",
		) ||
		service.Spec.Artifact.Identity != "sha256-digest" ||
		!validCredentialSHA256Digest(service.Spec.Artifact.Digest) ||
		service.Spec.Helm.Chart != environment.Helm.Chart ||
		service.Spec.Helm.Values != environment.Helm.Values ||
		strings.TrimSpace(service.Spec.Helm.Value) == "" ||
		!strings.HasPrefix(service.Spec.Helm.Workload, "deployment/") ||
		strings.TrimSpace(strings.TrimPrefix(service.Spec.Helm.Workload, "deployment/")) == "" ||
		strings.TrimSpace(service.Spec.Helm.Container) == "" ||
		service.Spec.Helm.Replicas < 1 ||
		service.Spec.Verification.MinReadyReplicas < 1 ||
		service.Spec.Verification.MinReadyReplicas > service.Spec.Helm.Replicas {
		return credentialEnvironmentService{}, invalidCredentialDeclaration(
			fmt.Errorf("environment service %q is incomplete or inconsistent", serviceName),
		)
	}
	if err := validateCredentialServiceDefinitionV3(root, serviceName, service); err != nil {
		return credentialEnvironmentService{}, err
	}
	var definition credentialServiceDefinitionV3
	definitionData, err := readCredentialDeclarationFile(root, service.Spec.ServiceDefinition)
	if err != nil {
		return credentialEnvironmentService{}, err
	}
	if err := strictCredentialYAMLDecode(definitionData, &definition); err != nil {
		return credentialEnvironmentService{}, invalidCredentialDeclaration(err)
	}
	if definition.Spec.BuildManifest != environment.Source.Build {
		return credentialEnvironmentService{}, invalidCredentialDeclaration(
			fmt.Errorf("service %q buildManifest does not match environment source.build", serviceName),
		)
	}
	return service, nil
}

func credentialRegistryParent(repository string) string {
	repository = strings.TrimRight(strings.TrimSpace(repository), "/")
	if index := strings.LastIndex(repository, "/"); index > 0 {
		return repository[:index]
	}
	return repository
}
