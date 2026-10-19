package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/user/doops/agent/api"
)

var (
	k8sNamePattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	k8sKindPattern      = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)
	k8sContainerPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)
	k8sTimeoutPattern   = regexp.MustCompile(`^[1-9][0-9]*(s|m|h)$`)
	// Label selectors: key[=|==|!=]value, key in (a,b), !key; comma-separated.
	k8sSelectorPattern = regexp.MustCompile(`^[A-Za-z0-9_./!=(), -]{1,512}$`)
	// jsonpath output is kubectl's own read-only template language.
	k8sJSONPathPattern = regexp.MustCompile(`^(jsonpath|jsonpath-as-json|custom-columns)=[^\x00\r\n]+$`)
	k8sReadableKinds   = map[string]bool{
		"pod": true, "pods": true,
		"deployment": true, "deployments": true, "deploy": true,
		"statefulset": true, "statefulsets": true, "sts": true,
		"daemonset": true, "daemonsets": true, "ds": true,
		"replicaset": true, "replicasets": true, "rs": true,
		"service": true, "services": true, "svc": true,
		"endpoint": true, "endpoints": true,
		"endpointslice": true, "endpointslices": true,
		"ingress": true, "ingresses": true, "ing": true,
		"job": true, "jobs": true,
		"cronjob": true, "cronjobs": true,
		"node": true, "nodes": true,
		"namespace": true, "namespaces": true, "ns": true,
		"event": true, "events": true,
		"persistentvolumeclaim": true, "persistentvolumeclaims": true, "pvc": true,
		"persistentvolume": true, "persistentvolumes": true, "pv": true,
		"configmap": true, "configmaps": true, "cm": true,
		"networkpolicy": true, "networkpolicies": true, "netpol": true,
		"horizontalpodautoscaler": true, "horizontalpodautoscalers": true, "hpa": true,
		"poddisruptionbudget": true, "poddisruptionbudgets": true, "pdb": true,
		"role": true, "roles": true, "rolebinding": true, "rolebindings": true,
		"clusterrole": true, "clusterroles": true, "clusterrolebinding": true, "clusterrolebindings": true,
		"storageclass": true, "storageclasses": true, "sc": true,
		"ingressclass": true, "ingressclasses": true,
		"priorityclass": true, "priorityclasses": true,
		"limitrange": true, "limitranges": true, "resourcequota": true, "resourcequotas": true, "quota": true,
		"customresourcedefinition": true, "customresourcedefinitions": true, "crd": true, "crds": true,
		"lease": true, "leases": true,
	}
	// Secrets and ServiceAccount tokens carry credentials and stay out of the read tool.
	k8sCredentialKinds = map[string]bool{
		"secret": true, "secrets": true, "serviceaccount": true, "serviceaccounts": true, "sa": true,
		"token": true, "tokenrequest": true, "tokenrequests": true,
	}
	k8sLoggableKinds = map[string]bool{
		"pod": true, "pods": true,
		"deployment": true, "deployments": true, "deploy": true,
		"statefulset": true, "statefulsets": true, "sts": true,
		"daemonset": true, "daemonsets": true, "ds": true,
		"job": true, "jobs": true,
	}
	k8sRolloutKinds = map[string]bool{
		"deployment": true, "deployments": true, "deploy": true,
		"statefulset": true, "statefulsets": true, "sts": true,
		"daemonset": true, "daemonsets": true, "ds": true,
	}
	k8sScalableKinds = map[string]bool{
		"deployment": true, "deployments": true, "deploy": true,
		"statefulset": true, "statefulsets": true, "sts": true,
		"replicaset": true, "replicasets": true, "rs": true,
	}
)

type k8sCommand struct {
	args          []string
	helm          bool // run helm instead of kubectl
	valueKeysOnly bool // print only the key paths of a values document
}

func handleK8STool(ctx context.Context, raw json.RawMessage, push notificationSender) (string, error) {
	var params api.K8SParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return "", fmt.Errorf("invalid k8s request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid k8s request")
	}

	commands, err := buildK8SCommands(params)
	if err != nil {
		return "", err
	}
	for _, command := range commands {
		if command.helm {
			if err := runHelmReadCommand(ctx, command, push); err != nil {
				return "", err
			}
			continue
		}
		if err := runK8SCommand(ctx, command.args, push, !gatewayK8SReadOnly(params.Operation)); err != nil {
			return "", err
		}
	}
	return "Kubernetes operation complete.", nil
}

