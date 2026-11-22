package server

import (
	"github.com/user/doops/agent/pkg/release"
	"time"
)

const (
	structuredSSHProtocol         = "structuredSSH"
	structuredSSHSchema           = "doops.sh/structured-ssh/v1"
	executionLifecycleProtocol    = "executionLifecycle"
	executionLifecycleSchema      = "doops.sh/execution-lifecycle/v1"
	registryAuthfileProtocol      = "registryAuthfile"
	registryAuthfileSchema        = "doops.sh/registry-authfile/v1"
	cosSnapshotExecutableProtocol = "cosSnapshotExecutable"
	cosSnapshotExecutableSchema   = "doops.sh/cos-snapshot-executable/v1"
	toolDeadlineProtocol          = "toolDeadline"
	toolDeadlineSchema            = "doops.sh/tool-deadline/v1"
)

// toolDeadlineCapability lets the Gateway tell the Agent how long it will wait
// for a tools/call. The Agent then replies with a definite timeout instead of
// being abandoned, or reporting a cancellation, when its own deadline fires. An
// Agent from before this work ignores the extra field, so the Gateway sends
// timeout_ms only after the Agent declares this capability.
func toolDeadlineCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion":  toolDeadlineSchema,
		"gatewayTimeout": true,
		"timeoutReply":   true,
	}
}

func toolDeadlineCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, toolDeadlineCapability())
}

func cosSnapshotExecutableCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion": cosSnapshotExecutableSchema,
		"executable":    true,
	}
}

func cosSnapshotExecutableCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, cosSnapshotExecutableCapability())
}

// registryAuthfileCapability says the Agent writes a registry credential to the
// authfile its OCI tools read, and accepts the authfile-only materialization
// the Gateway sends on reconnect. An Agent from before that work decodes
// materialization arguments strictly and rejects the unknown field, so the
// Gateway checks this before delivering.
func registryAuthfileCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion": registryAuthfileSchema,
		"authfileOnly":  true,
	}
}

func registryAuthfileCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, registryAuthfileCapability())
}

func structuredSSHCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion":  structuredSSHSchema,
		"tool":           "doops_ssh",
		"structuredArgv": true,
	}
}

func structuredSSHCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, structuredSSHCapability())
}

func executionLifecycleCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion":      executionLifecycleSchema,
		"terminalResponse":   true,
		"backgroundLifetime": true,
		"promptCancellation": true,
	}
}

func executionLifecycleCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, executionLifecycleCapability())
}

func capabilityMatches(value interface{}, expected map[string]interface{}) bool {
	capability, ok := value.(map[string]interface{})
	if !ok || len(capability) != len(expected) {
		return false
	}
	for key, want := range expected {
		if capability[key] != want {
			return false
		}
	}
	return true
}

const (
	credentialPlanProtocolV2    = "doops.sh/credential-plan/v2"
	credentialPlanDeclarationV3 = "doops.sh/v3"
)

func credentialPlanCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion":            credentialPlanProtocolV2,
		"declarationSchemaVersion": credentialPlanDeclarationV3,
		"sourceRevision":           true,
		"targetBinding":            true,
		"sessionBinding":           true,
		"buildDefinition":          true,
	}
}

func credentialPlanCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, credentialPlanCapability())
}

func clientCapabilities(agent *GatewayAgent) map[string]interface{} {
	return clientCapabilitiesFor(agent, defaultDetachedTaskTimeout)
}

func clientCapabilitiesFor(agent *GatewayAgent, detachedTimeout time.Duration) map[string]interface{} {
	protocols := map[string]interface{}{}
	if agent != nil && agent.StructuredSSH {
		protocols[structuredSSHProtocol] = structuredSSHCapability()
	}
	if agent != nil && agent.ExecutionLifecycle {
		protocols[executionLifecycleProtocol] = executionLifecycleCapability()
	}
	if agent != nil && agent.ToolDeadline {
		protocols[toolDeadlineProtocol] = toolDeadlineCapability()
	}
	if agent != nil && agent.UpgradeAudit {
		protocols[upgradeAuditProtocol] = upgradeAuditCapability()
	}
	capabilities := map[string]interface{}{
		"tools": map[string]interface{}{},
		// Gateway-owned protocols apply even when the selected Agent is offline.
		"gatewayProtocols": map[string]interface{}{
			cosSnapshotExecutableProtocol: cosSnapshotExecutableCapability(),
			detachedBackgroundProtocol:    detachedBackgroundCapability(detachedTimeout),
		},
	}
	if agent != nil && release.ValidCommit(agent.ReleaseCommit) {
		capabilities["releaseCommit"] = agent.ReleaseCommit
	}
	if len(protocols) != 0 {
		capabilities["protocols"] = protocols
	}
	return capabilities
}
