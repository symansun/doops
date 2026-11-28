package server

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// nodeInfoCapabilityLine returns the image-build probe the doops_node_info
// diagnostic runs, so the test exercises the real script rather than a copy.
func nodeInfoCapabilityLine(t *testing.T) string {
	t.Helper()
	// The probe lives beside the other doops_node_info commands. Scan the
	// package files so a file split does not make this test look for a stale
	// path instead of the real script.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(source), "\n") {
			if strings.Contains(line, "'image-build: ") {
				start, end := strings.Index(line, `"`), strings.LastIndex(line, `",`)
				if start < 0 || end <= start {
					t.Fatalf("malformed probe line in %s: %s", entry.Name(), line)
				}
				return line[start+1 : end]
			}
		}
	}
	t.Fatal("doops_node_info lost its image-build capability probe")
	return ""
}

func runNodeInfoProbe(t *testing.T, socket, buildctl string) string {
	t.Helper()
	dir := t.TempDir()
	script := strings.ReplaceAll(nodeInfoCapabilityLine(t), "/run/buildkit/buildkitd.sock", socket)
	path := dir + "/probe.sh"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := dir + "/bin"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if buildctl != "" {
		if err := os.WriteFile(bin+"/buildctl", []byte(buildctl), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", path)
	cmd.Env = []string{"PATH=" + bin}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestNodeInfoReportsWhetherImageBuildWorks makes the diagnostic tell an
// operator that a non-privileged Agent cannot build images, instead of
// reporting a socket that exists but cannot serve a build.
func TestNodeInfoReportsWhetherImageBuildWorks(t *testing.T) {
	socket := t.TempDir() + "/buildkitd.sock"
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ socket, buildctl, want string }{
		{socket, "#!/bin/sh\nexit 0\n", "image-build: OK"},
		{socket, "#!/bin/sh\nexit 1\n", "image-build: UNAVAILABLE buildkitd requires a privileged container"},
		{socket + ".missing", "#!/bin/sh\nexit 0\n", "image-build: MISSING no buildkitd socket"},
	}
	for _, tt := range cases {
		if got := runNodeInfoProbe(t, tt.socket, tt.buildctl); got != tt.want {
			t.Fatalf("probe = %q, want %q", got, tt.want)
		}
	}
}
