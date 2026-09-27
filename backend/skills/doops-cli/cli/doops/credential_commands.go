package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxCredentialPayloadBytes = 1 << 20

type credentialResource struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Scope  string            `json:"scope"`
	Type   string            `json:"type"`
	State  string            `json:"state"`
	Grants []credentialGrant `json:"grants,omitempty"`
}

type credentialVersion struct {
	ID            string `json:"id"`
	CredentialID  string `json:"credential_id"`
	PayloadDigest string `json:"payload_digest"`
	State         string `json:"state"`
}

type credentialGrant struct {
	ID           string               `json:"id"`
	CredentialID string               `json:"credential_id"`
	GranteeID    string               `json:"grantee_id"`
	Scope        credentialGrantScope `json:"scope"`
	State        string               `json:"state"`
}

type credentialGrantScope struct {
	Cluster         string                  `json:"cluster"`
	Instance        string                  `json:"instance"`
	Namespace       string                  `json:"namespace"`
	Use             string                  `json:"use"`
	Workload        credentialGrantWorkload `json:"workload"`
	ImageRepository string                  `json:"image_repository,omitempty"`
	ImageDigest     string                  `json:"image_digest,omitempty"`
	HelmRelease     string                  `json:"helm_release,omitempty"`
	HelmChart       string                  `json:"helm_chart,omitempty"`
	RequiredKeys    []string                `json:"required_keys,omitempty"`
}

type credentialGrantWorkload struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type credentialBundle struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
	State string `json:"state"`
}

type credentialVerificationEvidence struct {
	Status          string `json:"status"`
	ResourceVersion string `json:"resourceVersion"`
	Digest          string `json:"digest"`
	ErrorCategory   string `json:"errorCategory"`
}

type credentialVerifyResponse struct {
	Status   string                           `json:"status"`
	Evidence []credentialVerificationEvidence `json:"evidence"`
}

