package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	ErrCredentialDeclarationInvalid = errors.New("credential declaration is invalid")
	ErrInlineCredentialMaterial     = errors.New("inline credential material is forbidden")
)

type CredentialPlan struct {
	Template       string
	Project        string
	Environment    string
	Target         string
	Cluster        string
	Instance       string
	CredentialRefs []CredentialPlanReference
	BundleRefs     []string
}

type CredentialPlanReference struct {
	CredentialID       string
	Name               string
	Use                CredentialUse
	Namespace          string
	Workload           CredentialPlanWorkload
	RegistryRepository string
	RegistryReference  string
	RequiredKeys       []string
}

type CredentialPlanWorkload struct {
	Kind string
	Name string
}

type credentialDeploymentTemplate struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
	} `yaml:"metadata"`
	Spec struct {
		Parameters           map[string]credentialTemplateParameter `yaml:"parameters,omitempty"`
		Application          string                                 `yaml:"application"`
		Release              credentialTemplateRelease              `yaml:"release,omitempty"`
		Environment          string                                 `yaml:"environment"`
		ConfigurationSource  string                                 `yaml:"configurationSource"`
		CredentialRefs       []credentialTemplateCredentialRef      `yaml:"credentialRefs,omitempty"`
		CredentialBundleRefs []credentialTemplateBundleRef          `yaml:"credentialBundleRefs,omitempty"`
	} `yaml:"spec"`
}

type credentialDocumentIdentity struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

type credentialSemanticRelease struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
	} `yaml:"metadata"`
	Spec struct {
		Parameters           map[string]credentialTemplateParameter `yaml:"parameters,omitempty"`
		Application          string                                 `yaml:"application"`
		Release              credentialSemanticReleaseIdentity      `yaml:"release"`
		Environment          string                                 `yaml:"environment"`
		ConfigurationSource  string                                 `yaml:"configurationSource"`
		ServiceCatalog       map[string]credentialSemanticService   `yaml:"serviceCatalog"`
		Flow                 []credentialSemanticFlow               `yaml:"flow"`
		FailurePolicy        credentialSemanticFailurePolicy        `yaml:"failurePolicy,omitempty"`
		CredentialRefs       []credentialTemplateCredentialRef      `yaml:"credentialRefs,omitempty"`
		CredentialBundleRefs []credentialTemplateBundleRef          `yaml:"credentialBundleRefs,omitempty"`
	} `yaml:"spec"`
}

type credentialSemanticReleaseIdentity struct {
	Revision         string `yaml:"revision"`
	Version          string `yaml:"version"`
	Reason           string `yaml:"reason"`
	SelectedServices string `yaml:"selectedServices"`
	Strategy         string `yaml:"strategy"`
	ServiceSelection string `yaml:"serviceSelection"`
	ArtifactIdentity string `yaml:"artifactIdentity"`
}

type credentialSemanticService struct {
	Uses string `yaml:"uses"`
}

type credentialSemanticFlow struct {
	Intent      string            `yaml:"intent"`
	Purpose     string            `yaml:"purpose,omitempty"`
	ForEach     string            `yaml:"forEach,omitempty"`
	Concurrency string            `yaml:"concurrency,omitempty"`
	Requires    []string          `yaml:"requires,omitempty"`
	Produces    []string          `yaml:"produces,omitempty"`
	Satisfies   []string          `yaml:"satisfies,omitempty"`
	Scope       map[string]string `yaml:"scope,omitempty"`
}

type credentialSemanticFailurePolicy struct {
	StopBeforeConvergeOnArtifactFailure  bool `yaml:"stopBeforeConvergeOnArtifactFailure,omitempty"`
	StopBeforeConvergeOnImagePullFailure bool `yaml:"stopBeforeConvergeOnImagePullFailure,omitempty"`
	Converge                             struct {
		Atomic   bool   `yaml:"atomic,omitempty"`
		Wait     bool   `yaml:"wait,omitempty"`
		Rollback string `yaml:"rollback,omitempty"`
	} `yaml:"converge,omitempty"`
	UnknownOutcome string `yaml:"unknownOutcome,omitempty"`
}

