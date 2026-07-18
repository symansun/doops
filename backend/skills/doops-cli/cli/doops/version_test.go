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
		"version=dev",
		"commit=unknown",
		"contract=" + cliContractVersion,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("version output %q omitted %q", got, expected)
		}
	}
}