func runCredentialCommand(args []string, sessionName string, servers []Server, configErr error, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprint(stdout, `Usage:
  doops credential create        创建凭据元数据
  doops credential list          查询当前 target 可见或当前用户管理的凭据
  doops credential preflight     检查项目开发所需 credentialRef 是否可用
  doops credential put           从 stdin 写入新的凭据版本
  doops credential grant         授权凭据给当前用户或指定用户的精确 target scope
  doops credential grant-revoke  吊销一条凭据授权
  doops credential verify        在精确 target scope 验证 staged 版本
  doops credential promote       激活已验证的凭据版本
  doops credential revoke        吊销凭据资源
  doops credential bundle-create 从 stdin 创建凭据 bundle
  doops credential bundle-get    按名称查询凭据 bundle

Use "doops credential <subcommand> -h" for command flags.
`)
		return err
	}
	var err error
	switch args[0] {
	case "create":
		err = runCredentialCreate(args[1:], servers, configErr, stdout)
	case "list":
		err = runCredentialList(args[1:], servers, configErr, stdout)
	case "preflight":
		err = runCredentialPreflight(args[1:], sessionName, servers, configErr, stdout)
	case "put":
		err = runCredentialPut(args[1:], servers, configErr, stdin, stdout)
	case "grant":
		err = runCredentialGrant(args[1:], servers, configErr, stdout)
	case "grant-revoke":
		err = runCredentialGrantRevoke(args[1:], servers, configErr, stdout)
	case "verify":
		err = runCredentialVerify(args[1:], sessionName, servers, configErr, stdout)
	case "promote":
		err = runCredentialPromote(args[1:], servers, configErr, stdout)
	case "revoke":
		err = runCredentialRevoke(args[1:], servers, configErr, stdout)
	case "bundle-create":
		err = runCredentialBundleCreate(args[1:], servers, configErr, stdin, stdout)
	case "bundle-get":
		err = runCredentialBundleGet(args[1:], servers, configErr, stdout)
	default:
		return fmt.Errorf("unsupported credential subcommand %q", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runCredentialVerify(args []string, sessionName string, servers []Server, configErr error, stdout io.Writer) error {
	var target, id, versionID string
	var cluster, instance, namespace, use, workloadKind, workloadName string
	var imageRepository, imageDigest, helmRelease, helmChart, requiredKeys string
	flags := credentialFlagSet("credential verify", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&id, "id", "", "Credential ID")
	flags.StringVar(&versionID, "version-id", "", "Staged credential version ID")
	flags.StringVar(&cluster, "cluster", "", "Target cluster")
	flags.StringVar(&instance, "instance", "", "Target instance")
	flags.StringVar(&namespace, "namespace", "", "Kubernetes namespace")
	flags.StringVar(&use, "use", "", "Credential use: imagePull, gitCheckout, helmPull, tls, or opaqueSecret")
	flags.StringVar(&workloadKind, "workload-kind", "", "Kubernetes workload kind")
	flags.StringVar(&workloadName, "workload-name", "", "Kubernetes workload name")
	flags.StringVar(&imageRepository, "image-repository", "", "Authorized image repository")
	flags.StringVar(&imageDigest, "image-digest", "", "Authorized image digest")
	flags.StringVar(&helmRelease, "helm-release", "", "Authorized Helm release")
	flags.StringVar(&helmChart, "helm-chart", "", "Authorized Helm chart")
	flags.StringVar(&requiredKeys, "required-keys", "", "Comma-separated required Secret keys")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(sessionName) == "" ||
		strings.TrimSpace(id) == "" || strings.TrimSpace(versionID) == "" ||
		strings.TrimSpace(cluster) == "" || strings.TrimSpace(instance) == "" ||
		strings.TrimSpace(namespace) == "" || strings.TrimSpace(use) == "" ||
		strings.TrimSpace(workloadKind) == "" || strings.TrimSpace(workloadName) == "" {
		return fmt.Errorf("-session, --id, --version-id, --cluster, --instance, --namespace, --use, --workload-kind and --workload-name are required")
	}
	keyList, err := parseOptionalCredentialList(requiredKeys, "--required-keys")
	if err != nil {
		return err
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	endpoint, err := credentialEndpoint(id, "verify")
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"version_id": strings.TrimSpace(versionID),
		"session_id": strings.TrimSpace(sessionName),
		"scope": credentialGrantScope{
			Cluster:   strings.TrimSpace(cluster),
			Instance:  strings.TrimSpace(instance),
			Namespace: strings.TrimSpace(namespace),
			Use:       strings.TrimSpace(use),
			Workload: credentialGrantWorkload{
				Kind: strings.TrimSpace(workloadKind),
				Name: strings.TrimSpace(workloadName),
			},
			ImageRepository: strings.TrimSpace(imageRepository),
			ImageDigest:     strings.TrimSpace(imageDigest),
			HelmRelease:     strings.TrimSpace(helmRelease),
			HelmChart:       strings.TrimSpace(helmChart),
			RequiredKeys:    keyList,
		},
	})
	if err != nil {
		return err
	}
	raw, err := gatewayCredentialRequest(http.MethodPost, gateway, token, endpoint, nil, body)
	if err != nil {
		return err
	}
	var response credentialVerifyResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode credential verify response: %w", err)
	}
	if strings.TrimSpace(response.Status) == "" {
		return fmt.Errorf("credential verify response omitted status")
	}
	if _, err := fmt.Fprintf(stdout, "credential_id=%s version_id=%s status=%s evidence=%d\n",
		strings.TrimSpace(id), strings.TrimSpace(versionID), response.Status, len(response.Evidence)); err != nil {
		return err
	}
	for _, evidence := range response.Evidence {
		if _, err := fmt.Fprintf(stdout,
			"  status=%s resource_version=%s digest=%s error_category=%s\n",
			evidence.Status, evidence.ResourceVersion, evidence.Digest, evidence.ErrorCategory); err != nil {
			return err
		}
	}
	return nil
}

func runCredentialGrantRevoke(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, id, grantID string
	flags := credentialFlagSet("credential grant-revoke", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&id, "id", "", "Credential ID")
	flags.StringVar(&grantID, "grant-id", "", "Credential grant ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(id) == "" || strings.TrimSpace(grantID) == "" {
		return fmt.Errorf("--id and --grant-id are required")
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	endpoint, err := credentialEndpoint(id, "grants")
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"grant_id": strings.TrimSpace(grantID)})
	if err != nil {
		return err
	}
	if _, err := gatewayCredentialRequest(http.MethodDelete, gateway, token, endpoint, nil, body); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "credential_id=%s credential_grant_id=%s revoked=true\n",
		strings.TrimSpace(id), strings.TrimSpace(grantID))
	return err
}