type credentialServiceRelease struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
	} `yaml:"metadata"`
	Spec struct {
		Service     string `yaml:"service"`
		Application string `yaml:"application"`
		Source      struct {
			Repository string `yaml:"repository"`
			Branch     string `yaml:"branch"`
			Revision   string `yaml:"revision"`
			Context    string `yaml:"context"`
		} `yaml:"source"`
		Artifact struct {
			Type              string `yaml:"type"`
			Repository        string `yaml:"repository"`
			Version           string `yaml:"version"`
			DeclarationSource string `yaml:"declarationSource"`
			Identity          string `yaml:"identity"`
		} `yaml:"artifact"`
		Binding struct {
			HelmValue string `yaml:"helmValue"`
			Workload  string `yaml:"workload"`
			Container string `yaml:"container"`
		} `yaml:"binding"`
		Intents  []string `yaml:"intents"`
		Evidence struct {
			Required []string `yaml:"required"`
		} `yaml:"evidence"`
	} `yaml:"spec"`
}

type credentialTemplateParameter struct {
	Required *bool `yaml:"required,omitempty"`
	Default  any   `yaml:"default,omitempty"`
}

type credentialTemplateRelease struct {
	Source   credentialTemplateReleaseSource   `yaml:"source,omitempty"`
	Manifest credentialTemplateReleaseManifest `yaml:"manifest,omitempty"`
}

type credentialTemplateReleaseSource struct {
	Repository string `yaml:"repository,omitempty"`
	Revision   string `yaml:"revision,omitempty"`
	Branch     string `yaml:"branch,omitempty"`
}

type credentialTemplateReleaseManifest struct {
	Repository string `yaml:"repository,omitempty"`
	Digest     string `yaml:"digest,omitempty"`
}

type credentialTemplateCredentialRef struct {
	Name               string                     `yaml:"name"`
	Use                CredentialUse              `yaml:"use"`
	Namespace          string                     `yaml:"namespace"`
	Workload           credentialTemplateWorkload `yaml:"workload"`
	RegistryRepository string                     `yaml:"registryRepository,omitempty"`
	RegistryReference  string                     `yaml:"registryReference,omitempty"`
}

type credentialTemplateWorkload struct {
	Kind string `yaml:"kind"`
	Name string `yaml:"name"`
}

type credentialTemplateBundleRef struct {
	Name string `yaml:"name"`
}

type credentialConfigurationSource struct {
	ArtifactContract     credentialConfigurationArtifactContract               `yaml:"artifactContract"`
	VerificationProfiles map[string]credentialConfigurationVerificationProfile `yaml:"verificationProfiles"`
	Environments         map[string]credentialConfigurationEnvironment         `yaml:"environments"`
}

type credentialConfigurationArtifactContract struct {
	Type                 string            `yaml:"type"`
	SourceRegistry       string            `yaml:"sourceRegistry"`
	SourceRepository     string            `yaml:"sourceRepository"`
	SourceBranch         string            `yaml:"sourceBranch"`
	Services             []string          `yaml:"services"`
	SourceArtifactNames  map[string]string `yaml:"sourceArtifactNames"`
	ImageTagPattern      string            `yaml:"imageTagPattern"`
	ImageReferenceFormat string            `yaml:"imageReferenceFormat"`
}

type credentialConfigurationVerificationProfile struct {
	RequiredEvidence []string `yaml:"requiredEvidence"`
}

type credentialConfigurationEnvironment struct {
	DeploymentPlatform  string                          `yaml:"deploymentPlatform"`
	Target              credentialConfigurationTarget   `yaml:"target"`
	DeploymentTarget    credentialConfigurationTarget   `yaml:"deploymentTarget"`
	Executor            credentialConfigurationExecutor `yaml:"executor"`
	VerificationProfile string                          `yaml:"verificationProfile"`
}

type credentialConfigurationTarget struct {
	Name     string `yaml:"name"`
	Cluster  string `yaml:"cluster"`
	Instance string `yaml:"instance"`
}