func buildK8SCommands(params api.K8SParams) ([]k8sCommand, error) {
	params.Operation = strings.ToLower(strings.TrimSpace(params.Operation))
	params.Kind = strings.ToLower(strings.TrimSpace(params.Kind))
	params.Name = strings.TrimSpace(params.Name)
	params.Resource = normalizeK8SResource(params.Resource)
	params.Namespace = strings.TrimSpace(params.Namespace)
	params.Container = strings.TrimSpace(params.Container)
	params.Output = strings.TrimSpace(params.Output)
	params.Timeout = strings.TrimSpace(params.Timeout)
	params.Selector = strings.TrimSpace(params.Selector)
	params.Since = strings.TrimSpace(params.Since)

	if params.Operation == "" {
		return nil, fmt.Errorf("k8s operation is required")
	}
	if err := validateSession(strings.TrimSpace(params.SessionID)); err != nil {
		return nil, err
	}
	if params.Namespace != "" && !k8sNamePattern.MatchString(params.Namespace) {
		return nil, fmt.Errorf("invalid kubernetes namespace")
	}
	if params.Kind != "" && !k8sKindPattern.MatchString(params.Kind) {
		return nil, fmt.Errorf("invalid kubernetes kind")
	}
	if params.Name != "" && !k8sNamePattern.MatchString(params.Name) {
		return nil, fmt.Errorf("invalid kubernetes name")
	}
	if params.Container != "" && !k8sContainerPattern.MatchString(params.Container) {
		return nil, fmt.Errorf("invalid kubernetes container")
	}
	if params.Resource != "" &&
		(params.Operation == "logs" || params.Operation == "events" ||
			params.Operation == "rollout-status") &&
		!validK8SResource(params.Resource) {
		if params.Operation == "events" {
			return nil, fmt.Errorf("invalid kubernetes resource for events; omit resource for namespace-wide events or use kind/name")
		}
		return nil, fmt.Errorf("invalid kubernetes resource")
	}
	if params.Timeout != "" && !k8sTimeoutPattern.MatchString(params.Timeout) {
		return nil, fmt.Errorf("invalid kubernetes timeout")
	}
	if params.Selector != "" && !k8sSelectorPattern.MatchString(params.Selector) {
		return nil, fmt.Errorf("invalid kubernetes label selector")
	}
	if params.Since != "" && !k8sTimeoutPattern.MatchString(params.Since) {
		return nil, fmt.Errorf("invalid logs since duration; use e.g. 10m or 2h")
	}
	if params.AllNamespaces && params.Namespace != "" {
		return nil, fmt.Errorf("all_namespaces and namespace are mutually exclusive")
	}
	for _, kind := range []string{params.Kind, k8sResourceKind(params.Resource)} {
		if k8sCredentialKinds[kind] {
			return nil, fmt.Errorf("kubernetes kind %q is not available through the read tool: it carries credentials", kind)
		}
	}
	if params.Tail < 0 || params.Tail > 10000 {
		return nil, fmt.Errorf("tail must be between 0 and 10000")
	}
	if params.Replicas != nil && (*params.Replicas < 0 || *params.Replicas > 10000) {
		return nil, fmt.Errorf("replicas must be between 0 and 10000")
	}

	namespace := func(args []string) []string {
		if params.Namespace != "" {
			return append(args, "--namespace", params.Namespace)
		}
		if params.AllNamespaces {
			return append(args, "--all-namespaces")
		}
		return args
	}
	selector := func(args []string) []string {
		if params.Selector != "" {
			return append(args, "--selector", params.Selector)
		}
		return args
	}
	timeout := func(args []string) []string {
		if params.Timeout != "" {
			return append(args, "--timeout", params.Timeout)
		}
		return args
	}
	requireMutation := func() error {
		if !params.Confirm {
			return fmt.Errorf("confirm=true is required for k8s mutation %q", params.Operation)
		}
		return nil
	}

	switch params.Operation {
	case "get":
		if params.Kind == "" {
			return nil, fmt.Errorf("kind is required for get")
		}
		if !k8sReadableKinds[params.Kind] {
			return nil, fmt.Errorf("kubernetes kind %q is not available through the read tool", params.Kind)
		}
		args := namespace([]string{"get", params.Kind})
		if params.Name != "" {
			args = append(args, params.Name)
		} else {
			args = selector(args)
		}
		if params.Output != "" {
			if !validK8SOutput(params.Output) {
				return nil, fmt.Errorf("unsupported kubernetes output; use json, yaml, wide, name, jsonpath=..., or custom-columns=...")
			}
			args = append(args, "--output", params.Output)
		}
		return []k8sCommand{{args: args}}, nil
	case "describe":
		if params.Kind == "" || params.Name == "" {
			return nil, fmt.Errorf("kind and name are required for describe")
		}
		if !k8sReadableKinds[params.Kind] {
			return nil, fmt.Errorf("kubernetes kind %q is not available through the read tool", params.Kind)
		}
		return []k8sCommand{{args: namespace([]string{"describe", params.Kind, params.Name})}}, nil
	case "logs":
		if params.Resource == "" {
			return nil, fmt.Errorf("resource is required for logs")
		}
		if !k8sLoggableKinds[k8sResourceKind(params.Resource)] {
			return nil, fmt.Errorf("kubernetes resource %q is not available for logs", params.Resource)
		}
		args := namespace([]string{"logs", params.Resource})
		if params.Container != "" {
			args = append(args, "--container", params.Container)
		}
		if params.Tail > 0 {
			args = append(args, "--tail", strconv.Itoa(params.Tail))
		}
		if params.Previous {
			args = append(args, "--previous")
		}
		if params.Since != "" {
			args = append(args, "--since", params.Since)
		}
		return []k8sCommand{{args: args}}, nil
	case "top":
		if params.Kind != "pods" && params.Kind != "nodes" {
			return nil, fmt.Errorf("top kind must be pods or nodes")
		}
		return []k8sCommand{{args: selector(namespace([]string{"top", params.Kind}))}}, nil
	case "events":
		args := namespace([]string{"events"})
		if params.Resource != "" {
			if !k8sReadableKinds[k8sResourceKind(params.Resource)] {
				return nil, fmt.Errorf("kubernetes resource %q is not available for events; use kind/name or omit resource", params.Resource)
			}
			args = append(args, "--for", params.Resource)
		}
		return []k8sCommand{{args: args}}, nil
	case "helm-list":
		return []k8sCommand{{helm: true, args: namespace([]string{"list", "--output", "json"})}}, nil
	case "helm-history", "helm-status", "helm-values":
		if params.Name == "" || !k8sNamePattern.MatchString(params.Name) {
			return nil, fmt.Errorf("name (the Helm release) is required for %s", params.Operation)
		}
		if params.Namespace == "" {
			return nil, fmt.Errorf("namespace is required for %s", params.Operation)
		}
		switch params.Operation {
		case "helm-history":
			return []k8sCommand{{helm: true, args: []string{"history", params.Name, "--namespace", params.Namespace, "--max", "20", "--output", "json"}}}, nil
		case "helm-status":
			return []k8sCommand{{helm: true, args: []string{"status", params.Name, "--namespace", params.Namespace, "--output", "json"}}}, nil
		default:
			// Values can hold plaintext secrets, so only their key paths are returned.
			return []k8sCommand{{helm: true, valueKeysOnly: true, args: []string{"get", "values", params.Name, "--namespace", params.Namespace, "--all", "--output", "json"}}}, nil
		}
	case "rollout-status":
		if params.Resource == "" {
			return nil, fmt.Errorf("resource is required for rollout status")
		}
		if !k8sRolloutKinds[k8sResourceKind(params.Resource)] {
			return nil, fmt.Errorf("kubernetes resource %q does not support rollout status", params.Resource)
		}
		return []k8sCommand{{args: timeout(namespace([]string{"rollout", "status", params.Resource}))}}, nil
	case "rollout-restart":
		if err := requireMutation(); err != nil {
			return nil, err
		}
		if params.Resource == "" {
			return nil, fmt.Errorf("resource is required for rollout restart")
		}
		if !k8sRolloutKinds[k8sResourceKind(params.Resource)] {
			return nil, fmt.Errorf("kubernetes resource %q does not support rollout restart", params.Resource)
		}
		return []k8sCommand{{args: namespace([]string{"rollout", "restart", params.Resource})}}, nil
	case "scale":
		if err := requireMutation(); err != nil {
			return nil, err
		}
		if params.Resource == "" {
			return nil, fmt.Errorf("resource is required for scale")
		}
		if !k8sScalableKinds[k8sResourceKind(params.Resource)] {
			return nil, fmt.Errorf("kubernetes resource %q does not support scale", params.Resource)
		}
		if params.Replicas == nil {
			return nil, fmt.Errorf("replicas is required for scale")
		}
		return []k8sCommand{{args: namespace([]string{"scale", params.Resource, "--replicas", strconv.Itoa(*params.Replicas)})}}, nil
	case "cordon", "uncordon":
		if err := requireMutation(); err != nil {
			return nil, err
		}
		if params.Name == "" {
			return nil, fmt.Errorf("node name is required for %s", params.Operation)
		}
		return []k8sCommand{{args: []string{params.Operation, params.Name}}}, nil
	case "delete-pod":
		if err := requireMutation(); err != nil {
			return nil, err
		}
		if params.Name == "" {
			return nil, fmt.Errorf("pod name is required for delete")
		}
		return []k8sCommand{{args: namespace([]string{"delete", "pod", params.Name})}}, nil
	default:
		return nil, fmt.Errorf("unsupported k8s operation %q", params.Operation)
	}
}

