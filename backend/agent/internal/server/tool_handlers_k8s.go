package server

// Tool handlers for cluster inspection: node inventory, Kubernetes
// operations and deployment checks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/user/doops/agent/api"
)

func (gw *Gateway) handleToolNodeInfo(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON, pushProgress := call.ctx, call.reqID, call.args, call.writeJSON, call.pushProgress
	var args api.NodeInfoParams
	json.Unmarshal(argBytes, &args)
	if args.Section != "" {
		var (
			result string
			err    error
		)
		switch args.Section {
		case "admission":
			result, err = readDoagentAdmission(ctx, configuredDoagentURL())
		case "vm_metrics":
			result, err = collectAgentVMMetricsJSON(ctx)
		case "container_metrics":
			result, err = collectAgentContainerMetricsJSON(ctx)
		case "app_metrics":
			result, err = collectAgentAppMetricsJSON(ctx, args.Targets)
		default:
			writeJSON(buildErrorResponse(reqID, -32602, "unsupported node info section"))
			return
		}
		if err != nil {
			writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
		} else {
			writeJSON(buildSuccessResponse(reqID, result))
		}
		return
	}

	cmdStr := strings.Join([]string{
		"echo '--- System Info ---'",
		"uname -a",
		"echo ''",
		"echo '--- Uptime ---'",
		"uptime",
		"echo ''",
		"echo '--- Memory ---'",
		"free -m 2>/dev/null || echo 'free command not found'",
		"echo ''",
		"echo '--- Disk ---'",
		"df -h /",
		"echo ''",
		"echo '--- Capabilities ---'",
		"if command -v kubectl >/dev/null 2>&1; then echo 'kubectl: OK '$(command -v kubectl); else echo 'kubectl: MISSING'; fi",
		"if command -v nerdctl >/dev/null 2>&1; then echo 'container-runtime: nerdctl '$(command -v nerdctl); elif command -v docker >/dev/null 2>&1; then echo 'container-runtime: docker '$(command -v docker); elif command -v podman >/dev/null 2>&1; then echo 'container-runtime: podman '$(command -v podman); else echo 'container-runtime: MISSING'; fi",
		"if command -v buildctl >/dev/null 2>&1; then echo 'buildctl: OK '$(command -v buildctl); else echo 'buildctl: MISSING'; fi",
		"if command -v skopeo >/dev/null 2>&1; then echo 'skopeo: OK '$(command -v skopeo); else echo 'skopeo: MISSING'; fi",
		"if [ -S /run/buildkit/buildkitd.sock ]; then echo 'buildkit-sock: OK /run/buildkit/buildkitd.sock'; else echo 'buildkit-sock: MISSING'; fi",
		"if [ ! -e /run/buildkit/buildkitd.sock ]; then echo 'image-build: MISSING no buildkitd socket'; elif buildctl --addr unix:///run/buildkit/buildkitd.sock debug workers >/dev/null 2>&1; then echo 'image-build: OK'; else echo 'image-build: UNAVAILABLE buildkitd requires a privileged container'; fi",
		"if [ -f /root/.kube/config ]; then echo 'kubeconfig: OK /root/.kube/config'; elif [ -f /etc/rancher/k3s/k3s.yaml ]; then echo 'kubeconfig: OK /etc/rancher/k3s/k3s.yaml'; elif [ -f /etc/kubernetes/admin.conf ]; then echo 'kubeconfig: OK /etc/kubernetes/admin.conf'; else echo 'kubeconfig: MISSING'; fi",
	}, "; ")
	execCtx, cancelExec := context.WithTimeout(ctx, maxToolExecutionDuration())
	defer cancelExec()
	cmd, stdoutPipe, stderrPipe, err := executeRawCommand(execCtx, cmdStr)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
		return
	}

	cmdErr := waitCommandStreams(execCtx, cmd, stdoutPipe, stderrPipe, pushProgress, false)
	if errors.Is(cmdErr, errExecutionOutcomeUnknown) {
		writeJSON(buildErrorResponse(reqID, -32603, cmdErr.Error()))
		return
	}
	if execCtx.Err() != nil {
		writeJSON(buildErrorResponse(reqID, -32007, "operation canceled"))
		return
	}

	if cmdErr != nil {
		finalText := fmt.Sprintf("Command failed with error: %v", cmdErr)
		writeJSON(buildToolErrorResponse(reqID, finalText))
	} else {
		writeJSON(buildStreamedCommandSuccessResponse(reqID))
	}
}

func (gw *Gateway) handleToolK8s(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON, pushProgress := call.ctx, call.reqID, call.args, call.writeJSON, call.pushProgress
	result, err := handleK8STool(ctx, argBytes, pushProgress)
	if errors.Is(err, errExecutionOutcomeUnknown) {
		writeJSON(buildErrorResponse(reqID, -32603, err.Error()))
	} else if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}

func (gw *Gateway) handleToolCheckDeployment(call toolInvocation) {
	reqID, argBytes, writeJSON := call.reqID, call.args, call.writeJSON
	result, err := handleCheckDeployment(argBytes)
	if err != nil {
		writeJSON(buildToolErrorResponse(reqID, err.Error()))
	} else {
		writeJSON(buildSuccessResponse(reqID, result))
	}
}