type credentialConfigurationExecutor struct {
	Type      string                                `yaml:"type"`
	Lifecycle string                                `yaml:"lifecycle"`
	Config    credentialConfigurationExecutorConfig `yaml:"config"`
}

type credentialConfigurationExecutorConfig struct {
	Namespace                 string                               `yaml:"namespace"`
	Release                   string                               `yaml:"release"`
	Workload                  string                               `yaml:"workload"`
	Container                 string                               `yaml:"container"`
	Registry                  string                               `yaml:"registry"`
	ReleaseManifestRepository string                               `yaml:"releaseManifestRepository"`
	Chart                     string                               `yaml:"chart"`
	Values                    string                               `yaml:"values"`
	DetachedJob               credentialConfigurationDetachedJob   `yaml:"detachedJob,omitempty"`
	ImageBindings             map[string]string                    `yaml:"imageBindings"`
	HealthChecks              credentialConfigurationHealthChecks  `yaml:"healthChecks"`
	Authz                     credentialConfigurationAuthorization `yaml:"authz"`
}

type credentialConfigurationDetachedJob struct {
	NodeSelector            map[string]string `yaml:"nodeSelector"`
	KubeconfigHostPath      string            `yaml:"kubeconfigHostPath"`
	AgentHomeHostPath       string            `yaml:"agentHomeHostPath"`
	Timeout                 string            `yaml:"timeout"`
	ExecutorImageRepository string            `yaml:"executorImageRepository"`
	ExecutorImageDigest     string            `yaml:"executorImageDigest"`
}

type credentialConfigurationHealthChecks struct {
	Public []credentialConfigurationHTTPCheck `yaml:"public"`
}

type credentialConfigurationHTTPCheck struct {
	ID             string `yaml:"id"`
	URL            string `yaml:"url"`
	ExpectedStatus int    `yaml:"expectedStatus"`
}

type credentialConfigurationAuthorization struct {
	AppCode         string `yaml:"appCode"`
	EnvironmentCode string `yaml:"environmentCode"`
}

func parseCredentialPlan(root, workflowPath string) (CredentialPlan, error) {
	workflowData, err := readCredentialDeclarationFile(root, workflowPath)
	if err != nil {
		return CredentialPlan{}, err
	}
	if _, err := credentialYAMLDocument(workflowData); err != nil {
		return CredentialPlan{}, err
	}

	var identity credentialDocumentIdentity
	if err := yaml.Unmarshal(workflowData, &identity); err != nil {
		return CredentialPlan{}, invalidCredentialDeclaration(err)
	}
	switch identity.Kind {
	case "DeploymentTemplate":
		return parseDeploymentTemplateCredentialPlan(root, workflowData)
	case "SemanticRelease":
		return parseSemanticReleaseCredentialPlan(root, workflowPath, workflowData)
	default:
		return CredentialPlan{}, invalidCredentialDeclaration(fmt.Errorf("unsupported root kind %q", identity.Kind))
	}
}

