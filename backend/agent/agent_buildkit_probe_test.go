package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildkitProbeHarness extracts probe_buildkit and start_buildkit from the
// real entrypoint, rewrites their fixed paths under a temporary directory, and
// returns the script plus the directory holding the fake buildctl call log.
func buildkitProbeHarness(t *testing.T, buildctlExit int) (string, string) {
	t.Helper()
	data, err := os.ReadFile("sandbox-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	start, end := -1, -1
	for index, line := range lines {
		switch {
		case line == "probe_buildkit() {" && start < 0:
			start = index
		case start >= 0 && line == "start_buildkit() {" && end < 0:
			end = index
		}
	}
	for ; end >= 0 && end < len(lines) && lines[end] != "}"; end++ {
	}
	if start < 0 || end >= len(lines) {
		t.Fatal("sandbox-entrypoint.sh lost probe_buildkit/start_buildkit")
	}
	root := t.TempDir()
	body := strings.Join(lines[start:end+1], "\n")
	for _, name := range []string{"/run/buildkit", "/var/log/buildkitd.log", "/opt/buildkit-probe"} {
		body = strings.ReplaceAll(body, name, filepath.Join(root, filepath.Base(name)))
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"buildkitd": "#!/bin/sh\nexit 0\n",
		"buildctl":  "#!/bin/sh\necho probe >> " + filepath.Join(root, "buildctl.calls") + "\nexit " + itoa(buildctlExit) + "\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// seq is pinned to one iteration so a failing probe reaches its message
	// immediately; sleep never runs.
	harness := "set -euo pipefail\nPIDS_TO_CLEANUP=()\nseq() { echo 1; }\nsleep() { :; }\n" +
		body + "\nstart_buildkit\n"
	path := filepath.Join(root, "harness.sh")
	if err := os.WriteFile(path, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, bin
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	return "1"
}

func runBuildkitHarness(t *testing.T, path, bin string) (string, string) {
	t.Helper()
	cmd := exec.Command("bash", path)
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start_buildkit failed: %v\n%s", err, stderr.String())
	}
	return string(out), stderr.String()
}

func buildctlCalls(t *testing.T, bin string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "buildctl.calls"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

// TestSandboxEntrypointReportsUnusableBuildkit proves a buildkitd that starts
// but cannot serve a build makes the Agent log the reason and keep running,
// instead of every image build failing later with a raw buildctl error.
func TestSandboxEntrypointReportsUnusableBuildkit(t *testing.T) {
	path, bin := buildkitProbeHarness(t, 1)
	stdout, stderr := runBuildkitHarness(t, path, bin)
	if !strings.Contains(stderr, "image build unavailable: buildkitd requires a privileged container") {
		t.Fatalf("stderr must name the privilege requirement: %q", stderr)
	}
	if strings.Contains(stdout, "buildkitd started") {
		t.Fatalf("an unusable buildkitd must not be reported as started: %q", stdout)
	}
	if buildctlCalls(t, bin) == 0 {
		t.Fatal("start_buildkit did not probe the daemon")
	}

	path, bin = buildkitProbeHarness(t, 0)
	stdout, stderr = runBuildkitHarness(t, path, bin)
	if !strings.Contains(stdout, "buildkitd started") || strings.Contains(stderr, "image build unavailable") {
		t.Fatalf("healthy buildkitd = %q / %q", stdout, stderr)
	}
	if buildctlCalls(t, bin) != 1 {
		t.Fatalf("healthy daemon was probed %d times, want 1", buildctlCalls(t, bin))
	}
}

func TestSandboxEntrypointPrivilegeGuidanceFollowsTheChart(t *testing.T) {
	chart, err := os.ReadFile(filepath.Join("..", "deploy", "helm", "doops-agent", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(chart), "privileged: true") {
		t.Fatal("chart default must stay privileged while the entrypoint names it as the fix")
	}
}
