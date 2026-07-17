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