func parseDeploymentTemplateCredentialPlan(root string, workflowData []byte) (CredentialPlan, error) {
	var template credentialDeploymentTemplate
	if err := strictCredentialYAMLDecode(workflowData, &template); err != nil {
		return CredentialPlan{}, invalidCredentialDeclaration(err)
	}
	if template.APIVersion != "doops.sh/v2" || template.Kind != "DeploymentTemplate" ||
		strings.TrimSpace(template.Metadata.Name) == "" ||
		strings.TrimSpace(template.Spec.Application) == "" ||
		strings.TrimSpace(template.Spec.Environment) == "" ||
		strings.TrimSpace(template.Spec.ConfigurationSource) == "" {
		return CredentialPlan{}, invalidCredentialDeclaration(errors.New("invalid DeploymentTemplate identity or deployment context"))
	}
	for name, parameter := range template.Spec.Parameters {
		if parameter.Default != nil {
			return CredentialPlan{}, fmt.Errorf(
				"%w: parameter %q must not declare a default value",
				ErrInlineCredentialMaterial,
				name,
			)
		}
	}

	configurationData, err := readCredentialDeclarationFile(root, template.Spec.ConfigurationSource)
	if err != nil {
		return CredentialPlan{}, err
	}
	if _, err := credentialYAMLDocument(configurationData); err != nil {
		return CredentialPlan{}, err
	}

	var configuration credentialConfigurationSource
	if err := strictCredentialYAMLDecode(configurationData, &configuration); err != nil {
		return CredentialPlan{}, invalidCredentialDeclaration(err)
	}
	environment, ok := configuration.Environments[template.Spec.Environment]
	if !ok {
		return CredentialPlan{}, invalidCredentialDeclaration(fmt.Errorf("environment %q is not declared", template.Spec.Environment))
	}
	target := environment.Target
	if strings.TrimSpace(target.Name) == "" || strings.TrimSpace(target.Cluster) == "" || strings.TrimSpace(target.Instance) == "" {
		return CredentialPlan{}, invalidCredentialDeclaration(fmt.Errorf("environment %q has an incomplete target", template.Spec.Environment))
	}

	refs := make([]CredentialPlanReference, 0, len(template.Spec.CredentialRefs))
	for _, ref := range template.Spec.CredentialRefs {
		if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Namespace) == "" || !validCredentialUse(ref.Use) {
			return CredentialPlan{}, invalidCredentialDeclaration(errors.New("credential reference is incomplete or invalid"))
		}
		if ref.Use == CredentialUseImagePull &&
			(strings.TrimSpace(ref.RegistryRepository) == "" || strings.TrimSpace(ref.RegistryReference) == "") {
			return CredentialPlan{}, invalidCredentialDeclaration(errors.New("registry credential reference requires repository and reference"))
		}
		if ref.Use == CredentialUseImagePull &&
			(strings.TrimSpace(ref.Workload.Kind) == "" || strings.TrimSpace(ref.Workload.Name) == "") {
			return CredentialPlan{}, invalidCredentialDeclaration(errors.New("registry credential reference requires workload kind and name"))
		}
		refs = append(refs, CredentialPlanReference{
			Name:               ref.Name,
			Use:                ref.Use,
			Namespace:          ref.Namespace,
			RegistryRepository: strings.TrimSpace(ref.RegistryRepository),
			RegistryReference:  strings.TrimSpace(ref.RegistryReference),
			Workload: CredentialPlanWorkload{
				Kind: ref.Workload.Kind,
				Name: ref.Workload.Name,
			},
		})
	}

	bundles := make([]string, 0, len(template.Spec.CredentialBundleRefs))
	for _, ref := range template.Spec.CredentialBundleRefs {
		if strings.TrimSpace(ref.Name) == "" {
			return CredentialPlan{}, invalidCredentialDeclaration(errors.New("credential bundle reference name is required"))
		}
		bundles = append(bundles, ref.Name)
	}

	return CredentialPlan{
		Template:       template.Metadata.Name,
		Project:        template.Spec.Application,
		Environment:    template.Spec.Environment,
		Target:         target.Name,
		Cluster:        target.Cluster,
		Instance:       target.Instance,
		CredentialRefs: refs,
		BundleRefs:     bundles,
	}, nil
}

