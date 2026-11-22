package server

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// targetRuntime is what the Agent can observe about the target it runs on.
// A target is not always Kubernetes: it may be a standalone container or a
// plain Linux host, and a mounted kubeconfig does not prove a reachable API.
type targetRuntime struct {
	Kubernetes   bool   // the Kubernetes API answered with the Agent's own identity
	InCluster    bool   // the Agent runs in a Pod
	Container    bool   // the Agent runs in a container
	ContainerCLI string // nerdctl, docker or podman when one is installed
}

const targetRuntimeProbeTTL = 30 * time.Second

var (
	targetRuntimeMu      sync.Mutex
	targetRuntimeCached  targetRuntime
	targetRuntimeExpires time.Time

	probeKubernetesAPI = func(ctx context.Context) bool {
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "kubectl", "version", "--request-timeout=4s").Run() == nil
	}
)

func currentTargetRuntime(ctx context.Context) targetRuntime {
	targetRuntimeMu.Lock()
	defer targetRuntimeMu.Unlock()
	if time.Now().Before(targetRuntimeExpires) {
		return targetRuntimeCached
	}
	runtime := targetRuntime{InCluster: os.Getenv("KUBERNETES_SERVICE_HOST") != ""}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			runtime.Container = true
		}
	}
	runtime.Container = runtime.Container || runtime.InCluster
	for _, cli := range []string{"nerdctl", "docker", "podman"} {
		if _, err := exec.LookPath(cli); err == nil {
			runtime.ContainerCLI = cli
			break
		}
	}
	runtime.Kubernetes = probeKubernetesAPI(ctx)
	if ctx.Err() == nil {
		targetRuntimeCached, targetRuntimeExpires = runtime, time.Now().Add(targetRuntimeProbeTTL)
	}
	return runtime
}

// promptFacts is appended to the session system prompt so the model starts
// from the target's real shape instead of assuming a cluster.
func (r targetRuntime) promptFacts() string {
	process := "Linux 主机原生进程"
	switch {
	case r.InCluster:
		process = "Kubernetes Pod 内"
	case r.Container:
		process = "独立容器内（非 Kubernetes 编排）"
	}
	kubernetes := "不可用（未配置或当前不可达），本会话不提供 `mcp_doops_k8s_read`"
	if r.Kubernetes {
		kubernetes = "可用"
	}
	cli := "无"
	if r.ContainerCLI != "" {
		cli = r.ContainerCLI
	}
	return strings.Join([]string{
		"", "# 当前 target 运行时", "",
		"- Agent 进程：" + process,
		"- Kubernetes API：" + kubernetes,
		"- 容器运行时 CLI：" + cli, "",
	}, "\n")
}
