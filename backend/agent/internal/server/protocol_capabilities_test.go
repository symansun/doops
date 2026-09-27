package server

import "testing"

func TestStructuredSSHCapabilityRequiresExactSchema(t *testing.T) {
	if !structuredSSHCapabilityCompatible(structuredSSHCapability()) {
		t.Fatal("current structured SSH capability must be accepted")
	}
	for _, legacy := range []interface{}{
		nil,
		structuredSSHSchema,
		map[string]interface{}{
			"schemaVersion":  structuredSSHSchema,
			"tool":           "doops_shell",
			"structuredArgv": true,
		},
		map[string]interface{}{
			"schemaVersion": structuredSSHSchema,
			"tool":          "doops_ssh",
		},
	} {
		if structuredSSHCapabilityCompatible(legacy) {
			t.Fatalf("legacy or incomplete capability was accepted: %#v", legacy)
		}
	}
}

func TestClientCapabilitiesExposeOnlyAgentAdvertisedProtocols(t *testing.T) {
	legacy := clientCapabilities(&GatewayAgent{})
	if protocols, ok := legacy["protocols"].(map[string]interface{}); ok && len(protocols) != 0 {
		t.Fatalf("legacy Agent exposed unsupported protocols: %#v", legacy)
	}

	current := clientCapabilities(&GatewayAgent{StructuredSSH: true})
	protocols, ok := current["protocols"].(map[string]interface{})
	if !ok || !structuredSSHCapabilityCompatible(protocols[structuredSSHProtocol]) {
		t.Fatalf("current Agent did not expose structured SSH: %#v", current)
	}
}
