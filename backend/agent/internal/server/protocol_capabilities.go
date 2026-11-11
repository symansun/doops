package server

const (
	structuredSSHProtocol         = "structuredSSH"
	structuredSSHSchema           = "doops.sh/structured-ssh/v1"
	executionLifecycleProtocol    = "executionLifecycle"
	executionLifecycleSchema      = "doops.sh/execution-lifecycle/v1"
	registryAuthfileProtocol      = "registryAuthfile"
	registryAuthfileSchema        = "doops.sh/registry-authfile/v1"
	cosSnapshotExecutableProtocol = "cosSnapshotExecutable"
	cosSnapshotExecutableSchema   = "doops.sh/cos-snapshot-executable/v1"
)

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