func runCredentialBundleCreate(args []string, servers []Server, configErr error, stdin io.Reader, stdout io.Writer) error {
	var target string
	flags := credentialFlagSet("credential bundle-create", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("credential bundle-create accepts no positional arguments")
	}
	body, err := readCredentialPayload(stdin)
	if err != nil {
		return err
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	raw, err := gatewayCredentialRequest(http.MethodPost, gateway, token, "/v1/credential-bundles", nil, body)
	if err != nil {
		return err
	}
	var bundle credentialBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("decode credential bundle response: %w", err)
	}
	if strings.TrimSpace(bundle.ID) == "" {
		return fmt.Errorf("credential bundle response omitted id")
	}
	_, err = fmt.Fprintf(stdout, "credential_bundle_id=%s name=%s scope=%s state=%s\n",
		bundle.ID, bundle.Name, bundle.Scope, bundle.State)
	return err
}

func runCredentialBundleGet(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, name string
	flags := credentialFlagSet("credential bundle-get", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&name, "name", "", "Credential bundle name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(name) == "" {
		return fmt.Errorf("--name is required")
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	raw, err := gatewayCredentialRequest(http.MethodGet, gateway, token, "/v1/credential-bundles",
		url.Values{"name": []string{strings.TrimSpace(name)}}, nil)
	if err != nil {
		return err
	}
	var bundle credentialBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return fmt.Errorf("decode credential bundle response: %w", err)
	}
	if strings.TrimSpace(bundle.ID) == "" {
		return fmt.Errorf("credential bundle response omitted id")
	}
	_, err = fmt.Fprintf(stdout, "credential_bundle_id=%s name=%s scope=%s state=%s\n",
		bundle.ID, bundle.Name, bundle.Scope, bundle.State)
	return err
}

func runCredentialCreate(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, name, scope, credentialType string
	flags := credentialFlagSet("credential create", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&name, "name", "", "Credential name")
	flags.StringVar(&scope, "scope", "", "Credential scope: personal or platform")
	flags.StringVar(&credentialType, "type", "", "Credential type")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(scope) == "" || strings.TrimSpace(credentialType) == "" {
		return fmt.Errorf("--name, --scope and --type are required")
	}
	var err error
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"name":  strings.TrimSpace(name),
		"scope": strings.TrimSpace(scope),
		"type":  strings.TrimSpace(credentialType),
	})
	if err != nil {
		return err
	}
	raw, err := gatewayCredentialRequest(http.MethodPost, gateway, token, "/v1/credentials", nil, body)
	if err != nil {
		return err
	}
	var credential credentialResource
	if err := json.Unmarshal(raw, &credential); err != nil {
		return fmt.Errorf("decode credential create response: %w", err)
	}
	if strings.TrimSpace(credential.ID) == "" {
		return fmt.Errorf("credential create response omitted id")
	}
	_, err = fmt.Fprintf(stdout, "credential_id=%s\n", credential.ID)
	return err
}