func validK8SResource(resource string) bool {
	parts := strings.Split(resource, "/")
	return len(parts) == 2 &&
		k8sKindPattern.MatchString(parts[0]) &&
		k8sNamePattern.MatchString(parts[1])
}

func normalizeK8SResource(resource string) string {
	parts := strings.Split(strings.TrimSpace(resource), "/")
	if len(parts) != 2 {
		return strings.TrimSpace(resource)
	}
	return strings.ToLower(strings.TrimSpace(parts[0])) + "/" + strings.TrimSpace(parts[1])
}

func k8sResourceKind(resource string) string {
	kind, _, _ := strings.Cut(resource, "/")
	return strings.ToLower(kind)
}

func validK8SOutput(output string) bool {
	switch output {
	case "json", "yaml", "wide", "name":
		return true
	default:
		return len(output) <= 1024 && k8sJSONPathPattern.MatchString(output)
	}
}

func runK8SCommand(ctx context.Context, args []string, push notificationSender, mutation bool) error {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("start kubectl: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("start kubectl: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start kubectl: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamReader(stdout, push)
	}()
	go func() {
		defer wg.Done()
		streamReader(stderr, push)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		if mutation {
			// Once kubectl starts, a failed write cannot prove that no change landed.
			return errExecutionOutcomeUnknown
		}
		return fmt.Errorf("kubectl %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}

// runHelmReadCommand runs a read-only helm command. Values output is reduced to
// its key paths so plaintext secrets in release values never reach the caller.
func runHelmReadCommand(ctx context.Context, command k8sCommand, push notificationSender) error {
	output, err := exec.CommandContext(ctx, "helm", command.args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm %s failed: %s", command.args[0], sanitizeSecretText(strings.TrimSpace(string(output))))
	}
	if !command.valueKeysOnly {
		push(rawNotification(string(output)))
		return nil
	}
	var values interface{}
	if err := json.Unmarshal(output, &values); err != nil {
		return fmt.Errorf("decode helm values: %w", err)
	}
	keys := helmValueKeyPaths("", values)
	sort.Strings(keys)
	push(rawNotification("value keys (values are not shown):\n" + strings.Join(keys, "\n") + "\n"))
	return nil
}

func helmValueKeyPaths(prefix string, value interface{}) []string {
	object, ok := value.(map[string]interface{})
	if !ok || len(object) == 0 {
		if prefix == "" {
			return nil
		}
		return []string{prefix}
	}
	var keys []string
	for key, child := range object {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		keys = append(keys, helmValueKeyPaths(path, child)...)
	}
	return keys
}
