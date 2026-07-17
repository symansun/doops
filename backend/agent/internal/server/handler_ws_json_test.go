package server

import "testing"

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
