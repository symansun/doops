package main

import (
	"strings"
	"testing"
)

func TestBuildRegistrySyncCommandUsesSkopeoAndDigestVerification(t *testing.T) {
	command := buildRegistrySyncCommand(
		"docker.cnb.cool/l8ai/amp",
		"repo.example:8443/oilan-system",
		"release-20261004",
		"/run/doops/source-auth.json",
		"/run/doops/target-auth.json",
		[]string{"amp-backend", "amp-gateway"},
		600,
		true,
	)
	for _, want := range []string{
		"skopeo copy --all --retry-times 3",
		"/run/doops/source-auth.json",
		"/run/doops/target-auth.json",
		"docker://docker.cnb.cool/l8ai/amp/amp-backend:release-20261004",
		"docker://repo.example:8443/oilan-system/amp-gateway:release-20261004",
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
