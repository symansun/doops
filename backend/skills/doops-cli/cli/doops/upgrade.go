package main

import (
	"fmt"
	"strings"
	"time"
)

type UpgradeOptions struct {
	Image     string
	Mode      string
	Namespace string
	Workload  string
	Container string
	DryRun    bool
	Session   string
}

func UpgradeAgents(base Server, opts UpgradeOptions, verbose bool) error {
	if strings.TrimSpace(opts.Image) == "" {
		return fmt.Errorf("image is required")
	}
	if !opts.DryRun {
		return fmt.Errorf("direct agent self-upgrade is prohibited; use an external release controller or GitOps")
	}
	if strings.TrimSpace(base.Gateway) == "" ||
		strings.TrimSpace(base.Cluster) == "" ||
		strings.TrimSpace(base.Instance) == "" {
		return fmt.Errorf("configured Gateway target is required")
	}
	token, err := ResolveToken(base)
	if err != nil {
		return err
	}
	targets, err := fetchGatewayTargets(base.Gateway, token)
	if err != nil {
		return err
	}
	selected := filterUpgradeTargets(targets, base.Cluster, base.Instance)
	if len(selected) == 0 {
		return fmt.Errorf("configured target is offline: %s/%s", base.Cluster, base.Instance)
	}
	session := strings.TrimSpace(opts.Session)
	if session == "" {
		session = fmt.Sprintf("upgrade_%d", time.Now().Unix())
	}
	failures := 0
	for _, target := range selected {
		server := base
		server.Name = target.Key
		server.Gateway = base.Gateway
		server.Cluster = target.Cluster
		server.Instance = target.Instance
		client := NewMCPClient(server, NewSessionStore(), session, verbose)
		client.Token = token
		fmt.Printf("==> upgrading %s/%s image=%s\n", target.Cluster, target.Instance, opts.Image)
		out, err := client.CallAndCapture("doops_agent_upgrade", map[string]interface{}{
			"image":     opts.Image,
			"mode":      opts.Mode,
			"namespace": opts.Namespace,
			"workload":  opts.Workload,
			"container": opts.Container,
			"dry_run":   opts.DryRun,
		})
		client.Close()
		if err != nil {
			fmt.Printf("FAIL %s/%s: %v\n", target.Cluster, target.Instance, err)
			failures++
			continue
		}
		fmt.Printf("OK %s/%s: %s\n", target.Cluster, target.Instance, strings.TrimSpace(out))
	}
	if failures > 0 {
		return fmt.Errorf("upgrade failed on %d/%d target(s)", failures, len(selected))
	}
	return nil
}

func filterUpgradeTargets(targets []GatewayTarget, cluster, instance string) []GatewayTarget {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	out := make([]GatewayTarget, 0, len(targets))
	for _, target := range targets {
		if cluster != "" && cluster != "*" && target.Cluster != cluster {
			continue
		}
		if instance != "" && instance != "*" && target.Instance != instance {
			continue
		}
		out = append(out, target)
	}
	return out
}
