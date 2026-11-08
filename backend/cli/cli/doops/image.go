package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const buildkitSocket = "unix:///run/buildkit/buildkitd.sock"

func runImageCommand(args []string, sessionName string, servers []Server, configErr error, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: doops image build|copy ...")
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		fmt.Fprintln(stdout, "用法: doops -session <s> image build|copy --target <t> ...\n查看参数: doops image build -h / doops image copy -h")
		return nil
	case "build":
		return runImageBuild(args[1:], sessionName, servers, configErr, stdout)
	case "copy":
		return runImageCopy(args[1:], sessionName, servers, configErr, stdout)
	default:
		return fmt.Errorf("不支持的 image 操作 %q；可用 build、copy", args[0])
	}
}

func runImageBuild(args []string, sessionName string, servers []Server, configErr error, stdout io.Writer) error {
	flags := flag.NewFlagSet("image build", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	target := flags.String("target", "", "Configured Gateway target")
	contextDir := flags.String("context", "", "Remote build context directory")
	dockerfile := flags.String("dockerfile", "", "Remote Dockerfile path; defaults to <context>/Dockerfile")
	platform := flags.String("platform", "linux/amd64", "Target platform(s), comma separated")
	outputType := flags.String("output", "oci", "Output type: oci or image")
	outputPath := flags.String("output-path", "", "Remote OCI archive path when --output=oci")
	image := flags.String("image", "", "Image reference when --output=image")
	push := flags.Bool("push", false, "Push the image through BuildKit when --output=image")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	sessionName = resolveSession(sessionName)
	if configErr != nil {
		return configErr
	}
	if strings.TrimSpace(*target) == "" || strings.TrimSpace(*contextDir) == "" {
		return fmt.Errorf("image build requires --target and --context")
	}
	if *outputType != "oci" && *outputType != "image" {
		return fmt.Errorf("--output must be oci or image")
	}
	if *outputType == "oci" && strings.TrimSpace(*outputPath) == "" {
		return fmt.Errorf("--output=oci requires --output-path")
	}
	if *outputType == "image" && strings.TrimSpace(*image) == "" {
		return fmt.Errorf("--output=image requires --image")
	}
	if *outputType == "oci" && *push {
		return fmt.Errorf("--push requires --output=image")
	}

	dockerfilePath := strings.TrimSpace(*dockerfile)
	if dockerfilePath == "" {
		dockerfilePath = path.Join(strings.TrimSpace(*contextDir), "Dockerfile")
	}
	dockerfileDir := path.Dir(dockerfilePath)
	dockerfileName := path.Base(dockerfilePath)
	if dockerfileName == "." || dockerfileName == "/" || dockerfileName == "" {
		return fmt.Errorf("--dockerfile must name a file")
	}

	server := findServer(servers, *target)
	if server == nil {
		return errTargetNotFound(servers, *target)
	}
	client := NewMCPClient(*server, NewSessionStore(), sessionName, false)
	client.Token = mustResolveToken(*server)
	defer client.Close()

	command := buildImageBuildCommand(strings.TrimSpace(*contextDir), dockerfileDir, dockerfileName,
		strings.TrimSpace(*platform), *outputType, strings.TrimSpace(*outputPath), strings.TrimSpace(*image), *push)
	if err := client.Call("doops_shell", map[string]interface{}{"command": command}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "image_build=complete target=%s output=%s\n", *target, *outputType)
	return nil
}

func buildImageBuildCommand(contextDir, dockerfileDir, dockerfileName, platform, outputType, outputPath, image string, push bool) string {
	parts := []string{
		"set -eu",
		"command -v buildctl >/dev/null 2>&1",
		"test -S /run/buildkit/buildkitd.sock",
		fmt.Sprintf("buildctl --addr %s build --progress=plain --frontend dockerfile.v0 --local context=%s --local dockerfile=%s --opt filename=%s",
			shellQuote(buildkitSocket), shellQuote(contextDir), shellQuote(dockerfileDir), shellQuote(dockerfileName)),
	}
	if platform != "" {
		parts[3] += " --opt " + shellQuote("platform="+platform)
	}
	if outputType == "oci" {
		parts[3] += " --output " + shellQuote("type=oci,dest="+outputPath)
	} else {
		pushValue := "false"
		if push {
			pushValue = "true"
		}
		parts[3] += " --output " + shellQuote("type=image,name="+image+",push="+pushValue)
	}
	return strings.Join(parts, "\n")
}

func runImageCopy(args []string, sessionName string, servers []Server, configErr error, stdout io.Writer) error {
	flags := flag.NewFlagSet("image copy", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	target := flags.String("target", "", "Configured Gateway target")
	source := flags.String("source", "", "Source image reference, including transport")
	destination := flags.String("destination", "", "Destination image reference, including transport")
	sourceAuthfile := flags.String("source-authfile", "", "Remote source authfile path")
	destinationAuthfile := flags.String("destination-authfile", "", "Remote destination authfile path")
	all := flags.Bool("all", true, "Copy all image manifests")
	verify := flags.Bool("verify", true, "Compare source and destination raw manifest digests")
	timeoutSeconds := flags.Int("timeout", 600, "Timeout per copy/inspect operation in seconds")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	sessionName = resolveSession(sessionName)
	if configErr != nil {
		return configErr
	}
	if strings.TrimSpace(*target) == "" || strings.TrimSpace(*source) == "" || strings.TrimSpace(*destination) == "" {
		return fmt.Errorf("image copy requires --target, --source and --destination")
	}
	if *timeoutSeconds <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}

	server := findServer(servers, *target)
	if server == nil {
		return errTargetNotFound(servers, *target)
	}
	client := NewMCPClient(*server, NewSessionStore(), sessionName, false)
	client.Token = mustResolveToken(*server)
	defer client.Close()

	command := buildImageCopyCommand(strings.TrimSpace(*source), strings.TrimSpace(*destination),
		strings.TrimSpace(*sourceAuthfile), strings.TrimSpace(*destinationAuthfile), *all, *verify, *timeoutSeconds)
	if err := client.Call("doops_shell", map[string]interface{}{"command": command}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "image_copy=complete target=%s verify=%t\n", *target, *verify)
	return nil
}

func buildImageCopyCommand(source, destination, sourceAuthfile, destinationAuthfile string, all, verify bool, timeoutSeconds int) string {
	parts := []string{"set -eu", "command -v timeout >/dev/null 2>&1", "command -v skopeo >/dev/null 2>&1"}
	if verify {
		parts = append(parts, "command -v sha256sum >/dev/null 2>&1")
	}
	copyArgs := []string{"skopeo", "copy"}
	if all {
		copyArgs = append(copyArgs, "--all")
	}
	copyArgs = append(copyArgs, "--retry-times", "3")
	if sourceAuthfile != "" {
		copyArgs = append(copyArgs, "--src-authfile", sourceAuthfile)
	}
	if destinationAuthfile != "" {
		copyArgs = append(copyArgs, "--dest-authfile", destinationAuthfile)
	}
	copyArgs = append(copyArgs, source, destination)
	parts = append(parts, fmt.Sprintf("timeout %ds %s", timeoutSeconds, shellJoin(copyArgs)))
	if verify {
		srcInspect := []string{"skopeo", "inspect", "--raw"}
		if sourceAuthfile != "" {
			srcInspect = append(srcInspect, "--authfile", sourceAuthfile)
		}
		srcInspect = append(srcInspect, source)
		dstInspect := []string{"skopeo", "inspect", "--raw"}
		if destinationAuthfile != "" {
			dstInspect = append(dstInspect, "--authfile", destinationAuthfile)
		}
		dstInspect = append(dstInspect, destination)
		parts = append(parts,
			fmt.Sprintf("src_digest=$(timeout %ds %s | sha256sum | cut -d' ' -f1)", timeoutSeconds, shellJoin(srcInspect)),
			fmt.Sprintf("dst_digest=$(timeout %ds %s | sha256sum | cut -d' ' -f1)", timeoutSeconds, shellJoin(dstInspect)),
			fmt.Sprintf("test -n \"${src_digest}\" && test \"${src_digest}\" = \"${dst_digest}\" || { echo 'image manifest digest mismatch' >&2; exit 1; }"),
		)
	}
	return strings.Join(parts, "\n")
}

func shellJoin(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellQuote(value))
	}
	return strings.Join(quoted, " ")
}
