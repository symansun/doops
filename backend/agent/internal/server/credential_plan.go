package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

type credentialTemplateParameter struct {
	Required *bool `yaml:"required,omitempty"`
	Default  any   `yaml:"default,omitempty"`
}

type credentialTemplateRelease struct {
	Source   credentialTemplateReleaseSource   `yaml:"source,omitempty"`
	Manifest credentialTemplateReleaseManifest `yaml:"manifest,omitempty"`
	Version  string                            `yaml:"version,omitempty"`
	Reason   string                            `yaml:"reason,omitempty"`
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
		Release              credentialSemanticReleaseRelease       `yaml:"release,omitempty"`
		Environment          string                                 `yaml:"environment"`
		ConfigurationSource  string                                 `yaml:"configurationSource"`
		ServiceCatalog       map[string]credentialSemanticService   `yaml:"serviceCatalog"`
		Flow                 []credentialSemanticFlow               `yaml:"flow"`
		FailurePolicy        credentialSemanticFailurePolicy        `yaml:"failurePolicy,omitempty"`
		CredentialRefs       []credentialTemplateCredentialRef      `yaml:"credentialRefs,omitempty"`
		CredentialBundleRefs []credentialTemplateBundleRef          `yaml:"credentialBundleRefs,omitempty"`
	} `yaml:"spec"`
}

type credentialSemanticReleaseRelease struct {
	Revision         string `yaml:"revision,omitempty"`
	Version          string `yaml:"version,omitempty"`
	Reason           string `yaml:"reason,omitempty"`
	SelectedServices string `yaml:"selectedServices,omitempty"`
	Strategy         string `yaml:"strategy,omitempty"`
	ServiceSelection string `yaml:"serviceSelection,omitempty"`
	ArtifactIdentity string `yaml:"artifactIdentity,omitempty"`
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
			Branch     string `yaml:"branch,omitempty"`
			Revision   string `yaml:"revision"`
			Context    string `yaml:"context,omitempty"`
		} `yaml:"source"`
		Artifact struct {
			Type       string `yaml:"type"`
			Repository string `yaml:"repository"`
			Version    string `yaml:"version"`
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

type credentialParsedWorkflow struct {
	Template         credentialDeploymentTemplate
	SemanticRevision string
	Environment      *credentialEnvironmentContext
}

type credentialEnvironmentContext struct {
	Target           credentialConfigurationTarget
	SourceRepository string
	SourceBranch     string
}

type credentialWorkflowIdentity struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

type credentialConfigurationSource struct {
	APIVersion           string                                                `yaml:"apiVersion"`
	Kind                 string                                                `yaml:"kind"`
	Metadata             credentialConfigurationMetadata                       `yaml:"metadata"`
	IdentityRules        []string                                              `yaml:"identityRules"`
	ArtifactContract     credentialConfigurationArtifactContract               `yaml:"artifactContract"`
	ServiceCatalog       map[string]credentialConfigurationServiceCatalogItem  `yaml:"serviceCatalog"`
	VerificationProfiles map[string]credentialConfigurationVerificationProfile `yaml:"verificationProfiles"`
	Environments         map[string]credentialConfigurationEnvironment         `yaml:"environments"`
}

type credentialEnvironmentDefinition struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		DisplayName string `yaml:"displayName,omitempty"`
		Description string `yaml:"description,omitempty"`
	} `yaml:"metadata"`
	Spec struct {
		Application string                                 `yaml:"application"`
		Parameters  map[string]credentialTemplateParameter `yaml:"parameters,omitempty"`
		Release     struct {
			Reason string `yaml:"reason,omitempty"`
		} `yaml:"release,omitempty"`
		DeploymentMode string `yaml:"deploymentMode,omitempty"`
		Source         struct {
			Repository    string `yaml:"repository"`
			Branch        string `yaml:"branch"`
			BuildManifest string `yaml:"buildManifest"`
			ImageRegistry string `yaml:"imageRegistry"`
		} `yaml:"source"`
		Execution struct {
			Build struct {
				Executor    string `yaml:"executor"`
				DoopsTarget any    `yaml:"doopsTarget"`
			} `yaml:"build"`
			Deploy struct {
				Executor string `yaml:"executor"`
				Target   string `yaml:"target"`
				Cluster  string `yaml:"cluster"`
				Instance string `yaml:"instance"`
				Session  string `yaml:"session"`
			} `yaml:"deploy"`
		} `yaml:"execution"`
		Helm struct {
			Release           string `yaml:"release"`
			Namespace         string `yaml:"namespace"`
			Chart             string `yaml:"chart"`
			Values            string `yaml:"values"`
			RuntimeFiles      string `yaml:"runtimeFiles,omitempty"`
			Atomic            bool   `yaml:"atomic"`
			Wait              bool   `yaml:"wait"`
			RollbackOnFailure bool   `yaml:"rollbackOnFailure"`
			SharedRelease     bool   `yaml:"sharedRelease"`
		} `yaml:"helm"`
		Artifacts           credentialEnvironmentArtifacts       `yaml:"artifacts"`
		Services            map[string]string                    `yaml:"services,omitempty"`
		RetiredResources    []credentialConfigurationResource    `yaml:"retiredResources,omitempty"`
		PublicHosts         []string                             `yaml:"publicHosts,omitempty"`
		HealthChecks        *credentialConfigurationHealthChecks `yaml:"healthChecks"`
		VerificationProfile string                               `yaml:"verificationProfile,omitempty"`
		ReleaseProcess      []string                             `yaml:"releaseProcess,omitempty"`
		StopConditions      []string                             `yaml:"stopConditions,omitempty"`
	} `yaml:"spec"`
}