func parseSemanticReleaseCredentialPlan(root, workflowPath string, workflowData []byte) (CredentialPlan, error) {
	var release credentialSemanticRelease
	if err := strictCredentialYAMLDecode(workflowData, &release); err != nil {
		return CredentialPlan{}, invalidCredentialDeclaration(err)
	}
	if release.APIVersion != "doops.sh/v2" || release.Kind != "SemanticRelease" ||
		strings.TrimSpace(release.Metadata.Name) == "" ||
		strings.TrimSpace(release.Spec.Application) == "" ||
		strings.TrimSpace(release.Spec.Environment) == "" ||
		strings.TrimSpace(release.Spec.ConfigurationSource) == "" ||
		len(release.Spec.ServiceCatalog) == 0 || len(release.Spec.Flow) == 0 ||
		strings.TrimSpace(release.Spec.Release.Reason) == "" ||
		!validCredentialSemanticReleaseIdentity(release.Spec.Release) {
		return CredentialPlan{}, invalidCredentialDeclaration(errors.New("invalid SemanticRelease identity or deployment context"))
	}
	if err := validateCredentialParameters(release.Spec.Parameters); err != nil {
		return CredentialPlan{}, err
	}
	for _, flow := range release.Spec.Flow {
		if strings.TrimSpace(flow.Intent) == "" {
			return CredentialPlan{}, invalidCredentialDeclaration(errors.New("SemanticRelease flow intent is required"))
		}
	}

	workflowRelative, err := credentialDeclarationRelativePath(workflowPath)
	if err != nil {
		return CredentialPlan{}, err
	}
	workflowDirectory := filepath.Dir(workflowRelative)
	for serviceName, catalogEntry := range release.Spec.ServiceCatalog {
		if strings.TrimSpace(serviceName) == "" || strings.TrimSpace(catalogEntry.Uses) == "" {
			return CredentialPlan{}, invalidCredentialDeclaration(errors.New("SemanticRelease service catalog entry is incomplete"))
		}
		servicePath := filepath.ToSlash(filepath.Join(workflowDirectory, filepath.FromSlash(catalogEntry.Uses)))
		serviceData, err := readCredentialDeclarationFile(root, servicePath)
		if err != nil {
			return CredentialPlan{}, err
		}
		if _, err := credentialYAMLDocument(serviceData); err != nil {
			return CredentialPlan{}, err
		}
		var service credentialServiceRelease
		if err := strictCredentialYAMLDecode(serviceData, &service); err != nil {
			return CredentialPlan{}, invalidCredentialDeclaration(err)
		}
		if service.APIVersion != "doops.sh/v2" || service.Kind != "ServiceRelease" ||
			strings.TrimSpace(service.Metadata.Name) == "" ||
			service.Spec.Service != serviceName ||
			service.Spec.Application != release.Spec.Application ||
			strings.TrimSpace(service.Spec.Artifact.Type) == "" ||
			!validCredentialServiceArtifactIdentity(service) ||
			strings.TrimSpace(service.Spec.Binding.HelmValue) == "" ||
			strings.TrimSpace(service.Spec.Binding.Workload) == "" ||
			strings.TrimSpace(service.Spec.Binding.Container) == "" ||
			len(service.Spec.Intents) == 0 || len(service.Spec.Evidence.Required) == 0 {
			return CredentialPlan{}, invalidCredentialDeclaration(fmt.Errorf("service catalog entry %q has an invalid ServiceRelease", serviceName))
		}
	}

	configurationData, err := readCredentialDeclarationFile(root, release.Spec.ConfigurationSource)
	if err != nil {
		return CredentialPlan{}, err
	}
	configurationDocument, err := credentialYAMLDocument(configurationData)
	if err != nil {
		return CredentialPlan{}, err
	}
	target, err := semanticEnvironmentTarget(root, configurationDocument, release.Spec.Environment)
	if err != nil {
		return CredentialPlan{}, err
	}
	refs, err := credentialPlanReferences(release.Spec.CredentialRefs)
	if err != nil {
		return CredentialPlan{}, err
	}
	bundles, err := credentialPlanBundles(release.Spec.CredentialBundleRefs)
	if err != nil {
		return CredentialPlan{}, err
	}
	return CredentialPlan{
		Template:       release.Metadata.Name,
		Project:        release.Spec.Application,
		Environment:    release.Spec.Environment,
		Target:         target.Name,
		Cluster:        target.Cluster,
		Instance:       target.Instance,
		CredentialRefs: refs,
		BundleRefs:     bundles,
	}, nil
}

