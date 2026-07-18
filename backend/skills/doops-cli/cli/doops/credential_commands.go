package main

import (
	"bytes"
	"encoding/json"
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

func runCredentialCommand(args []string, servers []Server, configErr error, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("credential subcommand is required: create, list, put, grant, grant-revoke, promote, revoke, bundle-create, bundle-get")
	}
	switch args[0] {
	case "create":
		return runCredentialCreate(args[1:], servers, configErr, stdout)
	case "list":
		return runCredentialList(args[1:], servers, configErr, stdout)
	case "put":
		return runCredentialPut(args[1:], servers, configErr, stdin, stdout)
	case "grant":
		return runCredentialGrant(args[1:], servers, configErr, stdout)
	case "grant-revoke":
		return runCredentialGrantRevoke(args[1:], servers, configErr, stdout)
	case "promote":
		return runCredentialPromote(args[1:], servers, configErr, stdout)
	case "revoke":
		return runCredentialRevoke(args[1:], servers, configErr, stdout)
	case "bundle-create":
		return runCredentialBundleCreate(args[1:], servers, configErr, stdin, stdout)
	case "bundle-get":
		return runCredentialBundleGet(args[1:], servers, configErr, stdout)
	default:
		return fmt.Errorf("unsupported credential subcommand %q", args[0])
	}
}

func runCredentialGrantRevoke(args []string, servers []Server, configErr error, stdout io.Writer) error {
	var target, id, grantID string
	flags := credentialFlagSet("credential grant-revoke")
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
	flags := credentialFlagSet("credential bundle-create")
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
	flags := credentialFlagSet("credential bundle-get")
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
	flags := credentialFlagSet("credential create")
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
	flags := credentialFlagSet("credential list")
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
	gateway, token, routeCluster, routeInstance, err := resolveCredentialListTarget(
		target, servers, configErr,
	)
	if err != nil {
		return err
	}
	query := url.Values{"view": []string{view}}
	if view == "target" {
		if routeCluster == "" || routeInstance == "" {
			return fmt.Errorf("target credential view requires --target or --gateway with --cluster and --instance")
		}
		query.Set("cluster", routeCluster)
		query.Set("instance", routeInstance)
	}
	raw, err := gatewayCredentialRequest(http.MethodGet, gateway, token, "/v1/credentials", query, nil)
	if err != nil {
		return err
	}
	var response struct {
		Credentials []credentialResource `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode credential list response: %w", err)
	}
	for _, credential := range response.Credentials {
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

func runCredentialPut(args []string, servers []Server, configErr error, stdin io.Reader, stdout io.Writer) error {
	var target, id string
	flags := credentialFlagSet("credential put")
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
	flags := credentialFlagSet("credential grant")
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
	flags := credentialFlagSet("credential promote")
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
	flags := credentialFlagSet("credential revoke")
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

func credentialFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
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
		return nil, fmt.Errorf("credential gateway request failed: HTTP %s", response.Status)
	}
	return responseBody, nil
}