type credentialEnvironmentArtifacts struct {
	TargetRegistry                    string                         `yaml:"targetRegistry"`
	IdentitySource                    string                         `yaml:"identitySource,omitempty"`
	HelmValuesMustMatchDeclaredDigest bool                           `yaml:"helmValuesMustMatchDeclaredDigest,omitempty"`
	Migration                         credentialEnvironmentMigration `yaml:"migration,omitempty"`
	SourceCredentialRef               credentialEnvironmentSecretRef `yaml:"sourceCredentialRef"`
	TargetCredentialRef               credentialEnvironmentSecretRef `yaml:"targetCredentialRef"`
	RuntimeImagePullSecret            credentialEnvironmentSecretRef `yaml:"runtimeImagePullSecret"`
}

type credentialEnvironmentMigration struct {
	CopyOnlyWhenTargetDigestMissing   bool `yaml:"copyOnlyWhenTargetDigestMissing,omitempty"`
	RequireSourceTargetDigestEquality bool `yaml:"requireSourceTargetDigestEquality,omitempty"`
	PrePullBeforeHelm                 bool `yaml:"prePullBeforeHelm,omitempty"`
}

type credentialEnvironmentSecretRef struct {
	Kind                           string `yaml:"kind,omitempty"`
	Name                           string `yaml:"name,omitempty"`
	Status                         string `yaml:"status,omitempty"`
	DeploymentBlockedUntilDeclared bool   `yaml:"deploymentBlockedUntilDeclared,omitempty"`
}

type credentialEnvironmentService struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Environment string `yaml:"environment"`
	} `yaml:"metadata"`
	Spec struct {
		ServiceDefinition     string `yaml:"serviceDefinition"`
		EnvironmentDefinition string `yaml:"environmentDefinition"`
		Artifact              struct {
			Source   string `yaml:"source"`
			Target   string `yaml:"target"`
			Identity string `yaml:"identity"`
			Digest   string `yaml:"digest"`
		} `yaml:"artifact"`
		Helm struct {
			Chart     string `yaml:"chart"`
			Values    string `yaml:"values"`
			Value     string `yaml:"value"`
			Enabled   bool   `yaml:"enabled"`
			Replicas  int    `yaml:"replicas"`
			Workload  string `yaml:"workload"`
			Container string `yaml:"container"`
		} `yaml:"helm"`
		Configuration struct {
			ValuesPath string   `yaml:"valuesPath"`
			Files      []string `yaml:"files"`
		} `yaml:"configuration"`
		Verification struct {
			MinReadyReplicas int    `yaml:"minReadyReplicas"`
			RequireEndpoints bool   `yaml:"requireEndpoints"`
			PublicURL        string `yaml:"publicUrl,omitempty"`
		} `yaml:"verification"`
	} `yaml:"spec"`
}