func validCredentialSemanticReleaseIdentity(identity credentialSemanticReleaseIdentity) bool {
	legacy := strings.TrimSpace(identity.Revision) != "" &&
		strings.TrimSpace(identity.Version) != "" &&
		strings.TrimSpace(identity.SelectedServices) != ""
	declared := strings.TrimSpace(identity.Strategy) != "" &&
		strings.TrimSpace(identity.ServiceSelection) != "" &&
		strings.TrimSpace(identity.ArtifactIdentity) != ""
	environmentDeclared := strings.TrimSpace(identity.SelectedServices) != "" &&
		strings.TrimSpace(identity.Revision) == "" &&
		strings.TrimSpace(identity.Version) == ""
	return legacy || declared || environmentDeclared
}

func validCredentialServiceArtifactIdentity(service credentialServiceRelease) bool {
	legacy := strings.TrimSpace(service.Spec.Source.Repository) != "" &&
		strings.TrimSpace(service.Spec.Source.Revision) != "" &&
		strings.TrimSpace(service.Spec.Artifact.Repository) != "" &&
		strings.TrimSpace(service.Spec.Artifact.Version) != ""
	declared := strings.TrimSpace(service.Spec.Artifact.DeclarationSource) != "" &&
		strings.TrimSpace(service.Spec.Artifact.Identity) != ""
	return legacy || declared
}

func semanticEnvironmentTarget(root string, document *yaml.Node, environment string) (credentialConfigurationTarget, error) {
	if document == nil || len(document.Content) == 0 {
		return credentialConfigurationTarget{}, invalidCredentialDeclaration(errors.New("EnvironmentDefinition document is empty"))
	}
	documentRoot := document.Content[0]
	apiVersion := yamlScalarValue(yamlMappingValue(documentRoot, "apiVersion"))
	kind := yamlScalarValue(yamlMappingValue(documentRoot, "kind"))
	if apiVersion == "doops.sh/v2" && kind == "EnvironmentRegistry" {
		entry := yamlMappingValue(yamlMappingValue(documentRoot, "environments"), environment)
		path := yamlScalarValue(yamlMappingValue(entry, "file"))
		if path == "" {
			return credentialConfigurationTarget{}, invalidCredentialDeclaration(
				fmt.Errorf("environment %q is not declared in EnvironmentRegistry", environment),
			)
		}
		environmentData, err := readCredentialDeclarationFile(root, path)
		if err != nil {
			return credentialConfigurationTarget{}, err
		}
		environmentDocument, err := credentialYAMLDocument(environmentData)
		if err != nil {
			return credentialConfigurationTarget{}, err
		}
		return semanticEnvironmentTarget(root, environmentDocument, environment)
	}
	metadata := yamlMappingValue(documentRoot, "metadata")
	name := yamlScalarValue(yamlMappingValue(metadata, "name"))
	deploy := yamlMappingValue(yamlMappingValue(yamlMappingValue(documentRoot, "spec"), "execution"), "deploy")
	target := credentialConfigurationTarget{
		Name:     yamlScalarValue(yamlMappingValue(deploy, "target")),
		Cluster:  yamlScalarValue(yamlMappingValue(deploy, "cluster")),
		Instance: yamlScalarValue(yamlMappingValue(deploy, "instance")),
	}
	if apiVersion != "doops.sh/v2" || kind != "EnvironmentDefinition" || name != environment ||
		target.Name == "" || target.Cluster == "" || target.Instance == "" {
		return credentialConfigurationTarget{}, invalidCredentialDeclaration(fmt.Errorf("environment %q has an invalid EnvironmentDefinition target", environment))
	}
	return target, nil
}

func yamlMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		node = node.Content[0]
	}
	if key == "" {
		return node
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func validateCredentialParameters(parameters map[string]credentialTemplateParameter) error {
	for name, parameter := range parameters {
		if parameter.Default != nil {
			return fmt.Errorf("%w: parameter %q must not declare a default value", ErrInlineCredentialMaterial, name)
		}
	}
	return nil
}

func credentialPlanReferences(declared []credentialTemplateCredentialRef) ([]CredentialPlanReference, error) {
	refs := make([]CredentialPlanReference, 0, len(declared))
	for _, ref := range declared {
		if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Namespace) == "" || !validCredentialUse(ref.Use) {
			return nil, invalidCredentialDeclaration(errors.New("credential reference is incomplete or invalid"))
		}
		if ref.Use == CredentialUseImagePull &&
			(strings.TrimSpace(ref.RegistryRepository) == "" || strings.TrimSpace(ref.RegistryReference) == "") {
			return nil, invalidCredentialDeclaration(errors.New("registry credential reference requires repository and reference"))
		}
		if ref.Use == CredentialUseImagePull &&
			(strings.TrimSpace(ref.Workload.Kind) == "" || strings.TrimSpace(ref.Workload.Name) == "") {
			return nil, invalidCredentialDeclaration(errors.New("registry credential reference requires workload kind and name"))
		}
		refs = append(refs, CredentialPlanReference{
			Name:               ref.Name,
			Use:                ref.Use,
			Namespace:          ref.Namespace,
			RegistryRepository: strings.TrimSpace(ref.RegistryRepository),
			RegistryReference:  strings.TrimSpace(ref.RegistryReference),
			Workload: CredentialPlanWorkload{
				Kind: ref.Workload.Kind,
				Name: ref.Workload.Name,
			},
		})
	}
	return refs, nil
}

func credentialPlanBundles(declared []credentialTemplateBundleRef) ([]string, error) {
	bundles := make([]string, 0, len(declared))
	for _, ref := range declared {
		if strings.TrimSpace(ref.Name) == "" {
			return nil, invalidCredentialDeclaration(errors.New("credential bundle reference name is required"))
		}
		bundles = append(bundles, ref.Name)
	}
	return bundles, nil
}

func parseCredentialPlanDocuments(workflowPath string, workflowData, configurationData []byte) (CredentialPlan, error) {
	if _, err := credentialYAMLDocument(workflowData); err != nil {
		return CredentialPlan{}, err
	}
	var template credentialDeploymentTemplate
	if err := strictCredentialYAMLDecode(workflowData, &template); err != nil {
		return CredentialPlan{}, invalidCredentialDeclaration(err)
	}
	workflowRelative, err := credentialDeclarationRelativePath(workflowPath)
	if err != nil {
		return CredentialPlan{}, err
	}
	configurationRelative, err := credentialDeclarationRelativePath(template.Spec.ConfigurationSource)
	if err != nil {
		return CredentialPlan{}, err
	}
	root, err := os.MkdirTemp("", "doops-credential-plan-*")
	if err != nil {
		return CredentialPlan{}, invalidCredentialDeclaration(err)
	}
	defer os.RemoveAll(root)
	for path, data := range map[string][]byte{
		workflowRelative:      workflowData,
		configurationRelative: configurationData,
	} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
			return CredentialPlan{}, invalidCredentialDeclaration(err)
		}
		if err := os.WriteFile(absolute, data, 0600); err != nil {
			return CredentialPlan{}, invalidCredentialDeclaration(err)
		}
	}
	return parseCredentialPlan(root, filepath.ToSlash(workflowRelative))
}

func credentialDeclarationRelativePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", invalidCredentialDeclaration(errors.New("declaration path is required"))
	}
	relativePath := filepath.FromSlash(path)
	if filepath.IsAbs(relativePath) {
		return "", invalidCredentialDeclaration(fmt.Errorf("declaration path %q is absolute", path))
	}
	cleanPath := filepath.Clean(relativePath)
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", invalidCredentialDeclaration(fmt.Errorf("declaration path %q escapes the repository", path))
	}
	return cleanPath, nil
}

func readCredentialDeclarationFile(root, path string) ([]byte, error) {
	cleanPath, err := credentialDeclarationRelativePath(path)
	if err != nil {
		return nil, err
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, invalidCredentialDeclaration(fmt.Errorf("resolve repository root: %w", err))
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, invalidCredentialDeclaration(fmt.Errorf("resolve repository root: %w", err))
	}
	resolvedPath, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, cleanPath))
	if err != nil {
		return nil, invalidCredentialDeclaration(fmt.Errorf("resolve declaration path %q: %w", path, err))
	}
	relativeResolvedPath, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relativeResolvedPath == ".." || strings.HasPrefix(relativeResolvedPath, ".."+string(filepath.Separator)) {
		return nil, invalidCredentialDeclaration(fmt.Errorf("declaration path %q escapes the repository", path))
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, invalidCredentialDeclaration(fmt.Errorf("read declaration path %q: %w", path, err))
	}
	return data, nil
}

