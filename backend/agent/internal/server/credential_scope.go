package server

import (
	"errors"
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
	DatabaseAssetID string                 `json:"database_asset_id,omitempty"`
	EnvironmentID   string                 `json:"environment_id,omitempty"`
	DatabaseEngine  string                 `json:"database_engine,omitempty"`
	DatabaseName    string                 `json:"database_name,omitempty"`
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
	scope.DatabaseAssetID = strings.TrimSpace(scope.DatabaseAssetID)
	scope.EnvironmentID = strings.TrimSpace(scope.EnvironmentID)
	scope.DatabaseEngine = strings.ToLower(strings.TrimSpace(scope.DatabaseEngine))
	scope.DatabaseName = strings.TrimSpace(scope.DatabaseName)
	scope.ImageRepository = strings.Trim(strings.TrimSpace(scope.ImageRepository), "/")
	scope.ImageDigest = strings.TrimSpace(scope.ImageDigest)
	scope.HelmRelease = strings.TrimSpace(scope.HelmRelease)
	scope.HelmChart = strings.TrimSpace(scope.HelmChart)
	scope.RequiredKeys = normalizedCredentialKeys(scope.RequiredKeys)
	if scope.Use == CredentialUseDatabase {
		// Database assets and execution semantics belong to DoSql. Gateway only
		// grants target-scoped use of the opaque credential and its staging namespace.
		scope.Workload = CredentialPlanWorkload{}
		scope.DatabaseAssetID = ""
		scope.EnvironmentID = ""
		scope.DatabaseEngine = ""
		scope.DatabaseName = ""
		scope.RequiredKeys = nil
	}
	return scope
}

func validateCredentialGrantScope(scope CredentialGrantScope) error {
	scope = normalizeCredentialGrantScope(scope)
	if scope.Cluster == "" || scope.Instance == "" || !validKubernetesCredentialName(scope.Namespace) ||
		!validCredentialUse(scope.Use) {
		return errors.New("credential_scope_invalid")
	}
	if scope.Use != CredentialUseDatabase && scope.Use != CredentialUseHelmPull {
		if scope.Workload.Kind == "" || scope.Workload.Name == "" {
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
	}
	switch scope.Use {
	case CredentialUseImagePull:
		if scope.ImageRepository == "" || !validCredentialSHA256Digest(scope.ImageDigest) ||
			scope.HelmRelease != "" || scope.HelmChart != "" || len(scope.RequiredKeys) != 0 {
			return errors.New("credential_scope_invalid")
		}
	case CredentialUseHelmPull:
		if scope.ImageRepository != "" || scope.ImageDigest != "" || len(scope.RequiredKeys) != 0 {
			return errors.New("credential_scope_invalid")
		}
	case CredentialUseOpaqueSecret:
		if len(scope.RequiredKeys) == 0 || scope.ImageRepository != "" || scope.ImageDigest != "" ||
			scope.HelmRelease != "" || scope.HelmChart != "" {
			return errors.New("credential_scope_invalid")
		}
	case CredentialUseDatabase:
		if scope.ImageRepository != "" || scope.ImageDigest != "" ||
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
	if left.Use == CredentialUseDatabase && right.Use == CredentialUseDatabase {
		return left.Cluster == right.Cluster && left.Instance == right.Instance &&
			left.Namespace == right.Namespace && left.Use == right.Use
	}
	if left.Use == CredentialUseHelmPull && right.Use == CredentialUseHelmPull {
		return left.Cluster == right.Cluster && left.Instance == right.Instance && left.Use == right.Use
	}
	if left.Cluster != right.Cluster || left.Instance != right.Instance ||
		left.Namespace != right.Namespace || left.Use != right.Use || left.Workload != right.Workload ||
		left.DatabaseAssetID != right.DatabaseAssetID || left.EnvironmentID != right.EnvironmentID ||
		left.DatabaseEngine != right.DatabaseEngine || left.DatabaseName != right.DatabaseName ||
		left.ImageRepository != right.ImageRepository || left.ImageDigest != right.ImageDigest ||
		left.HelmRelease != right.HelmRelease || left.HelmChart != right.HelmChart ||
		len(left.RequiredKeys) != len(right.RequiredKeys) {
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

func validDatabaseName(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validDatabaseCredentialKeys(engine string, keys []string) bool {
	want := map[string]struct{}{}
	switch engine {
	case "mysql":
		want = map[string]struct{}{"SPRING_DATASOURCE_URL": {}, "SPRING_DATASOURCE_USERNAME": {}, "SPRING_DATASOURCE_PASSWORD": {}}
	case "postgres", "mongodb":
		want = map[string]struct{}{"connection_uri": {}}
	default:
		return false
	}
	if len(keys) != len(want) {
		return false
	}
	for _, key := range keys {
		if _, ok := want[key]; !ok {
			return false
		}
	}
	return true
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
