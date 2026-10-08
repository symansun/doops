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