func credentialYAMLDocument(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, invalidCredentialDeclaration(err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, invalidCredentialDeclaration(errors.New("multiple YAML documents are not allowed"))
		}
		return nil, invalidCredentialDeclaration(err)
	}
	if err := auditCredentialYAMLNode(&document, make(map[*yaml.Node]struct{})); err != nil {
		return nil, err
	}
	return &document, nil
}

func auditCredentialYAMLNode(node *yaml.Node, visited map[*yaml.Node]struct{}) error {
	if node == nil {
		return nil
	}
	if _, ok := visited[node]; ok {
		return nil
	}
	visited[node] = struct{}{}

	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if err := auditCredentialYAMLNode(child, visited); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		if len(node.Content)%2 != 0 {
			return invalidCredentialDeclaration(errors.New("invalid YAML mapping"))
		}
		keys := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Kind != yaml.ScalarNode {
				return invalidCredentialDeclaration(errors.New("YAML mapping keys must be scalars"))
			}
			if _, duplicate := keys[key.Value]; duplicate {
				return invalidCredentialDeclaration(fmt.Errorf("duplicate YAML mapping key %q", key.Value))
			}
			keys[key.Value] = struct{}{}
			if key.Value == "<<" {
				return invalidCredentialDeclaration(errors.New("YAML merge keys are not allowed"))
			}
			if credentialSecretLikeYAMLField(key.Value, value) {
				return fmt.Errorf("%w: YAML key %q is not allowed", ErrInlineCredentialMaterial, key.Value)
			}
			if err := auditCredentialYAMLNode(key, visited); err != nil {
				return err
			}
			if err := auditCredentialYAMLNode(value, visited); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		return auditCredentialYAMLNode(node.Alias, visited)
	case yaml.ScalarNode:
		if credentialScalarContainsInlineMaterial(node.Value) {
			return fmt.Errorf("%w: scalar value contains inline credential material", ErrInlineCredentialMaterial)
		}
	}
	return nil
}

func strictCredentialYAMLDecode(data []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func invalidCredentialDeclaration(err error) error {
	return fmt.Errorf("%w: %v", ErrCredentialDeclarationInvalid, err)
}

func credentialSecretLikeKey(key string) bool {
	normalized := normalizeCredentialKey(key)
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

func normalizeCredentialKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '.', ' ':
			return -1
		default:
			return r
		}
	}, strings.ToLower(strings.TrimSpace(key)))
}

func credentialSecretLikeYAMLField(key string, value *yaml.Node) bool {
	if !credentialSecretLikeKey(key) {
		return false
	}
	return !credentialKubernetesSecretReference(value)
}

func credentialScalarContainsInlineMaterial(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, " private key-----") {
		return true
	}
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "basic ") ||
		strings.Contains(lower, "sk-") {
		return true
	}
	if strings.Contains(value, "://") || strings.Contains(value, "?") {
		parsed, err := url.Parse(value)
		if err == nil {
			if parsed.User != nil {
				return true
			}
			for key, values := range parsed.Query() {
				if credentialSecretLikeKey(key) {
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

func credentialKubernetesSecretReference(value *yaml.Node) bool {
	if value == nil || value.Kind != yaml.MappingNode {
		return false
	}
	kind := yamlScalarValue(yamlMappingValue(value, "kind"))
	name := yamlScalarValue(yamlMappingValue(value, "name"))
	if kind != "KubernetesSecret" || name == "" {
		return false
	}
	for index := 0; index < len(value.Content); index += 2 {
		key := value.Content[index].Value
		if key != "kind" && key != "name" {
			return false
		}
	}
	return true
}
