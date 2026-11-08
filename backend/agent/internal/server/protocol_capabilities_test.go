package server

import (
	"strings"
	"testing"
)

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

func TestExecutionLifecycleCapabilityRequiresExactSchema(t *testing.T) {
	if !executionLifecycleCapabilityCompatible(executionLifecycleCapability()) {
		t.Fatal("current execution lifecycle capability must be accepted")
	}
	for name, invalid := range invalidExecutionLifecycleCapabilities() {
		if executionLifecycleCapabilityCompatible(invalid) {
			t.Fatalf("invalid lifecycle capability %s was accepted: %#v", name, invalid)
		}
	}
}

func invalidExecutionLifecycleCapabilities() map[string]interface{} {
	cases := map[string]interface{}{"missing": nil, "boolean": true, "string": executionLifecycleSchema}
	for field := range executionLifecycleCapability() {
		missing := executionLifecycleCapability()
		delete(missing, field)
		cases["missing-"+field] = missing
		wrongType := executionLifecycleCapability()
		wrongType[field] = 1
		cases["wrong-type-"+field] = wrongType
		disabled := executionLifecycleCapability()
		disabled[field] = false
		cases["disabled-"+field] = disabled
	}
	version := executionLifecycleCapability()
	version["schemaVersion"] = "unsupported"
	cases["version"] = version
	extra := executionLifecycleCapability()
	extra["extra"] = true
	cases["extra"] = extra
	return cases
}

func TestClientCapabilitiesExposeOnlyAgentAdvertisedProtocols(t *testing.T) {
	legacy := clientCapabilities(&GatewayAgent{})
	if protocols, ok := legacy["protocols"].(map[string]interface{}); ok && len(protocols) != 0 {
		t.Fatalf("legacy Agent exposed unsupported protocols: %#v", legacy)
	}

	current := clientCapabilities(&GatewayAgent{StructuredSSH: true, ExecutionLifecycle: true})
	protocols, ok := current["protocols"].(map[string]interface{})
	if !ok || !structuredSSHCapabilityCompatible(protocols[structuredSSHProtocol]) {
		t.Fatalf("current Agent did not expose structured SSH: %#v", current)
	}
	if !executionLifecycleCapabilityCompatible(protocols[executionLifecycleProtocol]) {
		t.Fatalf("current Agent did not expose background lifecycle: %#v", current)
	}
}

func TestClientCapabilitiesExposeOnlyVerifiedReleaseCommit(t *testing.T) {
	legacy := clientCapabilities(&GatewayAgent{ReleaseCommit: ""})
	if _, ok := legacy["releaseCommit"]; ok {
		t.Fatalf("legacy Agent exposed an empty release commit: %#v", legacy)
	}

	release := strings.Repeat("a", 40)
	current := clientCapabilities(&GatewayAgent{ReleaseCommit: release})
	if got := current["releaseCommit"]; got != release {
		t.Fatalf("current Agent release commit = %#v", got)
	}
	short := clientCapabilities(&GatewayAgent{ReleaseCommit: "cc4a56843"})
	if _, ok := short["releaseCommit"]; ok {
		t.Fatalf("short Agent release commit was accepted: %#v", short)
	}
}
