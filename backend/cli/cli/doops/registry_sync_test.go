package main

import (
	"strings"
	"testing"
)

func TestBuildRegistrySyncCommandUsesGenericOCIFlow(t *testing.T) {
	command := buildRegistrySyncCommand(
		"registry.example.com/source/team",
		"registry.example.com/mirror/team",
		"release-20261004",
		"/run/doops/source-auth.json",
		"/run/doops/target-auth.json",
		[]string{"api", "worker"},
		600,
	)
	for _, want := range []string{
		"skopeo copy --all --retry-times 3",
		"/run/doops/source-auth.json",
		"/run/doops/target-auth.json",
		"docker://registry.example.com/source/team/api:release-20261004",
		"docker://registry.example.com/mirror/team/worker:release-20261004",
		"skopeo inspect --raw",
		"registry manifest digest mismatch",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("registry sync command missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "password") || strings.Contains(command, "token") {
		t.Fatalf("registry sync command contains credential payload terminology: %s", command)
	}
}

// Without an authfile the copy must rely on the registry authfile the Gateway
// delivered to the target, so no authfile flag reaches skopeo.
func TestBuildRegistrySyncCommandWithoutAuthfiles(t *testing.T) {
	command := buildRegistrySyncCommand(
		"registry.example.com/source/team",
		"registry.example.com/mirror/team",
		"release-20261004",
		"",
		"",
		[]string{"api"},
		600,
	)
	for _, want := range []string{
		"skopeo copy --all --retry-times 3",
		"docker://registry.example.com/source/team/api:release-20261004",
		"docker://registry.example.com/mirror/team/api:release-20261004",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("registry sync command missing %q: %s", want, command)
		}
	}
	for _, unwanted := range []string{"--src-authfile", "--dest-authfile", "--authfile"} {
		if strings.Contains(command, unwanted) {
			t.Fatalf("registry sync command carries %q without an authfile: %s", unwanted, command)
		}
	}
}
