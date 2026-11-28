package server

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type checkDeploymentParams struct {
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
	Image      string `json:"image"`
}

func handleCheckDeployment(argBytes json.RawMessage) (string, error) {
	var args checkDeploymentParams
	if err := json.Unmarshal(argBytes, &args); err != nil {
		return "", fmt.Errorf("invalid check deployment params: %w", err)
	}
	namespace := strings.TrimSpace(args.Namespace)
	if namespace == "" {
		namespace = "default"
	}
	deployment := strings.TrimSpace(args.Deployment)
	wantImage := strings.TrimSpace(args.Image)
	if deployment == "" || wantImage == "" {
		return "", fmt.Errorf("namespace, deployment and image are required")
	}
	cmd := exec.Command("kubectl", "get", "deploy", deployment, "-n", namespace, "-o", "jsonpath={.spec.template.spec.containers[0].image}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl get deploy %s/%s failed: %v: %s", namespace, deployment, err, strings.TrimSpace(string(out)))
	}
	got := strings.Trim(strings.TrimSpace(string(out)), `"'`)
	if got == "" {
		return "", fmt.Errorf("empty image from kubectl for %s/%s", namespace, deployment)
	}
	if !imageRefMatches(wantImage, got) {
		return "", fmt.Errorf("image mismatch: want %q got %q", wantImage, got)
	}
	return fmt.Sprintf("check ok: %s/%s -> %s", namespace, deployment, got), nil
}

func imageRefMatches(want, got string) bool {
	want = strings.TrimSpace(want)
	got = strings.TrimSpace(got)
	if want == got {
		return true
	}
	if strings.HasPrefix(got, want+":") || strings.HasPrefix(got, want+"@") {
		return true
	}
	if strings.HasPrefix(want, got+":") || strings.HasPrefix(want, got+"@") {
		return true
	}
	return false
}
