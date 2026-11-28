package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestVersionReportsBuildAndContractMetadata(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	command := exec.Command(binary, "version")
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("doops version: %v\n%s", err, output)
	}
	got := string(output)
	for _, expected := range []string{
		"release=dev",
		"commit=unknown",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("version output %q omitted %q", got, expected)
		}
	}
	contract := exec.Command(binary, "--contract")
	contractOutput, err := contract.CombinedOutput()
	if err != nil {
		t.Fatalf("doops --contract: %v\n%s", err, contractOutput)
	}
	if strings.TrimSpace(string(contractOutput)) != cliContractVersion {
		t.Fatalf("contract output %q, want %q", contractOutput, cliContractVersion)
	}
}
