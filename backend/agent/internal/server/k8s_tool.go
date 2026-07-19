package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
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
	k8sReadableKinds    = map[string]bool{
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
	args []string
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
		if err := runK8SCommand(ctx, command.args, push); err != nil {
			return "", err
		}
	}
	return "Kubernetes operation complete.", nil
}

func buildK8SCommands(params api.K8SParams) ([]k8sCommand, error) {
	params.Operation = strings.TrimSpace(params.Operation)
	params.Kind = strings.TrimSpace(params.Kind)
	params.Name = strings.TrimSpace(params.Name)
	params.Resource = strings.TrimSpace(params.Resource)
	params.Namespace = strings.TrimSpace(params.Namespace)
	params.Container = strings.TrimSpace(params.Container)
	params.Output = strings.TrimSpace(params.Output)
	params.Timeout = strings.TrimSpace(params.Timeout)

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
		return nil, fmt.Errorf("invalid kubernetes resource")
	}
	if params.Timeout != "" && !k8sTimeoutPattern.MatchString(params.Timeout) {
		return nil, fmt.Errorf("invalid kubernetes timeout")
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
		}
		if params.Output != "" {
			if !validK8SOutput(params.Output) {
				return nil, fmt.Errorf("unsupported kubernetes output")
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
		return []k8sCommand{{args: args}}, nil
	case "top":
		if params.Kind != "pods" && params.Kind != "nodes" {
			return nil, fmt.Errorf("top kind must be pods or nodes")
		}
		return []k8sCommand{{args: namespace([]string{"top", params.Kind})}}, nil
	case "events":
		args := namespace([]string{"events"})
		if params.Resource != "" {
			if !k8sReadableKinds[k8sResourceKind(params.Resource)] {
				return nil, fmt.Errorf("kubernetes resource %q is not available for events", params.Resource)
			}
			args = append(args, "--for", params.Resource)
		}
		return []k8sCommand{{args: args}}, nil
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

func k8sResourceKind(resource string) string {
	kind, _, _ := strings.Cut(resource, "/")
	return kind
}

func validK8SOutput(output string) bool {
	switch output {
	case "json", "yaml", "wide", "name":
		return true
	default:
		return false
	}
}

func runK8SCommand(ctx context.Context, args []string, push notificationSender) error {
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
		return fmt.Errorf("kubectl %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}
