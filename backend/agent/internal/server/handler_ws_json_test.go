package server

import (
	"strings"
	"testing"
)

func TestValidateDoagentApplyInstructionAcceptsV3CICD(t *testing.T) {
	instruction := `{
		"task":"execute-doops-cicd",
		"skill":"$doops-cicd",
		"executionMode":"apply",
		"manifestPath":"deploy/environments.yaml",
		"workspaceCommit":"` + strings.Repeat("a", 40) + `",
		"inputs":{"environment":"oilan","service":"lab-api"}
	}`
	if err := validateDoagentApplyInstruction(instruction); err != nil {
		t.Fatalf("validate v3 apply: %v", err)
	}
}

func TestValidateDoagentApplyInstructionRejectsImplicitApply(t *testing.T) {
	if err := validateDoagentApplyInstruction(`{"task":"execute-doops-cicd"}`); err == nil {
		t.Fatal("implicit apply was accepted")
	}
}

func TestValidateDoagentCICDCapabilitiesRequiresExactV2Contract(t *testing.T) {
	valid := map[string]interface{}{
		"result": map[string]interface{}{
			"agentCapabilities": map[string]interface{}{
				"trustedReconciliationContext": map[string]interface{}{
					"schemaVersion":            "doops.reconciliation-context/v2",
					"callContextSchemaVersion": "doops.reconciliation-call-context/v1",
					"mcpMetaKey":               "doops.sh/reconciliation-context",
					"credentialRunDigest":      true,
					"sourceWorkspaceCommit":    true,
				},
			},
		},
	}
	if err := validateDoagentCICDCapabilities(valid); err != nil {
		t.Fatalf("valid v2 capability rejected: %v", err)
	}
	valid["result"].(map[string]interface{})["agentCapabilities"].(map[string]interface{})["trustedReconciliationContext"].(map[string]interface{})["sourceWorkspaceCommit"] = false
	if err := validateDoagentCICDCapabilities(valid); err == nil {
		t.Fatal("incomplete v2 capability was accepted")
	}
}
