package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/api"
)

func TestValidateDoagentApplyInstructionAcceptsV3CICD(t *testing.T) {
	if err := validateDoagentApplyInstruction(validDoagentApplyPrompt(t)); err != nil {
		t.Fatalf("validate v3 apply: %v", err)
	}
}

func TestValidateDoagentApplyInstructionRejectsImplicitApply(t *testing.T) {
	prompt := validDoagentApplyPrompt(t)
	prompt.Instruction = `{"task":"execute-doops-cicd"}`
	if err := validateDoagentApplyInstruction(prompt); err == nil {
		t.Fatal("implicit apply was accepted")
	}
}

func validDoagentApplyPrompt(t *testing.T) api.AgentPromptParams {
	t.Helper()
	commit := strings.Repeat("a", 40)
	session := "doops-oilan-release"
	instruction, err := json.Marshal(map[string]interface{}{
		"task":            "execute-doops-cicd",
		"skill":           "$doops-cicd",
		"executionMode":   "apply",
		"manifestPath":    "deploy/environments.yaml",
		"workspaceCommit": commit,
		"source": map[string]string{
			"repository": "https://example.test/doops.git",
			"branch":     "main",
		},
		"inputs": map[string]string{
			"environment": "oilan",
			"service":     "doops-agent",
		},
		"credentialRun": CredentialRun{
			APIVersion:     "doops.sh/v2",
			Kind:           "CredentialRun",
			ID:             "credrun_test",
			Mode:           "apply",
			Cluster:        "doops-edu",
			Instance:       "release-runner",
			Target:         "gw-edu-release-runner",
			Session:        session,
			SourceRevision: commit,
			Template:       "doops-oilan-doops-agent",
			Project:        "doops",
			Environment:    "oilan",
			CreatedAt:      time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("encode apply prompt: %v", err)
	}
	return api.AgentPromptParams{
		SessionID:       session,
		Instruction:     string(instruction),
		Operation:       "apply",
		ResponseFormat:  "json",
		WorkspaceCommit: commit,
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

func TestDoagentToolTraceCollectorRejectsDuplicateCICDReport(t *testing.T) {
	collector := newDoagentToolTraceCollector(true)
	update := map[string]interface{}{
		"sessionUpdate": "agent_report",
		"report": map[string]interface{}{
			"planDigest": "sha256:" + strings.Repeat("a", 64),
			"status":     "Converged",
			"evidence":   []interface{}{},
			"violations": []interface{}{},
		},
	}
	if err := collector.collect(update); err != nil {
		t.Fatalf("collect first report: %v", err)
	}
	if err := collector.collect(update); err == nil || !strings.Contains(err.Error(), "multiple agent reports") {
		t.Fatalf("duplicate report error = %v", err)
	}
}

func TestDoagentToolTraceCollectorIgnoresReportsForGenericJSONPrompt(t *testing.T) {
	collector := newDoagentToolTraceCollector(false)
	for _, status := range []string{"Converged", "Failed"} {
		if err := collector.collect(map[string]interface{}{
			"sessionUpdate": "agent_report",
			"report":        map[string]interface{}{"status": status},
		}); err != nil {
			t.Fatalf("generic prompt report must be ignored: %v", err)
		}
	}
	if err := collector.collect(map[string]interface{}{
		"sessionUpdate": "agent_message",
		"content":       map[string]interface{}{"type": "text", "text": `{"status":"observed"}`},
	}); err != nil {
		t.Fatalf("collect generic JSON result: %v", err)
	}
	text, result, err := collector.structuredResult(false)
	if err != nil || text != `{"status":"observed"}` || result["status"] != "observed" {
		t.Fatalf("generic structured result text=%q result=%#v err=%v", text, result, err)
	}
}
