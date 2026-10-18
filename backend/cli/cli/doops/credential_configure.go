package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var (
	configureCredentialIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	configureCredentialNamePattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	configureCredentialKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	configureCredentialDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func runCredentialConfigure(args []string, sessionName string, servers []Server, configErr error, stdin io.Reader, stdout io.Writer) error {
	var target, id, name, resourceScope, credentialType, requiredKeys string
	var fromSecret, keys, keyMap, connectionURI, passwordKey string
	var scope credentialGrantScope
	flags := credentialFlagSet("credential configure", stdout)
	flags.StringVar(&target, "target", "", "Configured Gateway target; supplies cluster and instance")
	flags.StringVar(&id, "id", "", "Existing managed credential ID; exclusive with --name, --type and --scope")
	flags.StringVar(&name, "name", "", "Unique managed credential name to reuse or create")
	flags.StringVar(&credentialType, "type", "", "Credential type (required with --name)")
	flags.StringVar(&resourceScope, "scope", "", "Credential scope: personal or platform (required with --name)")
	flags.StringVar(&scope.Namespace, "namespace", "default", "Secret materialization namespace (default: default)")
	flags.StringVar(&scope.Use, "use", "", "Optional credential use; inferred from credential type when omitted")
	flags.StringVar(&scope.Workload.Kind, "workload-kind", "", "Optional workload metadata for imagePull materialization")
	flags.StringVar(&scope.Workload.Name, "workload-name", "", "Optional workload metadata for imagePull materialization")
	flags.StringVar(&scope.ImageRepository, "image-repository", "", "Optional image verification repository")
	flags.StringVar(&scope.ImageDigest, "image-digest", "", "Optional image verification sha256 digest")
	flags.StringVar(&scope.HelmRelease, "helm-release", "", "Optional Helm execution metadata")
	flags.StringVar(&scope.HelmChart, "helm-chart", "", "Optional Helm execution metadata")
	flags.StringVar(&requiredKeys, "required-keys", "", "Optional Secret keys; payload keys are inferred when omitted")
	flags.StringVar(&fromSecret, "from-secret", "", "Import selected keys from this target Secret (opaque only); never reads stdin")
	flags.StringVar(&keys, "keys", "", "Explicit comma-separated source Secret keys (required with --from-secret)")
	flags.StringVar(&keyMap, "key-map", "", "Optional source=runtime Secret key mappings for --from-secret")
	flags.StringVar(&connectionURI, "connection-uri", "", "Passwordless postgres/postgresql/mongodb URL with a concrete database path; no query (including an empty ?) or fragment")
	flags.StringVar(&passwordKey, "password-key", "", "Source password key; must be the sole selected --keys entry with --connection-uri")
	flags.Usage = func() {
		fmt.Fprint(stdout, `Usage:
  doops -session <s> credential configure --target <t> [--namespace <ns>] [--use <use>]
    (--id <id> | --name <name> --type <type> --scope <personal|platform>) [scope flags]

Default source: stdin JSON, using the existing credential put payload format.
Alternative source: --from-secret <name> --keys <key,...> (opaque only).
These sources are mutually exclusive; --from-secret never reads stdin.
With --connection-uri, select exactly --password-key in --keys; the Agent builds
data.connection_uri without returning the source password. Never put passwords in a URL.
The URL requires a concrete database path, with no query (including an empty ?) or fragment.

Reuses a unique managed resource or creates it, stages a version, configures the
target binding, GETs all bindings, verifies every unique active target, then promotes.
Target operators inherit credential use from their target access; the binding is not a user grant.
Stops on the first failure, without retries or automatic cleanup.
Only phase IDs and metadata are printed. Re-running stages a new version.
No workload, repository, release or database asset fields are required for target authorization.

Example (no stdin read):
  doops -session setup credential configure --target db --namespace apps --use database \
    --name database --type opaque --scope personal --from-secret database --keys connection_uri

Flags:
`)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	for _, value := range []*string{&sessionName, &target, &id, &name, &resourceScope, &credentialType,
		&fromSecret, &keys, &keyMap, &connectionURI, &passwordKey, &scope.Namespace, &scope.Use,
		&scope.Workload.Kind, &scope.Workload.Name, &scope.ImageRepository, &scope.ImageDigest,
		&scope.HelmRelease, &scope.HelmChart} {
		*value = strings.TrimSpace(*value)
	}
	if flags.NArg() != 0 || sessionName == "" || target == "" {
		return fmt.Errorf("-session and --target are required; no positional arguments")
	}
	if sessionName == "." || strings.Contains(sessionName, "..") || strings.ContainsAny(sessionName, `/\`) {
		return fmt.Errorf("invalid -session")
	}
	if id != "" {
		if !configureCredentialIDPattern.MatchString(id) || name != "" || credentialType != "" || resourceScope != "" {
			return fmt.Errorf("--id must be valid and is exclusive with --name, --type and --scope")
		}
	} else if name == "" || credentialType == "" || (resourceScope != "personal" && resourceScope != "platform") {
		return fmt.Errorf("provide --id OR --name, --type and --scope (personal or platform)")
	}
	if scope.Use == "" && credentialType != "" {
		scope.Use = configureUseForType(credentialType)
	}
	var err error
	scope.RequiredKeys, err = parseOptionalCredentialList(requiredKeys, "--required-keys")
	if err != nil {
		return err
	}
	if err := validateConfigureCredentialScope(scope); err != nil {
		return err
	}
	wantType := map[string]string{
		"imagePull": "registry", "gitCheckout": "gitToken", "helmPull": "helmRepository",
		"tls": "tls", "opaqueSecret": "opaque", "database": "opaque",
	}[scope.Use]
	if credentialType != "" && credentialType != wantType {
		return fmt.Errorf("--type does not match --use")
	}
	selectedKeys, err := parseOptionalCredentialList(keys, "--keys")
	if err != nil {
		return err
	}
	selectedKeyMap, err := parseConfigureCredentialKeyMap(keyMap)
	if err != nil {
		return err
	}
	if fromSecret == "" {
		if keys != "" || keyMap != "" || connectionURI != "" || passwordKey != "" {
			return fmt.Errorf("--keys, --key-map, --connection-uri and --password-key require --from-secret")
		}
	} else {
		if wantType != "opaque" || !validConfigureCredentialName(fromSecret) || len(selectedKeys) == 0 {
			return fmt.Errorf("--from-secret requires an opaque credential, valid Secret name and explicit --keys")
		}
		seen := map[string]bool{}
		for _, key := range selectedKeys {
			if !configureCredentialKeyPattern.MatchString(key) || len(key) > 253 || seen[key] {
				return fmt.Errorf("--keys must contain distinct valid Secret keys")
			}
			seen[key] = true
		}
		for source := range selectedKeyMap {
			if !seen[source] {
				return fmt.Errorf("--key-map source %q must be included in --keys", source)
			}
		}
		if connectionURI != "" {
			if keyMap != "" {
				return fmt.Errorf("--key-map cannot be combined with --connection-uri")
			}
			if passwordKey == "" || len(selectedKeys) != 1 || selectedKeys[0] != passwordKey {
				return fmt.Errorf("--connection-uri requires exactly --password-key selected in --keys")
			}
			if err := validateConfigureConnectionURI(connectionURI); err != nil {
				return err
			}
		} else if passwordKey != "" {
			return fmt.Errorf("--password-key requires --connection-uri")
		}
	}
	gateway, token, cluster, instance, err := resolveCredentialListTarget(target, servers, configErr)
	if err != nil {
		return err
	}
	if strings.ContainsAny(cluster+instance, "*?") {
		return fmt.Errorf("--target must resolve an exact cluster/instance")
	}
	scope.Cluster, scope.Instance = cluster, instance
	scope = normalizeConfigureCredentialScope(scope)
	var payload json.RawMessage
	if fromSecret == "" {
		payload, err = readCredentialPayload(stdin)
		if err != nil {
			return fmt.Errorf("credential configure: invalid stdin payload")
		}
	}

	// Keep raw response bodies and transport errors out of credential output.
	request := func(method, endpoint string, query url.Values, body any, result any) error {
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				return fmt.Errorf("credential configure: invalid request")
			}
		}
		raw, requestErr := gatewayCredentialRequest(method, gateway, token, endpoint, query, data)
		if requestErr != nil {
			var httpErr *gatewayHTTPError
			if errors.As(requestErr, &httpErr) {
				return fmt.Errorf("credential configure %s %s failed: %s", method, endpoint, httpErr.Status)
			}
			return fmt.Errorf("credential configure %s %s failed: request unavailable", method, endpoint)
		}
		if json.Unmarshal(raw, result) != nil {
			return fmt.Errorf("credential configure %s %s: invalid response", method, endpoint)
		}
		return nil
	}
	var managed struct {
		Credentials []credentialResource `json:"credentials"`
	}
	if err := request(http.MethodGet, "/v1/credentials", url.Values{"view": {"managed"}}, nil, &managed); err != nil {
		return err
	}
	if managed.Credentials == nil {
		return fmt.Errorf("credential configure: managed response omitted credentials")
	}
	var resource credentialResource
	matches := 0
	for _, candidate := range managed.Credentials {
		if (id != "" && candidate.ID == id) || (id == "" && candidate.Name == name && candidate.Scope == resourceScope) {
			resource = candidate
			matches++
		}
	}
	if matches > 1 {
		return fmt.Errorf("credential configure: ambiguous managed resource; use an explicit --id")
	}
	if matches == 0 {
		if id != "" {
			return fmt.Errorf("credential configure: --id not found in managed resources")
		}
		if err := request(http.MethodPost, "/v1/credentials", nil, map[string]string{
			"name": name, "type": credentialType, "scope": resourceScope,
		}, &resource); err != nil {
			return err
		}
	}
	if !configureCredentialIDPattern.MatchString(resource.ID) || resource.State != "active" ||
		(id != "" && resource.ID != id) ||
		(id == "" && (resource.Name != name || resource.Scope != resourceScope)) {
		return fmt.Errorf("credential configure: managed resource metadata, type or state mismatch")
	}
	if scope.Use == "" {
		scope.Use = configureUseForType(resource.Type)
	}
	wantType = map[string]string{
		"imagePull": "registry", "gitCheckout": "gitToken", "helmPull": "helmRepository",
		"tls": "tls", "opaqueSecret": "opaque", "database": "opaque",
	}[scope.Use]
	if wantType == "" || resource.Type != wantType || validateConfigureCredentialScope(scope) != nil {
		return fmt.Errorf("credential configure: managed resource type or scope mismatch")
	}
	id = resource.ID
	if _, err := fmt.Fprintf(stdout, "phase=resource credential_id=%s\n", id); err != nil {
		return err
	}
	base := "/v1/credentials/" + id + "/"
	var version credentialVersion
	if fromSecret != "" {
		body := map[string]any{
			"session_id": sessionName, "cluster": cluster, "instance": instance,
			"namespace": scope.Namespace, "secret_name": fromSecret, "keys": selectedKeys,
		}
		if len(selectedKeyMap) != 0 {
			body["key_map"] = selectedKeyMap
		}
		if connectionURI != "" {
			body["connection_uri"], body["password_key"] = connectionURI, passwordKey
		}
		if err := request(http.MethodPost, base+"import-secret", nil, body, &version); err != nil {
			return err
		}
	} else if err := request(http.MethodPut, base+"payload", nil, payload, &version); err != nil {
		return err
	}
	if !configureCredentialIDPattern.MatchString(version.ID) || version.CredentialID != id || version.State != "staged" {
		return fmt.Errorf("credential configure: staging response omitted matching staged version metadata")
	}
	if _, err := fmt.Fprintf(stdout, "phase=staged credential_id=%s version_id=%s\n", id, version.ID); err != nil {
		return err
	}
	var selfGrant credentialGrant
	if err := request(http.MethodPost, base+"grants", nil, map[string]any{
		"scope": scope,
	}, &selfGrant); err != nil {
		return err
	}
	if !configureCredentialIDPattern.MatchString(selfGrant.ID) || selfGrant.CredentialID != id ||
		selfGrant.State != "active" ||
		configureCredentialScopeKey(selfGrant.Scope) != configureCredentialScopeKey(scope) {
		return fmt.Errorf("credential configure: target binding not confirmed")
	}
	if _, err := fmt.Fprintf(stdout, "phase=target-binding credential_id=%s binding_id=%s\n", id, selfGrant.ID); err != nil {
		return err
	}
	var history struct {
		Grants []credentialGrant `json:"grants"`
	}
	if err := request(http.MethodGet, base+"grants", nil, nil, &history); err != nil {
		return err
	}
	var scopes []credentialGrantScope
	counts := map[string]int{}
	foundSelf := false
	for _, grant := range history.Grants {
		if grant.State != "active" {
			continue
		}
		if grant.CredentialID != id || grant.ID == "" || grant.Scope.Cluster == "" || grant.Scope.Instance == "" ||
			validateConfigureCredentialScope(grant.Scope) != nil {
			return fmt.Errorf("credential configure: invalid active target binding metadata")
		}
		key := configureCredentialScopeKey(grant.Scope)
		if grant.ID == selfGrant.ID && key == configureCredentialScopeKey(scope) {
			foundSelf = true
		}
		if counts[key] == 0 {
			scopes = append(scopes, normalizeConfigureCredentialScope(grant.Scope))
		}
		counts[key]++
	}
	if !foundSelf {
		return fmt.Errorf("credential configure: target binding missing from active bindings")
	}
	for index, activeScope := range scopes {
		var verified credentialVerifyResponse
		if err := request(http.MethodPost, base+"verify", nil, map[string]any{
			"session_id": sessionName, "version_id": version.ID, "scope": activeScope,
		}, &verified); err != nil {
			return err
		}
		if verified.Status != "verified" || len(verified.Evidence) != counts[configureCredentialScopeKey(activeScope)] {
			return fmt.Errorf("credential configure: verification failed or evidence incomplete (scope %d)", index+1)
		}
		requiresDigest := (activeScope.Use == "imagePull" && activeScope.ImageRepository != "" && activeScope.ImageDigest != "") ||
			activeScope.Use == "gitCheckout" || activeScope.Use == "helmPull"
		for _, evidence := range verified.Evidence {
			if evidence.Status != "verified" || evidence.ErrorCategory != "" || evidence.ResourceVersion == "" ||
				(requiresDigest && evidence.Digest == "") {
				return fmt.Errorf("credential configure: verification evidence failed (scope %d)", index+1)
			}
		}
		if _, err := fmt.Fprintf(stdout, "phase=verified credential_id=%s version_id=%s scope=%d\n", id, version.ID, index+1); err != nil {
			return err
		}
	}
	var promoted struct {
		CredentialID string `json:"credential_id"`
		VersionID    string `json:"version_id"`
		Promoted     bool   `json:"promoted"`
	}
	if err := request(http.MethodPost, base+"promote", nil, map[string]string{"version_id": version.ID}, &promoted); err != nil {
		return err
	}
	if !promoted.Promoted || promoted.CredentialID != id || promoted.VersionID != version.ID {
		return fmt.Errorf("credential configure: promotion not confirmed")
	}
	_, err = fmt.Fprintf(stdout, "phase=promoted credential_id=%s version_id=%s\n", id, version.ID)
	return err
}

func parseConfigureCredentialKeyMap(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	result := make(map[string]string)
	destinations := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("--key-map must be a comma-separated source=runtime list")
		}
		source, destination := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if !configureCredentialKeyPattern.MatchString(source) || !configureCredentialKeyPattern.MatchString(destination) {
			return nil, fmt.Errorf("--key-map contains an invalid Secret key")
		}
		if _, exists := result[source]; exists {
			return nil, fmt.Errorf("--key-map contains duplicate source key %q", source)
		}
		if previous, exists := destinations[destination]; exists && previous != source {
			return nil, fmt.Errorf("--key-map contains duplicate runtime key %q", destination)
		}
		result[source] = destination
		destinations[destination] = source
	}
	return result, nil
}

func validConfigureCredentialName(value string) bool {
	return len(value) <= 253 && configureCredentialNamePattern.MatchString(value)
}

func validateConfigureCredentialScope(scope credentialGrantScope) error {
	if !validConfigureCredentialName(scope.Namespace) {
		return fmt.Errorf("--namespace must be a valid Kubernetes name")
	}
	if scope.Use == "" {
		return nil
	}
	if scope.Workload.Kind != "" || scope.Workload.Name != "" {
		switch strings.ToLower(scope.Workload.Kind) {
		case "deployment", "statefulset", "daemonset":
		default:
			return fmt.Errorf("--workload-kind must be Deployment, StatefulSet or DaemonSet when provided")
		}
		if !validConfigureCredentialName(scope.Workload.Name) {
			return fmt.Errorf("--workload-name must be valid when provided")
		}
	}
	image := scope.ImageRepository != "" || scope.ImageDigest != ""
	helm := scope.HelmRelease != "" || scope.HelmChart != ""
	keys := len(scope.RequiredKeys) != 0
	switch scope.Use {
	case "imagePull":
		if !helm && !keys && (!image || (strings.Trim(scope.ImageRepository, "/") != "" && configureCredentialDigestPattern.MatchString(scope.ImageDigest))) {
			return nil
		}
	case "helmPull":
		if !image && !keys {
			return nil
		}
	case "opaqueSecret":
		if !image && !helm {
			if !keys {
				return nil
			}
			for _, key := range scope.RequiredKeys {
				if !configureCredentialKeyPattern.MatchString(key) || len(key) > 253 {
					return fmt.Errorf("--required-keys must contain valid Secret keys")
				}
			}
			return nil
		}
	case "database", "gitCheckout", "tls":
		if !image && !helm && (scope.Use == "database" || !keys) {
			return nil
		}
	}
	return fmt.Errorf("invalid optional execution metadata for --use")
}

func configureUseForType(credentialType string) string {
	switch credentialType {
	case "registry":
		return "imagePull"
	case "gitToken":
		return "gitCheckout"
	case "helmRepository":
		return "helmPull"
	case "tls":
		return "tls"
	case "opaque":
		return "opaqueSecret"
	default:
		return ""
	}
}

func normalizeConfigureCredentialScope(scope credentialGrantScope) credentialGrantScope {
	scope.Workload.Kind = strings.ToLower(scope.Workload.Kind)
	scope.ImageRepository = strings.Trim(scope.ImageRepository, "/")
	scope.RequiredKeys = slices.Clone(scope.RequiredKeys)
	slices.Sort(scope.RequiredKeys)
	scope.RequiredKeys = slices.Compact(scope.RequiredKeys)
	if scope.Use == "database" {
		scope.Workload = credentialGrantWorkload{}
		scope.DatabaseAssetID, scope.EnvironmentID, scope.DatabaseEngine, scope.DatabaseName = "", "", "", ""
		scope.RequiredKeys = nil
	}
	return scope
}

func configureCredentialScopeKey(scope credentialGrantScope) string {
	scope = normalizeConfigureCredentialScope(scope)
	scope.Namespace = ""
	scope.Workload = credentialGrantWorkload{}
	scope.DatabaseAssetID, scope.EnvironmentID, scope.DatabaseEngine, scope.DatabaseName = "", "", "", ""
	scope.ImageRepository, scope.ImageDigest, scope.HelmRelease, scope.HelmChart = "", "", "", ""
	scope.RequiredKeys = nil
	data, _ := json.Marshal(scope)
	return string(data)
}

func validateConfigureConnectionURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User == nil ||
		parsed.User.Username() == "" {
		return fmt.Errorf("--connection-uri must be a passwordless postgres/postgresql/mongodb URL with username and host")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" && parsed.Scheme != "mongodb" {
		return fmt.Errorf("--connection-uri scheme must be postgres, postgresql or mongodb")
	}
	if _, present := parsed.User.Password(); present {
		return fmt.Errorf("--connection-uri must not contain a password")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return fmt.Errorf("--connection-uri requires a concrete database path")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return fmt.Errorf("--connection-uri must not contain a query (including an empty ?) or fragment")
	}
	return nil
}