type credentialServiceDefinitionV3 struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Service       string `yaml:"service"`
		Artifact      string `yaml:"artifact"`
		BuildManifest string `yaml:"buildManifest"`
		SourceContext string `yaml:"sourceContext"`
		Dockerfile    string `yaml:"dockerfile"`
		Helm          struct {
			Value     string `yaml:"value"`
			Workload  string `yaml:"workload"`
			Container string `yaml:"container"`
		} `yaml:"helm"`
		Verification struct {
			EndpointRequired bool `yaml:"endpointRequired"`
		} `yaml:"verification"`
	} `yaml:"spec"`
}

type credentialConfigurationMetadata struct {
	Name      string `yaml:"name"`
	UpdatedAt string `yaml:"updatedAt"`
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
	ImageTagTimeZone     string            `yaml:"imageTagTimeZone"`
}

type credentialConfigurationServiceCatalogItem struct {
	Artifact         string `yaml:"artifact"`
	SourceContext    string `yaml:"sourceContext"`
	HelmValue        string `yaml:"helmValue"`
	Workload         string `yaml:"workload"`
	Container        string `yaml:"container"`
	EndpointRequired *bool  `yaml:"endpointRequired"`
}

type credentialConfigurationVerificationProfile struct {
	RequiredEvidence []string `yaml:"requiredEvidence"`
}

type credentialConfigurationEnvironment struct {
	DisplayName             string                          `yaml:"displayName"`
	Services                []string                        `yaml:"services"`
	DeploymentPlatform      string                          `yaml:"deploymentPlatform"`
	Target                  credentialConfigurationTarget   `yaml:"target"`
	DeploymentTarget        credentialConfigurationTarget   `yaml:"deploymentTarget"`
	Executor                credentialConfigurationExecutor `yaml:"executor"`
	VerificationProfile     string                          `yaml:"verificationProfile"`
	DeploymentMode          string                          `yaml:"deploymentMode"`
	ControlPlaneEnvironment string                          `yaml:"controlPlaneEnvironment"`
	PublicHosts             []string                        `yaml:"publicHosts"`
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
	RuntimeFiles              string                               `yaml:"runtimeFiles"`
	DeploymentMode            string                               `yaml:"deploymentMode"`
	RegistryCredential        credentialConfigurationSecretRef     `yaml:"registryCredential"`
	ArtifactSync              credentialConfigurationArtifactSync  `yaml:"artifactSync"`
	DetachedJob               credentialConfigurationDetachedJob   `yaml:"detachedJob,omitempty"`
	ImageBindings             map[string]string                    `yaml:"imageBindings"`
	WorkloadNamespaces        map[string]string                    `yaml:"workloadNamespaces"`
	PublicHosts               []string                             `yaml:"publicHosts"`
	HealthChecks              credentialConfigurationHealthChecks  `yaml:"healthChecks"`
	Authz                     credentialConfigurationAuthorization `yaml:"authz"`
	RetiredResources          []credentialConfigurationResource    `yaml:"retiredResources"`
}

type credentialConfigurationSecretRef struct {
	Kind       string `yaml:"kind"`
	Name       string `yaml:"name"`
	Namespace  string `yaml:"namespace"`
	SecretName string `yaml:"secretName"`
	Key        string `yaml:"key"`
}

type credentialConfigurationArtifactSync struct {
	SourceCredentialRef   credentialConfigurationSecretRef `yaml:"sourceCredentialRef"`
	RegistryCredentialRef credentialConfigurationSecretRef `yaml:"registryCredentialRef"`
}

type credentialConfigurationResource struct {
	APIVersion     string                                  `yaml:"apiVersion"`
	Kind           string                                  `yaml:"kind"`
	Name           string                                  `yaml:"name"`
	DeletionPolicy string                                  `yaml:"deletionPolicy"`
	Expected       credentialConfigurationResourceExpected `yaml:"expected"`
}

type credentialConfigurationResourceExpected struct {
	Manager          string   `yaml:"manager"`
	IngressClassName string   `yaml:"ingressClassName"`
	Hosts            []string `yaml:"hosts"`
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
	Public    []credentialConfigurationHTTPCheck     `yaml:"public"`
	Workloads []credentialConfigurationWorkloadCheck `yaml:"workloads"`
}

type credentialConfigurationHTTPCheck struct {
	ID             string `yaml:"id"`
	URL            string `yaml:"url"`
	ExpectedStatus int    `yaml:"expectedStatus"`
	BodyContains   string `yaml:"bodyContains"`
}