func runCredentialList(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, view string
	flags := credentialFlagSet("credential list", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&view, "view", "target", "Credential view: target or managed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("credential list accepts no positional arguments")
	}
	view = strings.TrimSpace(view)
	if view != "target" && view != "managed" {
		return fmt.Errorf("--view must be target or managed")
	}
	credentials, err := listCredentialResources(target, view, servers, configErr)
	if err != nil {
		return err
	}
	for _, credential := range credentials {
		if _, err := fmt.Fprintf(stdout, "credential_id=%s name=%s scope=%s type=%s state=%s grants=%d\n",
			credential.ID, credential.Name, credential.Scope, credential.Type, credential.State,
			len(credential.Grants)); err != nil {
			return err
		}
		for _, grant := range credential.Grants {
			if _, err := fmt.Fprintf(stdout,
				"  grant_id=%s cluster=%s instance=%s namespace=%s use=%s workload=%s/%s\n",
				grant.ID, grant.Scope.Cluster, grant.Scope.Instance, grant.Scope.Namespace,
				grant.Scope.Use, grant.Scope.Workload.Kind, grant.Scope.Workload.Name,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func listCredentialResources(
	target string,
	view string,
	servers []Server,
	configErr error,
) ([]credentialResource, error) {
	gateway, token, routeCluster, routeInstance, err := resolveCredentialListTarget(
		target, servers, configErr,
	)
	if err != nil {
		return nil, err
	}
	query := url.Values{"view": []string{view}}
	if view == "target" {
		if routeCluster == "" || routeInstance == "" {
			return nil, fmt.Errorf("target credential view requires --target")
		}
		query.Set("cluster", routeCluster)
		query.Set("instance", routeInstance)
	}
	raw, err := gatewayCredentialRequest(http.MethodGet, gateway, token, "/v1/credentials", query, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Credentials []credentialResource `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode credential list response: %w", err)
	}
	return response.Credentials, nil
}

func runCredentialPut(args []string, servers []Server, configErr error, stdin io.Reader, stdout io.Writer) error {
	var target, id string
	flags := credentialFlagSet("credential put", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&id, "id", "", "Credential ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(id) == "" {
		return fmt.Errorf("--id is required")
	}
	payload, err := readCredentialPayload(stdin)
	if err != nil {
		return err
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	endpoint, err := credentialEndpoint(id, "payload")
	if err != nil {
		return err
	}
	raw, err := gatewayCredentialRequest(http.MethodPut, gateway, token, endpoint, nil, payload)
	if err != nil {
		return err
	}
	var version credentialVersion
	if err := json.Unmarshal(raw, &version); err != nil {
		return fmt.Errorf("decode credential put response: %w", err)
	}
	if strings.TrimSpace(version.ID) == "" || strings.TrimSpace(version.CredentialID) == "" {
		return fmt.Errorf("credential put response omitted version metadata")
	}
	_, err = fmt.Fprintf(stdout, "credential_id=%s version_id=%s state=%s payload_digest=%s\n",
		version.CredentialID, version.ID, version.State, version.PayloadDigest)
	return err
}

func runCredentialGrant(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, id, granteeID string
	var cluster, instance, namespace, use, workloadKind, workloadName string
	var imageRepository, imageDigest, helmRelease, helmChart, requiredKeys string
	flags := credentialFlagSet("credential grant", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&id, "id", "", "Credential ID")
	flags.StringVar(&granteeID, "grantee-id", "", "Gateway user ID receiving the grant")
	flags.StringVar(&cluster, "cluster", "", "Target cluster")
	flags.StringVar(&instance, "instance", "", "Target instance")
	flags.StringVar(&namespace, "namespace", "", "Kubernetes namespace")
	flags.StringVar(&use, "use", "", "Credential use: imagePull, gitCheckout, helmPull, tls, or opaqueSecret")
	flags.StringVar(&workloadKind, "workload-kind", "", "Kubernetes workload kind")
	flags.StringVar(&workloadName, "workload-name", "", "Kubernetes workload name")
	flags.StringVar(&imageRepository, "image-repository", "", "Authorized image repository")
	flags.StringVar(&imageDigest, "image-digest", "", "Authorized image digest")
	flags.StringVar(&helmRelease, "helm-release", "", "Authorized Helm release")
	flags.StringVar(&helmChart, "helm-chart", "", "Authorized Helm chart")
	flags.StringVar(&requiredKeys, "required-keys", "", "Comma-separated required Secret keys")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(id) == "" ||
		strings.TrimSpace(cluster) == "" || strings.TrimSpace(instance) == "" || strings.TrimSpace(namespace) == "" ||
		strings.TrimSpace(use) == "" || strings.TrimSpace(workloadKind) == "" || strings.TrimSpace(workloadName) == "" {
		return fmt.Errorf("--id, --cluster, --instance, --namespace, --use, --workload-kind and --workload-name are required")
	}
	keyList, err := parseOptionalCredentialList(requiredKeys, "--required-keys")
	if err != nil {
		return err
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	endpoint, err := credentialEndpoint(id, "grants")
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"grantee_id": strings.TrimSpace(granteeID),
		"scope": credentialGrantScope{
			Cluster:   strings.TrimSpace(cluster),
			Instance:  strings.TrimSpace(instance),
			Namespace: strings.TrimSpace(namespace),
			Use:       strings.TrimSpace(use),
			Workload: credentialGrantWorkload{
				Kind: strings.TrimSpace(workloadKind),
				Name: strings.TrimSpace(workloadName),
			},
			ImageRepository: strings.TrimSpace(imageRepository),
			ImageDigest:     strings.TrimSpace(imageDigest),
			HelmRelease:     strings.TrimSpace(helmRelease),
			HelmChart:       strings.TrimSpace(helmChart),
			RequiredKeys:    keyList,
		},
	})
	if err != nil {
		return err
	}
	raw, err := gatewayCredentialRequest(http.MethodPost, gateway, token, endpoint, nil, body)
	if err != nil {
		return err
	}
	var grant credentialGrant
	if err := json.Unmarshal(raw, &grant); err != nil {
		return fmt.Errorf("decode credential grant response: %w", err)
	}
	if strings.TrimSpace(grant.ID) == "" {
		return fmt.Errorf("credential grant response omitted id")
	}
	_, err = fmt.Fprintf(stdout, "credential_grant_id=%s credential_id=%s grantee_id=%s\n", grant.ID, grant.CredentialID, grant.GranteeID)
	return err
}

func runCredentialPromote(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, id, versionID string
	flags := credentialFlagSet("credential promote", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&id, "id", "", "Credential ID")
	flags.StringVar(&versionID, "version-id", "", "Credential version ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(id) == "" || strings.TrimSpace(versionID) == "" {
		return fmt.Errorf("--id and --version-id are required")
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	endpoint, err := credentialEndpoint(id, "promote")
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"version_id": strings.TrimSpace(versionID)})
	if err != nil {
		return err
	}
	if _, err := gatewayCredentialRequest(http.MethodPost, gateway, token, endpoint, nil, body); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "credential_id=%s version_id=%s promoted=true\n", strings.TrimSpace(id), strings.TrimSpace(versionID))
	return err
}

func runCredentialRevoke(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, id string
	flags := credentialFlagSet("credential revoke", stdout)
	flags.StringVar(&target, "target", "", "Configured gateway target")
	flags.StringVar(&id, "id", "", "Credential ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(id) == "" {
		return fmt.Errorf("--id is required")
	}
	gateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return err
	}
	endpoint, err := credentialEndpoint(id, "revoke")
	if err != nil {
		return err
	}
	if _, err := gatewayCredentialRequest(http.MethodPost, gateway, token, endpoint, nil, nil); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "credential_id=%s revoked=true\n", strings.TrimSpace(id))
	return err
}

func credentialFlagSet(name string, stdout io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stdout)
	return flags
}

func resolveCredentialGateway(target string, servers []Server, configErr error) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("--target is required")
	}
	if configErr != nil {
		return "", "", configErr
	}
	server := findServer(servers, target)
	if server == nil {
		return "", "", fmt.Errorf("target %q not found", target)
	}
	token, err := ResolveToken(*server)
	if err != nil {
		return "", "", err
	}
	return server.Gateway, token, nil
}

func resolveCredentialListTarget(
	target string,
	servers []Server,
	configErr error,
) (string, string, string, string, error) {
	target = strings.TrimSpace(target)
	resolvedGateway, token, err := resolveCredentialGateway(target, servers, configErr)
	if err != nil {
		return "", "", "", "", err
	}
	server := findServer(servers, target)
	if server == nil {
		return "", "", "", "", fmt.Errorf("target %q not found", target)
	}
	cluster := strings.TrimSpace(server.Cluster)
	instance := strings.TrimSpace(server.Instance)
	if cluster == "" || instance == "" {
		return "", "", "", "", fmt.Errorf("target %q has no gateway cluster/instance route", target)
	}
	return resolvedGateway, token, cluster, instance, nil
}

func readCredentialPayload(stdin io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(stdin, maxCredentialPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read credential payload from stdin: %w", err)
	}
	if len(payload) > maxCredentialPayloadBytes {
		return nil, fmt.Errorf("credential payload exceeds %d bytes", maxCredentialPayloadBytes)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("credential payload from stdin must be valid JSON")
	}
	return payload, nil
}

func parseOptionalCredentialList(raw, flagName string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%s must be a comma-separated non-empty list", flagName)
		}
		values = append(values, part)
	}
	return values, nil
}

func credentialEndpoint(id, operation string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("invalid credential id")
	}
	return "/v1/credentials/" + id + "/" + operation, nil
}

func gatewayCredentialRequest(method, gateway, token, endpoint string, query url.Values, body []byte) ([]byte, error) {
	requestURL, err := gatewayURLWithPath(gateway, endpoint, query)
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, requestURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token = strings.TrimSpace(token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 11 * time.Minute}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxCredentialPayloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(responseBody) > maxCredentialPayloadBytes {
		return nil, fmt.Errorf("credential gateway response exceeds %d bytes", maxCredentialPayloadBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, decodeGatewayHTTPError("credential gateway "+endpoint, response, responseBody)
	}
	return responseBody, nil
}
