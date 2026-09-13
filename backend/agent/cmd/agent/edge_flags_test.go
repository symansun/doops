package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentBinaryHelpDocumentsEdgeOffByDefault(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "doops-agent")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build doops-agent: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "-h").CombinedOutput()
	if err != nil {
		t.Fatalf("doops-agent -h: %v\n%s", err, out)
	}
	help := string(out)
	if !strings.Contains(help, "-edge") {
		t.Fatalf("agent help must document -edge\n%s", help)
	}
	if !strings.Contains(help, "Enable Edge public reverse tunnel") {
		t.Fatalf("agent help must describe -edge\n%s", help)
	}
	if !strings.Contains(help, "-edge-enroll-token") || !strings.Contains(help, "-edge-backend-allow") {
		t.Fatalf("agent help must document enrollment and backend allow-list flags\n%s", help)
	}
}
