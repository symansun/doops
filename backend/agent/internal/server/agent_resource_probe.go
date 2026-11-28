package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const agentResourceProbeMaxRows = 2048

func collectAgentResourceSnapshot(ctx context.Context) agentResourceSnapshot {
	runtime := currentTargetRuntime(ctx)
	snapshot := agentResourceSnapshot{
		Mode: runtime.mode(), KubernetesAvailable: runtime.Kubernetes,
		Complete: []string{}, Resources: []agentResourceObservation{},
	}
	if ctx.Err() != nil {
		return snapshot
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	collect := func(kind string, probe func(context.Context) ([]agentResourceObservation, bool)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, complete := probe(ctx)
			mu.Lock()
			defer mu.Unlock()
			snapshot.Resources = append(snapshot.Resources, rows...)
			if complete {
				snapshot.Complete = append(snapshot.Complete, kind)
			}
		}()
	}
	collect("machine", func(ctx context.Context) ([]agentResourceObservation, bool) {
		return probeAgentMachine(ctx, runtime)
	})
	collect("site", probeAgentSites)
	if !runtime.InCluster {
		collect("container", probeAgentDocker)
	}
	if runtime.Kubernetes {
		collect("cluster", probeAgentCluster)
		collect("namespace", probeAgentNamespaces)
		collect("database", probeAgentDatabases)
	}
	wg.Wait()
	snapshot.canonicalize()
	return snapshot
}

func probeAgentCluster(ctx context.Context) ([]agentResourceObservation, bool) {
	data, err := runAgentResourceProbe(ctx, 256, "kubectl", "--request-timeout=4s", "get", "namespace", "kube-system", "--output=jsonpath={.metadata.uid}")
	uid := string(data)
	if err != nil || !validObservationToken(uid) {
		return nil, false
	}
	return []agentResourceObservation{{Kind: "cluster", Key: "current", Name: "current", Identity: uid}}, true
}

func probeAgentNamespaces(ctx context.Context) ([]agentResourceObservation, bool) {
	const fields = `jsonpath={range .items[*]}{.metadata.name}{"\t"}{.metadata.uid}{"\n"}{end}`
	data, err := runAgentResourceProbe(ctx, 512<<10, "kubectl", "--request-timeout=4s", "get", "namespaces", "--output="+fields)
	if err != nil {
		return nil, false
	}
	if len(data) == 0 {
		return nil, true
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > agentResourceProbeMaxRows {
		return nil, false
	}
	rows := make([]agentResourceObservation, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		name, uid, ok := strings.Cut(line, "\t")
		if !ok || !inventoryNamespacePattern.MatchString(name) || !validObservationToken(uid) || seen[name] {
			return nil, false
		}
		seen[name] = true
		rows = append(rows, agentResourceObservation{Kind: "namespace", Key: name, Name: name, Identity: uid})
	}
	return rows, true
}

var agentDockerContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func probeAgentDocker(ctx context.Context) ([]agentResourceObservation, bool) {
	// This is the daemon socket mounted by the standalone Agent deployment.
	const socket = "/var/run/docker.sock"
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, false
	}
	data, err := agentResourceProbeOutput(ctx, 2<<20, func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker", "--host=unix://"+socket, "ps", "--all", "--quiet", "--no-trunc")
		cmd.Env = append(os.Environ(), "DOCKER_HOST=", "DOCKER_CONTEXT=", "DOCKER_TLS=", "DOCKER_TLS_VERIFY=")
		return cmd
	})
	if err != nil {
		return nil, false
	}
	count, ok := countAgentDockerContainers(string(data))
	if !ok {
		return nil, false
	}
	return []agentResourceObservation{{Kind: "container", Key: "docker", Name: "docker", Count: count}}, true
}

func countAgentDockerContainers(data string) (int, bool) {
	if data == "" {
		return 0, true
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		if !agentDockerContainerID.MatchString(line) || seen[line] {
			return 0, false
		}
		seen[line] = true
	}
	return len(seen), true
}

var agentSiteID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func probeAgentSites(ctx context.Context) ([]agentResourceObservation, bool) {
	script := strings.TrimSpace(os.Getenv("DOWEB_SCRIPT"))
	if script == "" {
		script = "/app/skills/doweb/scripts/doweb.mjs"
	}
	if !filepath.IsAbs(script) || strings.Contains(script, "..") {
		return nil, false
	}
	node := strings.TrimSpace(os.Getenv("DOWEB_NODE"))
	if node == "" {
		node = "node"
	}
	data, err := agentResourceProbeOutput(ctx, 1<<20, func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, node, script, "site-list")
		cmd.Dir = filepath.Dir(script)
		return cmd
	})
	var result struct {
		Sites []struct {
			ID     string `json:"site_id"`
			Domain string `json:"domain"`
		}
	}
	if err != nil || json.Unmarshal(data, &result) != nil || result.Sites == nil || len(result.Sites) > agentResourceProbeMaxRows {
		return nil, false
	}
	rows := make([]agentResourceObservation, 0, len(result.Sites))
	seen := make(map[string]bool, len(result.Sites))
	for _, site := range result.Sites {
		if !agentSiteID.MatchString(site.ID) || !validObservationToken(site.ID) || seen[site.ID] {
			return nil, false
		}
		seen[site.ID] = true
		rows = append(rows, agentResourceObservation{Kind: "site", Key: site.ID, Name: site.ID, Identity: siteObservationIdentity(site.Domain)})
	}
	// DoWeb's publication history only covers the sites it manages.
	return rows, false
}

func runAgentResourceProbe(ctx context.Context, limit int, name string, args ...string) ([]byte, error) {
	return agentResourceProbeOutput(ctx, limit, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, name, args...)
	})
}

func agentResourceProbeOutput(ctx context.Context, limit int, command func(context.Context) *exec.Cmd) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := command(ctx)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 250 * time.Millisecond
	output := agentProbeBuffer{limit: limit}
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return output.data.Bytes(), nil
}

type agentProbeBuffer struct {
	data  bytes.Buffer
	limit int
}

func (b *agentProbeBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.data.Len() {
		return 0, errors.New("agent_resource_probe_output_too_large")
	}
	return b.data.Write(data)
}
