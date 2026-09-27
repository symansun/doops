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
	capability, ok := value.(map[string]interface{})
	return ok &&
		len(capability) == 3 &&
		capability["schemaVersion"] == structuredSSHSchema &&
		capability["tool"] == "doops_ssh" &&
		capability["structuredArgv"] == true
}
