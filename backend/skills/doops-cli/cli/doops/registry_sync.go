package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var registrySyncTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)

func runRegistryCommand(args []string, sessionName string, servers []Server, configErr error, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "sync" {
		return fmt.Errorf("用法: doops registry sync --target <target> --source-registry <registry/path> --target-registry <registry/path> --tag <tag> --source-authfile <remote-path> --target-authfile <remote-path> --image <name> (可重复)")
	}
	if sessionName == "" {
		return fmt.Errorf("-session 必传")
	}
	if configErr != nil {
		return configErr
	}

	registryFlags := flag.NewFlagSet("registry sync", flag.ContinueOnError)
	registryFlags.SetOutput(os.Stdout)
	var target, sourceRegistry, targetRegistry, tag, sourceAuthfile, targetAuthfile string
	var timeoutSeconds int
	var installSkopeo bool
	var images stringListFlag
	registryFlags.StringVar(&target, "target", "", "Configured Gateway target")
	registryFlags.StringVar(&sourceRegistry, "source-registry", "", "Source registry/repository prefix")
	registryFlags.StringVar(&targetRegistry, "target-registry", "", "Target registry/repository prefix")
	registryFlags.StringVar(&tag, "tag", "", "Immutable release tag")
	registryFlags.StringVar(&sourceAuthfile, "source-authfile", "", "Remote source registry authfile path")
	registryFlags.StringVar(&targetAuthfile, "target-authfile", "", "Remote target registry authfile path")
	registryFlags.IntVar(&timeoutSeconds, "timeout", 600, "Timeout per copy/inspect operation in seconds")
	registryFlags.BoolVar(&installSkopeo, "install-skopeo", false, "Install skopeo with the target package manager when missing")
	registryFlags.Var(&images, "image", "Image name without registry/tag (repeatable)")
	if err := registryFlags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if target == "" || sourceRegistry == "" || targetRegistry == "" || tag == "" || sourceAuthfile == "" || targetAuthfile == "" || len(images) == 0 {
		return fmt.Errorf("registry sync requires --target, --source-registry, --target-registry, --tag, --source-authfile, --target-authfile and at least one --image")
	}
	if timeoutSeconds <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	for name, value := range map[string]string{
		"--source-registry": sourceRegistry,
		"--target-registry": targetRegistry,
		"--tag":             tag,
	} {
		if !registrySyncTokenPattern.MatchString(value) {
			return fmt.Errorf("%s contains unsupported characters", name)
		}
	}
	for _, image := range images {
		if !registrySyncTokenPattern.MatchString(image) {
			return fmt.Errorf("--image contains unsupported characters: %s", image)
		}
	}

	server := findServer(servers, target)
	if server == nil {
		return fmt.Errorf("server %q not found", target)
	}
	client := NewMCPClient(*server, NewSessionStore(), sessionName, false)
	client.Token = mustResolveToken(*server)
	defer client.Close()

	command := buildRegistrySyncCommand(sourceRegistry, targetRegistry, tag, sourceAuthfile, targetAuthfile, images, timeoutSeconds, installSkopeo)
	if err := client.Call("doops_shell", map[string]interface{}{"command": command}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "registry_sync=complete target=%s images=%d tag=%s\n", target, len(images), tag)
	return nil
}

func buildRegistrySyncCommand(sourceRegistry, targetRegistry, tag, sourceAuthfile, targetAuthfile string, images []string, timeoutSeconds int, installSkopeo bool) string {
	parts := []string{"set -eu", "command -v timeout >/dev/null 2>&1", "command -v sha256sum >/dev/null 2>&1"}
	if installSkopeo {
		parts = append(parts, `if ! command -v skopeo >/dev/null 2>&1; then if command -v dnf >/dev/null 2>&1; then sudo -n dnf install -y skopeo; elif command -v apt-get >/dev/null 2>&1; then sudo -n apt-get update && sudo -n apt-get install -y skopeo; elif command -v apk >/dev/null 2>&1; then sudo -n apk add --no-cache skopeo; else echo 'skopeo installation is unsupported on this target' >&2; exit 1; fi; fi`)
	} else {
		parts = append(parts, "command -v skopeo >/dev/null 2>&1")
	}
	for _, image := range images {
		sourceRef := sourceRegistry + "/" + image + ":" + tag
		targetRef := targetRegistry + "/" + image + ":" + tag
		parts = append(parts, fmt.Sprintf("timeout %ds skopeo copy --all --retry-times 3 --src-authfile %s --dest-authfile %s %s %s", timeoutSeconds, shellQuote(sourceAuthfile), shellQuote(targetAuthfile), shellQuote("docker://"+sourceRef), shellQuote("docker://"+targetRef)))
		parts = append(parts, fmt.Sprintf("src_digest=$(timeout %ds skopeo inspect --raw --authfile %s %s | sha256sum | cut -d' ' -f1)", timeoutSeconds, shellQuote(sourceAuthfile), shellQuote("docker://"+sourceRef)))
		parts = append(parts, fmt.Sprintf("dst_digest=$(timeout %ds skopeo inspect --raw --authfile %s %s | sha256sum | cut -d' ' -f1)", timeoutSeconds, shellQuote(targetAuthfile), shellQuote("docker://"+targetRef)))
		parts = append(parts, fmt.Sprintf("test -n \"${src_digest}\" && test \"${src_digest}\" = \"${dst_digest}\" || { echo 'registry manifest digest mismatch for %s' >&2; exit 1; }", image))
	}
	return strings.Join(parts, "\n")
}
