package server

const (
	structuredSSHProtocol = "structuredSSH"
	structuredSSHSchema   = "doops.sh/structured-ssh/v1"
)

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
