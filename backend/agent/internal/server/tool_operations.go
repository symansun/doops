package server

import (
	"slices"
	"strings"
)

var (
	k8sReadOperations = []string{
		"get", "describe", "logs", "top", "events", "rollout-status", "configmap-keys",
		"helm-list", "helm-history", "helm-status", "helm-values",
	}
)

func gatewayK8SReadOnly(operation string) bool {
	return slices.Contains(k8sReadOperations, strings.ToLower(strings.TrimSpace(operation)))
}

var (
	webReadOperations     = []string{"render", "check", "config-get", "domain-list", "site-list"}
	webMutationOperations = []string{
		"deploy", "rollback", "config-set", "snippet-set", "install-nginx", "domain-set",
	}
)

func containsWebString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// mcpAllowedOperationsSuffix tells the model which operations the tool accepts
// so an invalid-operation retry self-corrects instead of guessing again.
func mcpAllowedOperationsSuffix(operations []string) string {
	return "; allowed operations: " + strings.Join(operations, ", ")
}
