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
	var fromSecret, keys, connectionURI, passwordKey string
	var scope credentialGrantScope
	flags := credentialFlagSet("credential configure", stdout)
	flags.StringVar(&target, "target", "", "Configured Gateway target; supplies cluster and instance")
	flags.StringVar(&id, "id", "", "Existing managed credential ID; exclusive with --name, --type and --scope")
	flags.StringVar(&name, "name", "", "Unique managed credential name to reuse or create")
	flags.StringVar(&credentialType, "type", "", "Credential type (required with --name)")
	flags.StringVar(&resourceScope, "scope", "", "Credential scope: personal or platform (required with --name)")
	flags.StringVar(&scope.Namespace, "namespace", "", "Exact Secret materialization namespace")
	flags.StringVar(&scope.Use, "use", "", "Credential use: imagePull, gitCheckout, helmPull, tls, opaqueSecret, or database")
	flags.StringVar(&scope.Workload.Kind, "workload-kind", "", "Workload kind (required except for database)")
	flags.StringVar(&scope.Workload.Name, "workload-name", "", "Workload name (required except for database)")
	flags.StringVar(&scope.ImageRepository, "image-repository", "", "Authorized image repository")
	flags.StringVar(&scope.ImageDigest, "image-digest", "", "Authorized image sha256 digest")
	flags.StringVar(&scope.HelmRelease, "helm-release", "", "Authorized Helm release")
	flags.StringVar(&scope.HelmChart, "helm-chart", "", "Authorized Helm chart")
	flags.StringVar(&requiredKeys, "required-keys", "", "Comma-separated required Secret keys for opaqueSecret")
	flags.StringVar(&fromSecret, "from-secret", "", "Import selected keys from this target Secret (opaque only); never reads stdin")
	flags.StringVar(&keys, "keys", "", "Explicit comma-separated source Secret keys (required with --from-secret)")
	flags.StringVar(&connectionURI, "connection-uri", "", "Passwordless postgres/postgresql/mongodb URL with a concrete database path; no query (including an empty ?) or fragment")
	flags.StringVar(&passwordKey, "password-key", "", "Source password key; must be the sole selected --keys entry with --connection-uri")
	flags.Usage = func() {
		fmt.Fprint(stdout, `Usage:
  doops -session <s> credential configure --target <t> --namespace <ns> --use <use>
    (--id <id> | --name <name> --type <type> --scope <personal|platform>) [scope flags]

Default source: stdin JSON, using the existing credential put payload format.
Alternative source: --from-secret <name> --keys <key,...> (opaque only).
These sources are mutually exclusive; --from-secret never reads stdin.
With --connection-uri, select exactly --password-key in --keys; the Agent builds
data.connection_uri without returning the source password. Never put passwords in a URL.
The URL requires a concrete database path, with no query (including an empty ?) or fragment.

Reuses a unique managed resource or creates it, stages a version, grants the exact
scope to the current user, GETs all grants, verifies every unique active scope,
then promotes. Stops on the first failure, without retries or automatic cleanup.
Only phase IDs and metadata are printed. Re-running stages a new version.
Database grants require no workload, database asset, environment or database name.

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
		&fromSecret, &keys, &connectionURI, &passwordKey, &scope.Namespace, &scope.Use,
		&scope.Workload.Kind, &scope.Workload.Name, &scope.ImageRepository, &scope.ImageDigest,
		&scope.HelmRelease, &scope.HelmChart} {
		*value = strings.TrimSpace(*value)
	}
	if flags.NArg() != 0 || sessionName == "" || target == "" || scope.Namespace == "" || scope.Use == "" {
		return fmt.Errorf("-session, --target, --namespace and --use are required; no positional arguments")
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
	if fromSecret == "" {
		if keys != "" || connectionURI != "" || passwordKey != "" {
			return fmt.Errorf("--keys, --connection-uri and --password-key require --from-secret")
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
		if connectionURI != "" {
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
		resource.Type != wantType || (id != "" && resource.ID != id) ||
		(id == "" && (resource.Name != name || resource.Scope != resourceScope)) {
		return fmt.Errorf("credential configure: managed resource metadata, type or state mismatch")
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
		"grantee_id": "", "scope": scope,
	}, &selfGrant); err != nil {
		return err
	}
	if !configureCredentialIDPattern.MatchString(selfGrant.ID) || selfGrant.CredentialID != id ||
		selfGrant.GranteeID == "" || selfGrant.State != "active" ||
		configureCredentialScopeKey(selfGrant.Scope) != configureCredentialScopeKey(scope) {
		return fmt.Errorf("credential configure: exact self grant not confirmed")
	}
	if _, err := fmt.Fprintf(stdout, "phase=grant credential_id=%s grant_id=%s\n", id, selfGrant.ID); err != nil {
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
			return fmt.Errorf("credential configure: invalid active grant metadata")
		}
		key := configureCredentialScopeKey(grant.Scope)
		if grant.ID == selfGrant.ID && grant.GranteeID == selfGrant.GranteeID && key == configureCredentialScopeKey(scope) {
			foundSelf = true
		}
		if counts[key] == 0 {
			scopes = append(scopes, normalizeConfigureCredentialScope(grant.Scope))
		}
		counts[key]++
	}
	if !foundSelf {
		return fmt.Errorf("credential configure: exact self grant missing from active grants")
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
		requiresDigest := activeScope.Use == "imagePull" || activeScope.Use == "gitCheckout" || activeScope.Use == "helmPull"
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

func validConfigureCredentialName(value string) bool {
	return len(value) <= 253 && configureCredentialNamePattern.MatchString(value)
}

func validateConfigureCredentialScope(scope credentialGrantScope) error {
	if !validConfigureCredentialName(scope.Namespace) {
		return fmt.Errorf("--namespace must be a valid Kubernetes name")
	}
	if scope.Use != "database" {
		switch strings.ToLower(scope.Workload.Kind) {
		case "deployment", "statefulset", "daemonset":
		default:
			return fmt.Errorf("--workload-kind must be Deployment, StatefulSet or DaemonSet except for database")
		}
		if !validConfigureCredentialName(scope.Workload.Name) {
			return fmt.Errorf("--workload-name is required and must be valid except for database")
		}
	}
	image := scope.ImageRepository != "" || scope.ImageDigest != ""
	helm := scope.HelmRelease != "" || scope.HelmChart != ""
	keys := len(scope.RequiredKeys) != 0
	switch scope.Use {
	case "imagePull":
		if strings.Trim(scope.ImageRepository, "/") != "" && configureCredentialDigestPattern.MatchString(scope.ImageDigest) && !helm && !keys {
			return nil
		}
	case "helmPull":
		if scope.HelmRelease != "" && scope.HelmChart != "" && !image && !keys {
			return nil
		}
	case "opaqueSecret":
		if keys && !image && !helm {
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
	return fmt.Errorf("invalid --use or missing/conflicting image, Helm or required-key scope flags")
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
	if scope.Use == "helmPull" {
		scope.HelmChart = configureHelmChartIdentity(scope.HelmChart)
	}
	data, _ := json.Marshal(normalizeConfigureCredentialScope(scope))
	return string(data)
}

func configureHelmChartIdentity(raw string) string {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return value
	}
	path := parsed.EscapedPath()
	marker := strings.LastIndex(path, "/v/")
	if marker <= 0 || !strings.HasSuffix(path, ".tgz") {
		return value
	}
	versionPath := path[marker+len("/v/"):]
	if versionPath == "" || strings.Contains(versionPath, "/") {
		return value
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path[:marker]
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
