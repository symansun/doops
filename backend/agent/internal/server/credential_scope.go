package server

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	ErrInlineCredentialMaterial      = errors.New("inline credential material is forbidden")
	credentialInlineMaterialPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9]{20,255}\b`),
		regexp.MustCompile(`(?i)\bgithub_pat_[A-Za-z0-9_]{20,255}\b`),
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,255}\b`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
		regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,255}\b`),
		regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{16,255}\b`),
		regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36,255}\b`),
	}
)

const (
	credentialScopePlanAPIVersion = "doops.sh/credential-scope-plan/v1"
	credentialScopePlanKind       = "CredentialScopePlan"
)

type CredentialScopePlan struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	References []CredentialPlanReference `json:"references"`
}

type CredentialPlanReference struct {
	Name  string               `json:"name"`
	Scope CredentialGrantScope `json:"scope"`
}

type CredentialGrantScope struct {
	Cluster         string                 `json:"cluster"`
	Instance        string                 `json:"instance"`
	Namespace       string                 `json:"namespace"`
	Use             CredentialUse          `json:"use"`
	Workload        CredentialPlanWorkload `json:"workload"`
	ImageRepository string                 `json:"image_repository,omitempty"`
	ImageDigest     string                 `json:"image_digest,omitempty"`
	HelmRelease     string                 `json:"helm_release,omitempty"`
	HelmChart       string                 `json:"helm_chart,omitempty"`
	RequiredKeys    []string               `json:"required_keys,omitempty"`
}

type CredentialPlanWorkload struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func normalizeCredentialGrantScope(scope CredentialGrantScope) CredentialGrantScope {
	scope.Cluster = strings.TrimSpace(scope.Cluster)
	scope.Instance = strings.TrimSpace(scope.Instance)
	scope.Namespace = strings.TrimSpace(scope.Namespace)
	scope.Workload.Kind = strings.ToLower(strings.TrimSpace(scope.Workload.Kind))
	scope.Workload.Name = strings.TrimSpace(scope.Workload.Name)
	scope.ImageRepository = strings.Trim(strings.TrimSpace(scope.ImageRepository), "/")
	scope.ImageDigest = strings.TrimSpace(scope.ImageDigest)
	scope.HelmRelease = strings.TrimSpace(scope.HelmRelease)
	scope.HelmChart = strings.TrimSpace(scope.HelmChart)
	scope.RequiredKeys = normalizedCredentialKeys(scope.RequiredKeys)
	return scope
}

func validateCredentialGrantScope(scope CredentialGrantScope) error {
	scope = normalizeCredentialGrantScope(scope)
	if scope.Cluster == "" || scope.Instance == "" || !validKubernetesCredentialName(scope.Namespace) ||
		!validCredentialUse(scope.Use) || scope.Workload.Kind == "" || scope.Workload.Name == "" {
		return errors.New("credential_scope_invalid")
	}
	switch scope.Workload.Kind {
	case "deployment", "statefulset", "daemonset":
	default:
		return errors.New("credential_scope_invalid")
	}
	if !validKubernetesCredentialName(scope.Workload.Name) {
		return errors.New("credential_scope_invalid")
	}
	switch scope.Use {
	case CredentialUseImagePull:
		if scope.ImageRepository == "" || !validCredentialSHA256Digest(scope.ImageDigest) ||
			scope.HelmRelease != "" || scope.HelmChart != "" || len(scope.RequiredKeys) != 0 {
			return errors.New("credential_scope_invalid")
		}
	case CredentialUseHelmPull:
		if scope.HelmRelease == "" || scope.HelmChart == "" ||
			scope.ImageRepository != "" || scope.ImageDigest != "" || len(scope.RequiredKeys) != 0 {
			return errors.New("credential_scope_invalid")
		}
	case CredentialUseOpaqueSecret:
		if len(scope.RequiredKeys) == 0 || scope.ImageRepository != "" || scope.ImageDigest != "" ||
			scope.HelmRelease != "" || scope.HelmChart != "" {
			return errors.New("credential_scope_invalid")
		}
	case CredentialUseGitCheckout, CredentialUseTLS:
		if scope.ImageRepository != "" || scope.ImageDigest != "" ||
			scope.HelmRelease != "" || scope.HelmChart != "" || len(scope.RequiredKeys) != 0 {
			return errors.New("credential_scope_invalid")
		}
	}
	return nil
}

func normalizedCredentialKeys(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func credentialScopeEqual(left, right CredentialGrantScope) bool {
	left = normalizeCredentialGrantScope(left)
	right = normalizeCredentialGrantScope(right)
	if left.Cluster != right.Cluster || left.Instance != right.Instance ||
		left.Namespace != right.Namespace || left.Use != right.Use ||
		left.Workload != right.Workload || left.ImageRepository != right.ImageRepository ||
		left.ImageDigest != right.ImageDigest || left.HelmRelease != right.HelmRelease ||
		left.HelmChart != right.HelmChart || len(left.RequiredKeys) != len(right.RequiredKeys) {
		return false
	}
	for index := range left.RequiredKeys {
		if left.RequiredKeys[index] != right.RequiredKeys[index] {
			return false
		}
	}
	return true
}

func validCredentialSHA256Digest(value string) bool {
	const prefix = "sha256:"
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, prefix) && validLowerHex(strings.TrimPrefix(value, prefix), 64)
}

func validateCredentialScopePlan(plan CredentialScopePlan, explicitNames []string, cluster, instance string) error {
	if plan.APIVersion != credentialScopePlanAPIVersion || plan.Kind != credentialScopePlanKind ||
		len(plan.References) != len(explicitNames) {
		return errors.New("credential_scope_plan_invalid")
	}
	expected := make(map[string]struct{}, len(explicitNames))
	for _, name := range explicitNames {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("credential_scope_plan_invalid")
		}
		if _, duplicate := expected[name]; duplicate {
			return errors.New("credential_scope_plan_invalid")
		}
		expected[name] = struct{}{}
	}
	for index := range plan.References {
		reference := &plan.References[index]
		reference.Name = strings.TrimSpace(reference.Name)
		if _, ok := expected[reference.Name]; !ok {
			return errors.New("credential_scope_plan_invalid")
		}
		delete(expected, reference.Name)
		reference.Scope.Cluster = cluster
		reference.Scope.Instance = instance
		reference.Scope = normalizeCredentialGrantScope(reference.Scope)
		if err := validateCredentialGrantScope(reference.Scope); err != nil {
			return err
		}
	}
	if len(expected) != 0 {
		return errors.New("credential_scope_plan_invalid")
	}
	return nil
}

func credentialScalarContainsInlineMaterial(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, "private key-----") {
		return true
	}
	for _, pattern := range credentialInlineMaterialPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "basic ") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key := range parsed.Query() {
		if credentialSecretLikeKey(key) {
			return true
		}
	}
	return false
}

var credentialSecretLikeKeys = map[string]bool{
	"token": true, "password": true, "auth": true, "authorization": true,
	"cookie": true, "data": true, "stringdata": true, "secret": true,
	"sig": true, "clientsecret": true, "apikey": true, "privatekey": true,
	"registrypassword": true,
}

func normalizeCredentialKey(key string) string {
	return strings.Map(func(char rune) rune {
		switch char {
		case '_', '-', '.', ' ':
			return -1
		default:
			return char
		}
	}, strings.ToLower(strings.TrimSpace(key)))
}

func credentialSecretLikeKey(key string) bool {
	normalized := normalizeCredentialKey(key)
	if credentialSecretLikeKeys[normalized] {
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

func credentialScopePlanningInstruction(instruction string, names []string) string {
	return fmt.Sprintf(`Plan managed credential scopes for the deployment task below.
Do not execute tools and do not make changes. Return only JSON matching:
{"apiVersion":%q,"kind":%q,"references":[{"name":"...","scope":{"namespace":"...","use":"imagePull|gitCheckout|helmPull|tls|opaqueSecret","workload":{"kind":"Deployment|StatefulSet|DaemonSet","name":"..."},"image_repository":"...","image_digest":"sha256:...","helm_release":"...","helm_chart":"deploy/helm/...","required_keys":["..."]}}]}
Return every explicit credential name exactly once and no other name. Omit fields that do not apply. Never output credential values.
Explicit credential names: %q
Deployment task:
%s`, credentialScopePlanAPIVersion, credentialScopePlanKind, names, instruction)
}