type credentialConfigurationWorkloadCheck struct {
	Service          string `yaml:"service"`
	MinReadyReplicas int    `yaml:"minReadyReplicas"`
	RequireEndpoints *bool  `yaml:"requireEndpoints"`
}

type credentialConfigurationAuthorization struct {
	AppCode         string `yaml:"appCode"`
	EnvironmentCode string `yaml:"environmentCode"`
}

func parseCredentialPlan(root, workflowPath string) (CredentialPlan, error) {
	workflow, err := parseCredentialWorkflow(root, workflowPath)
	if err != nil {
		return CredentialPlan{}, err
	}
	template := workflow.Template
	var environment credentialEnvironmentContext
	if workflow.Environment != nil {
		environment = *workflow.Environment
	} else {
		environment, err = parseCredentialEnvironment(
			root,
			template.Spec.ConfigurationSource,
			template.Spec.Environment,
		)
		if err != nil {
			return CredentialPlan{}, err
		}
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

func parseCredentialWorkflow(root, workflowPath string) (credentialParsedWorkflow, error) {
	workflowData, err := readCredentialDeclarationFile(root, workflowPath)
	if err != nil {
		return credentialParsedWorkflow{}, err
	}
	if _, err := credentialYAMLDocument(workflowData); err != nil {
		return credentialParsedWorkflow{}, err
	}

	var identity credentialWorkflowIdentity
	if err := yaml.Unmarshal(workflowData, &identity); err != nil {
		return credentialParsedWorkflow{}, invalidCredentialDeclaration(err)
	}

	var template credentialDeploymentTemplate
	var semantic credentialSemanticRelease
	semanticRevision := ""
	switch {
	case identity.APIVersion == "doops.sh/v2" && identity.Kind == "SemanticRelease":
		if err := strictCredentialYAMLDecode(workflowData, &semantic); err != nil {
			return credentialParsedWorkflow{}, invalidCredentialDeclaration(err)
		}
		if strings.TrimSpace(semantic.Metadata.Name) == "" ||
			strings.TrimSpace(semantic.Spec.Application) == "" ||
			strings.TrimSpace(semantic.Spec.Environment) == "" ||
			strings.TrimSpace(semantic.Spec.ConfigurationSource) == "" ||
			len(semantic.Spec.ServiceCatalog) == 0 ||
			len(semantic.Spec.Flow) == 0 {
			return credentialParsedWorkflow{}, invalidCredentialDeclaration(errors.New("invalid SemanticRelease identity or deployment context"))
		}
		template.APIVersion = semantic.APIVersion
		template.Kind = semantic.Kind
		template.Metadata.Name = semantic.Metadata.Name
		template.Spec.Application = semantic.Spec.Application
		template.Spec.Environment = semantic.Spec.Environment
		template.Spec.ConfigurationSource = semantic.Spec.ConfigurationSource
		template.Spec.Parameters = semantic.Spec.Parameters
		template.Spec.CredentialRefs = semantic.Spec.CredentialRefs
		template.Spec.CredentialBundleRefs = semantic.Spec.CredentialBundleRefs
		semanticRevision = semantic.Spec.Release.Revision
		for _, flow := range semantic.Spec.Flow {
			if strings.TrimSpace(flow.Intent) == "" {
				return credentialParsedWorkflow{}, invalidCredentialDeclaration(errors.New("SemanticRelease flow intent is required"))
			}
		}
		if err := validateSemanticServiceCatalog(root, workflowPath, semantic); err != nil {
			return credentialParsedWorkflow{}, err
		}
	case identity.APIVersion == "doops.sh/v2" && identity.Kind == "DeploymentTemplate":
		if err := strictCredentialYAMLDecode(workflowData, &template); err != nil {
			return credentialParsedWorkflow{}, invalidCredentialDeclaration(err)
		}
		if strings.TrimSpace(template.Metadata.Name) == "" ||
			strings.TrimSpace(template.Spec.Application) == "" ||
			strings.TrimSpace(template.Spec.Environment) == "" ||
			strings.TrimSpace(template.Spec.ConfigurationSource) == "" {
			return credentialParsedWorkflow{}, invalidCredentialDeclaration(errors.New("invalid DeploymentTemplate identity or deployment context"))
		}
	case identity.APIVersion == "doops.sh/v3" && identity.Kind == "EnvironmentDefinition":
		var definition credentialEnvironmentDefinition
		if err := strictCredentialYAMLDecode(workflowData, &definition); err != nil {
			return credentialParsedWorkflow{}, invalidCredentialDeclaration(err)
		}
		environment, err := validateCredentialEnvironmentDefinitionV3(root, workflowPath, definition)
		if err != nil {
			return credentialParsedWorkflow{}, err
		}
		template.APIVersion = definition.APIVersion
		template.Kind = definition.Kind
		template.Metadata.Name = definition.Metadata.Name
		template.Spec.Parameters = definition.Spec.Parameters
		template.Spec.Application = definition.Spec.Application
		template.Spec.Environment = definition.Metadata.Name
		template.Spec.Release.Source.Repository = definition.Spec.Source.Repository
		template.Spec.Release.Source.Branch = definition.Spec.Source.Branch
		for name, parameter := range template.Spec.Parameters {
			if parameter.Default != nil && credentialSecretLikeKey(name) {
				return credentialParsedWorkflow{}, fmt.Errorf("%w: sensitive parameter %q must not declare a default value", ErrInlineCredentialMaterial, name)
			}
		}
		return credentialParsedWorkflow{
			Template:    template,
			Environment: &environment,
		}, nil
	default:
		return credentialParsedWorkflow{}, invalidCredentialDeclaration(
			fmt.Errorf("unsupported workflow contract %q %q", identity.APIVersion, identity.Kind),
		)
	}
	for name, parameter := range template.Spec.Parameters {
		if parameter.Default != nil && credentialSecretLikeKey(name) {
			return credentialParsedWorkflow{}, fmt.Errorf("%w: sensitive parameter %q must not declare a default value", ErrInlineCredentialMaterial, name)
		}
	}
	return credentialParsedWorkflow{Template: template, SemanticRevision: semanticRevision}, nil
}

func validateSemanticServiceCatalog(root, workflowPath string, semantic credentialSemanticRelease) error {
	workflowRelative, err := credentialDeclarationRelativePath(workflowPath)
	if err != nil {
		return err
	}
	workflowDirectory := filepath.Dir(workflowRelative)
	for serviceName, entry := range semantic.Spec.ServiceCatalog {
		if strings.TrimSpace(serviceName) == "" || strings.TrimSpace(entry.Uses) == "" {
			return invalidCredentialDeclaration(errors.New("SemanticRelease service catalog entry is incomplete"))
		}
		servicePath := filepath.ToSlash(filepath.Join(workflowDirectory, filepath.FromSlash(entry.Uses)))
		serviceData, err := readCredentialDeclarationFile(root, servicePath)
		if err != nil {
			return err
		}
		if _, err := credentialYAMLDocument(serviceData); err != nil {
			return err
		}
		var service credentialServiceRelease
		if err := strictCredentialYAMLDecode(serviceData, &service); err != nil {
			return invalidCredentialDeclaration(err)
		}
		if service.APIVersion != "doops.sh/v2" || service.Kind != "ServiceRelease" ||
			strings.TrimSpace(service.Metadata.Name) == "" ||
			service.Spec.Service != serviceName ||
			service.Spec.Application != semantic.Spec.Application ||
			strings.TrimSpace(service.Spec.Source.Repository) == "" ||
			strings.TrimSpace(service.Spec.Source.Revision) == "" ||
			strings.TrimSpace(service.Spec.Artifact.Type) == "" ||
			strings.TrimSpace(service.Spec.Artifact.Repository) == "" ||
			strings.TrimSpace(service.Spec.Artifact.Version) == "" ||
			strings.TrimSpace(service.Spec.Binding.HelmValue) == "" ||
			strings.TrimSpace(service.Spec.Binding.Workload) == "" ||
			strings.TrimSpace(service.Spec.Binding.Container) == "" ||
			len(service.Spec.Intents) == 0 ||
			len(service.Spec.Evidence.Required) == 0 {
			return invalidCredentialDeclaration(fmt.Errorf("service catalog entry %q has an invalid ServiceRelease", serviceName))
		}
	}
	return nil
}

func validateCredentialEnvironmentDefinitionV3(
	root, workflowPath string,
	definition credentialEnvironmentDefinition,
) (credentialEnvironmentContext, error) {
	environmentName := strings.TrimSpace(definition.Metadata.Name)
	deploy := definition.Spec.Execution.Deploy
	if environmentName == "" ||
		strings.TrimSpace(definition.Spec.Application) == "" ||
		strings.TrimSpace(definition.Spec.Source.Repository) == "" ||
		strings.TrimSpace(definition.Spec.Source.Branch) == "" ||
		strings.TrimSpace(definition.Spec.Source.BuildManifest) == "" ||
		strings.TrimSpace(definition.Spec.Source.ImageRegistry) == "" ||
		strings.TrimSpace(deploy.Executor) != "doops" ||
		strings.TrimSpace(deploy.Target) == "" ||
		strings.TrimSpace(deploy.Cluster) == "" ||
		strings.TrimSpace(deploy.Instance) == "" ||
		strings.TrimSpace(deploy.Session) == "" ||
		strings.TrimSpace(definition.Spec.Helm.Release) == "" ||
		strings.TrimSpace(definition.Spec.Helm.Namespace) == "" ||
		strings.TrimSpace(definition.Spec.Helm.Chart) == "" ||
		strings.TrimSpace(definition.Spec.Helm.Values) == "" ||
		!definition.Spec.Helm.Atomic ||
		!definition.Spec.Helm.Wait ||
		!definition.Spec.Helm.RollbackOnFailure ||
		strings.TrimSpace(definition.Spec.Artifacts.TargetRegistry) == "" ||
		len(definition.Spec.Services) == 0 ||
		definition.Spec.HealthChecks == nil ||
		(len(definition.Spec.HealthChecks.Public) == 0 &&
			len(definition.Spec.HealthChecks.Workloads) == 0) ||
		len(definition.Spec.ReleaseProcess) == 0 ||
		len(definition.Spec.StopConditions) == 0 {
		return credentialEnvironmentContext{}, invalidCredentialDeclaration(
			fmt.Errorf("environment %q has an incomplete v3 EnvironmentDefinition", environmentName),
		)
	}
	for label, ref := range map[string]credentialEnvironmentSecretRef{
		"sourceCredentialRef":    definition.Spec.Artifacts.SourceCredentialRef,
		"targetCredentialRef":    definition.Spec.Artifacts.TargetCredentialRef,
		"runtimeImagePullSecret": definition.Spec.Artifacts.RuntimeImagePullSecret,
	} {
		if strings.TrimSpace(ref.Status) == "undeclared" || ref.DeploymentBlockedUntilDeclared {
			return credentialEnvironmentContext{}, invalidCredentialDeclaration(
				fmt.Errorf("environment %q %s is undeclared", environmentName, label),
			)
		}
		if strings.TrimSpace(ref.Status) != "" ||
			strings.TrimSpace(ref.Kind) != "KubernetesSecret" ||
			strings.TrimSpace(ref.Name) == "" {
			return credentialEnvironmentContext{}, invalidCredentialDeclaration(
				fmt.Errorf("environment %q %s is incomplete", environmentName, label),
			)
		}
	}
	if err := validateCredentialEnvironmentServicesV3(root, workflowPath, definition); err != nil {
		return credentialEnvironmentContext{}, err
	}
	return credentialEnvironmentContext{
		Target: credentialConfigurationTarget{
			Name:     deploy.Target,
			Cluster:  deploy.Cluster,
			Instance: deploy.Instance,
		},
		SourceRepository: definition.Spec.Source.Repository,
		SourceBranch:     definition.Spec.Source.Branch,
	}, nil
}

func validateCredentialEnvironmentServicesV3(
	root, workflowPath string,
	definition credentialEnvironmentDefinition,
) error {
	workflowRelative, err := credentialDeclarationRelativePath(workflowPath)
	if err != nil {
		return err
	}
	environmentDirectory := filepath.ToSlash(filepath.Dir(workflowRelative))
	expectedRoot := filepath.ToSlash(filepath.Join(
		"deploy", "environments", definition.Metadata.Name, "environment.yaml",
	))
	if filepath.ToSlash(workflowRelative) != expectedRoot {
		return invalidCredentialDeclaration(
			fmt.Errorf("v3 EnvironmentDefinition path must be %q", expectedRoot),
		)
	}
	seenPaths := make(map[string]string, len(definition.Spec.Services))
	for serviceName, servicePath := range definition.Spec.Services {
		if strings.TrimSpace(serviceName) == "" {
			return invalidCredentialDeclaration(errors.New("v3 service name is required"))
		}
		serviceRelative, err := credentialDeclarationRelativePath(servicePath)
		if err != nil {
			return err
		}
		normalizedServicePath := filepath.ToSlash(serviceRelative)
		if previous, duplicate := seenPaths[normalizedServicePath]; duplicate {
			return invalidCredentialDeclaration(
				fmt.Errorf("services %q and %q reference the same environment service", previous, serviceName),
			)
		}
		seenPaths[normalizedServicePath] = serviceName
		expectedDirectory := filepath.ToSlash(filepath.Join(environmentDirectory, "services"))
		if filepath.ToSlash(filepath.Dir(serviceRelative)) != expectedDirectory {
			return invalidCredentialDeclaration(
				fmt.Errorf("environment service %q must be under %q", serviceName, expectedDirectory),
			)
		}
		serviceData, err := readCredentialDeclarationFile(root, servicePath)
		if err != nil {
			return err
		}
		if _, err := credentialYAMLDocument(serviceData); err != nil {
			return err
		}
		var service credentialEnvironmentService
		if err := strictCredentialYAMLDecode(serviceData, &service); err != nil {
			return invalidCredentialDeclaration(err)
		}
		if service.APIVersion != definition.APIVersion ||
			service.Kind != "EnvironmentService" ||
			strings.TrimSpace(service.Metadata.Name) == "" ||
			filepath.Base(normalizedServicePath) != service.Metadata.Name+".yaml" ||
			service.Metadata.Environment != definition.Metadata.Name ||
			service.Spec.EnvironmentDefinition != filepath.ToSlash(workflowRelative) ||
			strings.TrimSpace(service.Spec.Artifact.Source) == "" ||
			strings.TrimSpace(service.Spec.Artifact.Target) == "" ||
			!strings.HasPrefix(service.Spec.Artifact.Source, strings.TrimRight(definition.Spec.Source.ImageRegistry, "/")+"/") ||
			!strings.HasPrefix(service.Spec.Artifact.Target, strings.TrimRight(definition.Spec.Artifacts.TargetRegistry, "/")+"/") ||
			service.Spec.Artifact.Identity != "sha256-digest" ||
			!validCredentialSHA256Digest(service.Spec.Artifact.Digest) ||
			service.Spec.Helm.Chart != definition.Spec.Helm.Chart ||
			service.Spec.Helm.Values != definition.Spec.Helm.Values ||
			strings.TrimSpace(service.Spec.Helm.Value) == "" ||
			strings.TrimSpace(service.Spec.Helm.Workload) == "" ||
			strings.TrimSpace(service.Spec.Helm.Container) == "" ||
			service.Spec.Helm.Replicas < 1 ||
			service.Spec.Verification.MinReadyReplicas < 1 ||
			service.Spec.Verification.MinReadyReplicas > service.Spec.Helm.Replicas {
			return invalidCredentialDeclaration(
				fmt.Errorf("environment service %q is incomplete or inconsistent", serviceName),
			)
		}
		if err := validateCredentialServiceDefinitionV3(root, serviceName, service); err != nil {
			return err
		}
	}
	return nil
}

func validateCredentialServiceDefinitionV3(
	root, serviceName string,
	environmentService credentialEnvironmentService,
) error {
	definitionPath, err := credentialDeclarationRelativePath(environmentService.Spec.ServiceDefinition)
	if err != nil {
		return err
	}
	if filepath.ToSlash(filepath.Dir(definitionPath)) != "deploy/services" {
		return invalidCredentialDeclaration(
			fmt.Errorf("service definition %q must be under deploy/services", serviceName),
		)
	}
	data, err := readCredentialDeclarationFile(root, environmentService.Spec.ServiceDefinition)
	if err != nil {
		return err
	}
	if _, err := credentialYAMLDocument(data); err != nil {
		return err
	}
	var definition credentialServiceDefinitionV3
	if err := strictCredentialYAMLDecode(data, &definition); err != nil {
		return invalidCredentialDeclaration(err)
	}
	if definition.APIVersion != environmentService.APIVersion ||
		definition.Kind != "ServiceDefinition" ||
		definition.Metadata.Name != environmentService.Metadata.Name ||
		filepath.Base(filepath.ToSlash(definitionPath)) != definition.Metadata.Name+".yaml" ||
		definition.Spec.Service != serviceName ||
		strings.TrimSpace(definition.Spec.Artifact) == "" ||
		strings.TrimSpace(definition.Spec.BuildManifest) == "" ||
		strings.TrimSpace(definition.Spec.SourceContext) == "" ||
		strings.TrimSpace(definition.Spec.Dockerfile) == "" ||
		definition.Spec.Helm.Value != environmentService.Spec.Helm.Value ||
		definition.Spec.Helm.Workload != environmentService.Spec.Helm.Workload ||
		definition.Spec.Helm.Container != environmentService.Spec.Helm.Container {
		return invalidCredentialDeclaration(
			fmt.Errorf("service definition %q is incomplete or inconsistent", serviceName),
		)
	}
	return nil
}

func validCredentialSHA256Digest(value string) bool {
	const prefix = "sha256:"
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, prefix) && validLowerHex(strings.TrimPrefix(value, prefix), 64)
}

func parseCredentialEnvironment(
	root, configurationPath, environmentName string,
) (credentialEnvironmentContext, error) {
	configurationData, err := readCredentialDeclarationFile(root, configurationPath)
	if err != nil {
		return credentialEnvironmentContext{}, err
	}
	if _, err := credentialYAMLDocument(configurationData); err != nil {
		return credentialEnvironmentContext{}, err
	}
	return credentialConfigurationForEnvironment(
		configurationData,
		environmentName,
	)
}

func credentialConfigurationForEnvironment(
	data []byte,
	environmentName string,
) (credentialEnvironmentContext, error) {
	var identity credentialWorkflowIdentity
	if err := yaml.Unmarshal(data, &identity); err != nil {
		return credentialEnvironmentContext{}, invalidCredentialDeclaration(err)
	}
	switch identity.Kind {
	case "EnvironmentRegistry":
		var configuration credentialConfigurationSource
		if err := strictCredentialYAMLDecode(data, &configuration); err != nil {
			return credentialEnvironmentContext{}, invalidCredentialDeclaration(err)
		}
		if configuration.APIVersion != "doops.sh/v2" || configuration.Kind != "EnvironmentRegistry" ||
			strings.TrimSpace(configuration.Metadata.Name) == "" {
			return credentialEnvironmentContext{}, invalidCredentialDeclaration(errors.New("invalid EnvironmentRegistry identity"))
		}
		environment, ok := configuration.Environments[environmentName]
		if !ok {
			return credentialEnvironmentContext{}, invalidCredentialDeclaration(fmt.Errorf("environment %q is not declared", environmentName))
		}
		return credentialEnvironmentContext{
			Target:           environment.Target,
			SourceRepository: configuration.ArtifactContract.SourceRepository,
			SourceBranch:     configuration.ArtifactContract.SourceBranch,
		}, nil
	default:
		return credentialEnvironmentContext{}, invalidCredentialDeclaration(
			fmt.Errorf("unsupported configuration kind %q", identity.Kind),
		)
	}
}

func credentialYAMLMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		node = node.Content[0]
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

func credentialYAMLScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
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
			if credentialSecretLikeKey(key.Value) &&
				!credentialKubernetesSecretReference(value) {
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
	}
	return nil
}

func credentialKubernetesSecretReference(value *yaml.Node) bool {
	if value == nil || value.Kind != yaml.MappingNode {
		return false
	}
	if credentialYAMLScalarValue(credentialYAMLMappingValue(value, "kind")) != "KubernetesSecret" ||
		credentialYAMLScalarValue(credentialYAMLMappingValue(value, "name")) == "" {
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

var credentialSecretLikeKeys = map[string]bool{
	"token":            true,
	"password":         true,
	"auth":             true,
	"data":             true,
	"stringdata":       true,
	"clientsecret":     true,
	"apikey":           true,
	"privatekey":       true,
	"registrypassword": true,
}

func credentialSecretLikeKey(key string) bool {
	normalized := strings.Map(func(char rune) rune {
		switch char {
		case '_', '-', '.', ' ':
			return -1
		default:
			return char
		}
	}, strings.ToLower(strings.TrimSpace(key)))
	if credentialSecretLikeKeys[normalized] {
		return true
	}
	for _, suffix := range []string{
		"token", "password", "privatekey", "clientsecret", "apikey", "accesskey", "secretkey",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}
