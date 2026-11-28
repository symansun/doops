package main

import (
	"strings"
	"testing"
)

func TestBuildImageBuildCommandUsesGenericBuildKit(t *testing.T) {
	command := buildImageBuildCommand("/root/ws/image", "/root/ws/image", "Dockerfile", "linux/amd64,linux/arm64", "oci", "/tmp/image.oci", "", false)
	for _, want := range []string{
		"command -v buildctl",
		"test -S /run/buildkit/buildkitd.sock",
		"buildctl --addr",
		"--frontend dockerfile.v0",
		"--opt 'platform=linux/amd64,linux/arm64'",
		"type=oci,dest=/tmp/image.oci",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("build command missing %q: %s", want, command)
		}
	}
}

func TestBuildImageCopyCommandSupportsGenericTransportsAndVerification(t *testing.T) {
	command := buildImageCopyCommand(
		"docker://registry.example/source/app:release",
		"oci-archive:/tmp/app.oci",
		"/run/source-auth.json",
		"",
		true,
		true,
		600,
	)
	for _, want := range []string{
		"command -v skopeo",
		"skopeo' 'copy' '--all' '--retry-times' '3' '--src-authfile' '/run/source-auth.json'",
		"docker://registry.example/source/app:release",
		"oci-archive:/tmp/app.oci",
		"skopeo' 'inspect' '--raw' '--authfile' '/run/source-auth.json'",
		"image manifest digest mismatch",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("copy command missing %q: %s", want, command)
		}
	}
}
